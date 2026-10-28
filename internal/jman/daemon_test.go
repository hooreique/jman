package jman

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusDeadlineAndConcurrentSessionManagement(t *testing.T) {
	dir := fakeResourceCommands(t, "echo 123", "printf started > \"$JMAN_TEST_RESOURCE_STARTED\"\nsleep 1.5\necho 'garbage-first heap total 456K, used 78K'")
	marker := filepath.Join(dir, "started")
	t.Setenv("JMAN_TEST_RESOURCE_STARTED", marker)
	s := newSession("first")
	s.pid = 1234
	m := &manager{sessions: map[string]*Session{s.root: s}, max: 2, idleTimeout: time.Second}
	// Leave room for process startup under -race; the resource unit test
	// separately exercises the original 50ms deadline regression.
	q := Request{Command: "status", Deadline: time.Now().Add(500 * time.Millisecond)}
	body, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		m.serve(recorder, httptest.NewRequest("POST", "/query", bytes.NewReader(body)))
		close(done)
	}()
	defer func() { <-done }()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatal("status returned before querying JVM resources")
		case <-time.After(time.Millisecond):
		}
	}
	acquired := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		other, err := m.acquire(ctx, "second")
		if err == nil {
			other.release()
			m.evictIdle(time.Now().Add(2 * time.Second))
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("status resource lookup blocked session management")
	}
	select {
	case <-done:
	case <-time.After(800 * time.Millisecond):
		t.Fatal("status ignored request deadline")
	}
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("status exceeded deadline: %v", elapsed)
	}
	var response Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("status lost session snapshot: %#v", response)
	}
}

func TestSessionLimitProtectsBusyWorkspace(t *testing.T) {
	m := &manager{sessions: map[string]*Session{}, max: 1}
	first, e := m.acquire(context.Background(), "first")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e = m.acquire(ctx, "second"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("busy session was evicted: %v", e)
	}
	first.release()
	second, e := m.acquire(context.Background(), "second")
	if e != nil {
		t.Fatal(e)
	}
	defer second.release()
	if len(m.sessions) != 1 || m.sessions["first"] != nil {
		t.Fatalf("idle LRU not evicted: %v", m.sessions)
	}
}

func TestStopSessionsWaitsForBusySessionAndRejectsNewWork(t *testing.T) {
	m := &manager{sessions: map[string]*Session{}, max: 1}
	s, err := m.acquire(context.Background(), "busy")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.stopSessions(ctx); err == nil {
		t.Fatal("stop did not wait for busy session")
	}
	s.release()
	if err := m.stopSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.acquire(context.Background(), "new"); err == nil {
		t.Fatal("stopping daemon accepted new work")
	}
}
