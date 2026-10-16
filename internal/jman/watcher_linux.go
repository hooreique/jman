//go:build linux

package jman

import (
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type platformWatcher interface {
	events() <-chan string
	errors() <-chan error
	close() error
}

// Watch directories so atomic file replacement keeps reporting the new inode.
// Structural changes and queue overflow require a full scan and watch rebuild.
type linuxWatcher struct {
	flushC                   chan chan struct{}
	roots, sources, projects []string
	fd                       int
	paths                    map[int]string
	eventC                   chan string
	errorC                   chan error
	stop                     chan struct{}
	done                     chan struct{}
	once                     sync.Once
}

func newPlatformWatcher(roots []string, declared ...[]string) (*linuxWatcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	w := &linuxWatcher{flushC: make(chan chan struct{}), fd: fd, paths: map[int]string{}, eventC: make(chan string, 32), errorC: make(chan error, 1), stop: make(chan struct{})}
	var sources, projects []string
	if len(declared) > 0 {
		sources = declared[0]
	}
	if len(declared) > 1 {
		projects = declared[1]
	}
	w.roots, w.sources, w.projects = roots, sources, projects
	seen := map[string]bool{}
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			if !scanPathIncluded(path, true, []string{root}, sources, projects) {
				ancestor := intersectsSourceRoot(path, sources)
				for _, project := range projects {
					ancestor = ancestor || withinRoot(project, path)
				}
				if !ancestor {
					return filepath.SkipDir
				}
			}
			if seen[path] {
				return nil
			}
			wd, err := syscall.InotifyAddWatch(fd, path, syscall.IN_MODIFY|syscall.IN_ATTRIB|syscall.IN_CLOSE_WRITE|syscall.IN_CREATE|syscall.IN_DELETE|syscall.IN_MOVED_FROM|syscall.IN_MOVED_TO|syscall.IN_DELETE_SELF|syscall.IN_MOVE_SELF|syscall.IN_ONLYDIR)
			if err != nil {
				return err
			}
			seen[path] = true
			w.paths[wd] = path
			return nil
		})
		if err != nil {
			_ = w.close()
			return nil, err
		}
	}
	w.done = make(chan struct{})
	go w.run()
	return w, nil
}

func (w *linuxWatcher) run() {
	defer close(w.done)
	defer close(w.eventC)
	defer close(w.errorC)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	buffer := make([]byte, 64*1024)
	var flushed chan struct{}
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		n, err := syscall.Read(w.fd, buffer)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN {
			if flushed != nil {
				close(flushed)
				flushed = nil
			}
			select {
			case <-w.stop:
				return
			case <-ticker.C:
			case flushed = <-w.flushC:
			}
			continue
		}
		if err != nil {
			select {
			case w.errorC <- err:
			case <-w.stop:
			}
			return
		}
		w.handleEvents(buffer[:n])
	}
}
func (w *linuxWatcher) handleEvents(buffer []byte) {
	n := len(buffer)
	for offset := 0; offset+syscall.SizeofInotifyEvent <= n; {
		event := buffer[offset:]
		wd := int(int32(binary.NativeEndian.Uint32(event)))
		mask := binary.NativeEndian.Uint32(event[4:])
		length := int(binary.NativeEndian.Uint32(event[12:]))
		end := offset + syscall.SizeofInotifyEvent + length
		if end > n {
			w.emit("")
			break
		}
		name := strings.TrimRight(string(buffer[offset+syscall.SizeofInotifyEvent:end]), "\x00")
		path, known := w.paths[wd]
		switch {
		case !known || mask&(syscall.IN_Q_OVERFLOW|syscall.IN_IGNORED|syscall.IN_DELETE_SELF|syscall.IN_MOVE_SELF|syscall.IN_UNMOUNT) != 0:
			w.emit("")
		case mask&syscall.IN_ISDIR != 0:
			w.emit("")
		case trackedFile(name) && scanPathIncluded(filepath.Join(path, name), false, w.roots, w.sources, w.projects):
			w.emit(filepath.Join(path, name))
		}
		offset = end
	}
}

func (w *linuxWatcher) emit(path string) {
	select {
	case w.eventC <- path:
	case <-w.stop:
	}
}
func (w *linuxWatcher) events() <-chan string { return w.eventC }
func (w *linuxWatcher) errors() <-chan error  { return w.errorC }
func (w *linuxWatcher) close() error {
	var err error
	w.once.Do(func() {
		close(w.stop)
		if w.done != nil {
			<-w.done
		}
		err = syscall.Close(w.fd)
	})
	return err
}

// Wait until the reader has drained events already queued in the kernel.
func (w *linuxWatcher) flush() {
	ack := make(chan struct{})
	select {
	case w.flushC <- ack:
		select {
		case <-ack:
		case <-w.done:
		}
	case <-w.done:
	}
}
