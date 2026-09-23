package jman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	max      int
	queue    chan struct{}
}

func (m *manager) acquire(ctx context.Context, root string) (*Session, error) {
	for {
		m.mu.Lock()
		s := m.sessions[root]
		if s == nil && len(m.sessions) >= m.max {
			var oldest *Session
			for _, candidate := range m.sessions {
				if len(candidate.gate) > 0 {
					continue
				}
				if oldest == nil || candidate.lastAccess().Before(oldest.lastAccess()) {
					oldest = candidate
				}
			}
			if oldest != nil {
				oldest.gate <- struct{}{}
				oldest.stop()
				delete(m.sessions, oldest.root)
				<-oldest.gate
			}
		}
		if s == nil && len(m.sessions) < m.max {
			s = newSession(root)
			m.sessions[root] = s
		}
		if s != nil {
			select {
			case s.gate <- struct{}{}:
				m.mu.Unlock()
				return s, nil
			default:
			}
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (m *manager) serve(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if req.URL.Path == "/health" {
		_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 1, "pid": os.Getpid()})
		return
	}
	if req.Method != "POST" || req.URL.Path != "/query" {
		http.NotFound(w, req)
		return
	}
	var q Request
	if e := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&q); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if q.Deadline.IsZero() {
		q.Deadline = time.Now().Add(120 * time.Second)
	}
	ctx, cancel := context.WithDeadline(req.Context(), q.Deadline)
	defer cancel()
	if q.Command != "status" && q.Command != "stop" {
		select {
		case m.queue <- struct{}{}:
			defer func() { <-m.queue }()
		default:
			_ = json.NewEncoder(w).Encode(failure(q, "not-ready", "QUEUE_FULL", fmt.Errorf("daemon queue is full"), "retry later"))
			return
		}
	}
	var response Response
	if q.Command == "stop" {
		response = reply(q)
		response.Results = append(response.Results, Result{Name: "daemon stopping"})
		defer func() {
			go func() { time.Sleep(100 * time.Millisecond); _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }()
		}()
	} else if q.Command == "status" {
		response = reply(q)
		m.mu.Lock()
		for _, s := range m.sessions {
			response.Results = append(response.Results, Result{Name: s.root, Detail: s.status()})
		}
		m.mu.Unlock()
	} else {
		root, e := Canonical(q.Project)
		if e != nil {
			response = failure(q, "error", "PROJECT_INVALID", e, "")
		} else {
			q.Project = root
			s, e := m.acquire(ctx, root)
			if e != nil {
				response = failure(q, "not-ready", "QUEUE_TIMEOUT", e, "retry with a longer --timeout")
			} else {
				response = s.Query(ctx, q)
				s.release()
			}
		}
	}
	_ = json.NewEncoder(w).Encode(response)
}
func RunDaemon(maxSessions int) error {
	if maxSessions < 1 {
		return fmt.Errorf("max-sessions must be positive")
	}
	socket := SocketPath()
	if e := os.MkdirAll(filepath.Dir(socket), 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(socket+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("daemon is already running: %w", e)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = os.Remove(socket)
	listener, e := net.Listen("unix", socket)
	if e != nil {
		return e
	}
	defer listener.Close()
	defer os.Remove(socket)
	if e = os.Chmod(socket, 0600); e != nil {
		return e
	}
	m := &manager{sessions: map[string]*Session{}, max: maxSessions, queue: make(chan struct{}, 64)}
	server := &http.Server{Handler: http.HandlerFunc(m.serve), ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() { <-ctx.Done(); _ = server.Close() }()
	// Idle eviction bounds long-lived memory, even if no new workspaces arrive.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.mu.Lock()
				for root, s := range m.sessions {
					if len(s.gate) == 0 && time.Since(s.lastAccess()) > 15*time.Minute {
						s.stop()
						delete(m.sessions, root)
					}
				}
				m.mu.Unlock()
			}
		}
	}()
	e = server.Serve(listener)
	m.mu.Lock()
	for _, s := range m.sessions {
		if err := s.acquire(context.Background()); err == nil {
			s.stop()
			s.release()
		}
	}
	m.mu.Unlock()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
func Client(ctx context.Context, q Request) (Response, error) {
	socket := SocketPath()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	ping := func() error {
		request, _ := http.NewRequestWithContext(ctx, "GET", "http://jman/health", nil)
		res, e := client.Do(request)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("daemon health: %s", res.Status)
		}
		return nil
	}
	if e := ping(); e != nil {
		if q.Command == "status" || q.Command == "stop" {
			return reply(q), nil
		}
		if e = os.MkdirAll(filepath.Dir(socket), 0700); e != nil {
			return Response{}, e
		}
		log, e := os.OpenFile(filepath.Join(filepath.Dir(socket), "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return Response{}, e
		}
		executable, e := os.Executable()
		if e != nil {
			log.Close()
			return Response{}, e
		}
		cmd := exec.Command(executable, "daemon")
		cmd.Stdout = log
		cmd.Stderr = log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		e = cmd.Start()
		log.Close()
		if e != nil {
			return Response{}, e
		}
		go cmd.Wait()
		for {
			if e = ping(); e == nil {
				break
			}
			select {
			case <-ctx.Done():
				return Response{}, fmt.Errorf("daemon startup: %w", ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	reader, writer := io.Pipe()
	go func() { err := json.NewEncoder(writer).Encode(q); _ = writer.CloseWithError(err) }()
	request, e := http.NewRequestWithContext(ctx, "POST", "http://jman/query", reader)
	if e != nil {
		return Response{}, e
	}
	request.Header.Set("Content-Type", "application/json")
	res, e := client.Do(request)
	if e != nil {
		reader.Close()
		return Response{}, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Response{}, fmt.Errorf("daemon response: %s", res.Status)
	}
	var response Response
	e = json.NewDecoder(res.Body).Decode(&response)
	return response, e
}
