//go:build darwin

package jman

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type platformWatcher interface {
	events() <-chan string
	errors() <-chan error
	close() error
}

type darwinWatcher struct {
	kq     int
	fds    []int
	paths  map[uint64]string
	eventC chan string
	errorC chan error
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newPlatformWatcher(roots []string, declared ...[]string) (*darwinWatcher, error) {
	kq, err := syscall.Kqueue()
	if err != nil {
		return nil, err
	}
	w := &darwinWatcher{kq: kq, paths: map[uint64]string{}, eventC: make(chan string, 32), errorC: make(chan error, 1), stop: make(chan struct{})}
	seen := map[string]struct{}{}
	var sourceRoots []string
	var projectRoots []string
	if len(declared) > 0 {
		sourceRoots = declared[0]
	}
	if len(declared) > 1 {
		projectRoots = declared[1]
	}
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		if err := w.addTree(root, seen, sourceRoots, projectRoots); err != nil {
			_ = w.close()
			return nil, err
		}
	}
	w.done = make(chan struct{})
	go w.run()
	return w, nil
}

func (w *darwinWatcher) addTree(root string, seen map[string]struct{}, sourceRoots, projectRoots []string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !scanPathIncluded(path, entry.IsDir(), []string{root}, sourceRoots, projectRoots) {
			if entry.IsDir() {
				// Traverse excluded ancestors only to reach declared sources/projects.
				ancestor := false
				for _, project := range projectRoots {
					ancestor = ancestor || withinRoot(project, path)
				}
				if !intersectsSourceRoot(path, sourceRoots) && !ancestor {
					return filepath.SkipDir
				}
			} else {
				return nil
			}
		}
		if !entry.IsDir() && !trackedFile(entry.Name()) {
			return nil
		}
		if _, ok := seen[path]; ok {
			return nil
		}
		fd, err := syscall.Open(path, syscall.O_EVTONLY, 0)
		if err != nil {
			return err
		}
		change := syscall.Kevent_t{Ident: uint64(fd), Filter: syscall.EVFILT_VNODE, Flags: syscall.EV_ADD | syscall.EV_CLEAR, Fflags: syscall.NOTE_WRITE | syscall.NOTE_DELETE | syscall.NOTE_RENAME | syscall.NOTE_EXTEND | syscall.NOTE_ATTRIB}
		if _, err = syscall.Kevent(w.kq, []syscall.Kevent_t{change}, nil, nil); err != nil {
			_ = syscall.Close(fd)
			return err
		}
		w.fds = append(w.fds, fd)
		w.paths[uint64(fd)] = path
		seen[path] = struct{}{}
		return nil
	})
}

func (w *darwinWatcher) run() {
	defer close(w.done)
	defer close(w.eventC)
	defer close(w.errorC)
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		var event [1]syscall.Kevent_t
		// Bound idle shutdown without closing descriptors under a running Kevent.
		timeout := syscall.Timespec{Nsec: 100_000_000}
		n, err := syscall.Kevent(w.kq, nil, event[:], &timeout)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			if err != syscall.EBADF {
				select {
				case w.errorC <- err:
				case <-w.stop:
				}
			}
			return
		}
		if n > 0 {
			path := w.paths[event[0].Ident]
			if event[0].Fflags&(syscall.NOTE_DELETE|syscall.NOTE_RENAME|syscall.NOTE_REVOKE) != 0 {
				// Vnode watches follow the old inode after an atomic save. Rebuild
				// rather than trusting a watch on an unlinked or renamed file.
				path = ""
			}
			select {
			case w.eventC <- path:
			case <-w.stop:
				return
			}
		}
	}
}

func (w *darwinWatcher) events() <-chan string { return w.eventC }
func (w *darwinWatcher) errors() <-chan error  { return w.errorC }
func (w *darwinWatcher) close() error {
	var err error
	w.once.Do(func() {
		close(w.stop)
		if w.done != nil {
			<-w.done
		}
		for _, fd := range w.fds {
			_ = syscall.Close(fd)
		}
		err = syscall.Close(w.kq)
	})
	return err
}
