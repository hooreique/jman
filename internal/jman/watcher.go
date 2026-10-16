package jman

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// workspaceWatcher turns native filesystem activity into a conservative dirty
// set. Platform implementations watch all existing workspace directories;
// directory creation/removal is promoted to a full scan and watcher rebuild.
type workspaceWatcher struct {
	source  platformWatcher
	done    chan struct{}
	barrier chan chan struct{}

	mu       sync.Mutex
	dirtyAll bool
	paths    map[string]struct{}
}

func newWorkspaceWatcher(roots []string, declared ...[]string) (*workspaceWatcher, error) {
	source, err := newPlatformWatcher(roots, declared...)
	if err != nil {
		return nil, err
	}
	w := &workspaceWatcher{source: source, done: make(chan struct{}), barrier: make(chan chan struct{}), paths: map[string]struct{}{}}
	go w.run()
	return w, nil
}

func withinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Declared sources override exclusions only inside the source root, not in
// its ancestors or siblings. Actual project roots override their own directory
// name, but still exclude unrelated output directories below the project.
func scanPathIncluded(path string, directory bool, roots, sourceRoots []string, projects ...[]string) bool {
	for _, root := range sourceRoots {
		if withinRoot(path, root) {
			return true
		}
	}
	ordinaryRoots := append([]string{}, roots...)
	projectRoots := map[string]bool{}
	for _, paths := range projects {
		for _, root := range paths {
			projectRoots[root] = true
			ordinaryRoots = append(ordinaryRoots, root)
		}
	}
	for _, root := range ordinaryRoots {
		if !withinRoot(path, root) || ignoredScanDirectory(filepath.Base(root)) && !projectRoots[root] {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		if !directory {
			rel = filepath.Dir(rel)
		}
		ignored := false
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if ignoredScanDirectory(part) {
				ignored = true
				break
			}
		}
		if !ignored {
			return true
		}
	}
	return false
}

func intersectsSourceRoot(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if withinRoot(path, root) || withinRoot(root, path) {
			return true
		}
	}
	return false
}

func (w *workspaceWatcher) run() {
	defer close(w.done)
	for {
		select {
		case path, ok := <-w.source.events():
			if !ok {
				w.markAll()
				return
			}
			w.record(path)
		case ack := <-w.barrier:
			// The native flush has delivered its events; consume the buffered
			// remainder before acknowledging the snapshot boundary.
		drain:
			for {
				select {
				case path, ok := <-w.source.events():
					if !ok {
						w.markAll()
						break drain
					}
					w.record(path)
				default:
					break drain
				}
			}
			close(ack)
		case _, ok := <-w.source.errors():
			if !ok {
				w.markAll()
				return
			}
			w.markAll()
		}
	}
}

func (w *workspaceWatcher) record(path string) {
	if path == "" {
		w.markAll()
		return
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		w.markAll()
		return
	}
	w.mark(path)
}

func (w *workspaceWatcher) mark(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dirtyAll {
		return
	}
	w.paths[path] = struct{}{}
}

func (w *workspaceWatcher) markAll() {
	w.mu.Lock()
	w.dirtyAll = true
	w.paths = map[string]struct{}{}
	w.mu.Unlock()
}

func (w *workspaceWatcher) take() (bool, []string) {
	// Linux can synchronously drain inotify before trusting a cached snapshot.
	// A second barrier waits for this goroutine to apply those delivered events.
	if source, ok := w.source.(interface{ flush() }); ok && w.barrier != nil {
		source.flush()
		ack := make(chan struct{})
		select {
		case w.barrier <- ack:
			select {
			case <-ack:
			case <-w.done:
			}
		case <-w.done:
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	all := w.dirtyAll
	paths := make([]string, 0, len(w.paths))
	for path := range w.paths {
		paths = append(paths, path)
	}
	w.dirtyAll = false
	w.paths = map[string]struct{}{}
	return all, paths
}

func (w *workspaceWatcher) pending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dirtyAll || len(w.paths) > 0
}

func (w *workspaceWatcher) restore(all bool, paths []string) {
	if !all && len(paths) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if all {
		w.dirtyAll = true
		w.paths = map[string]struct{}{}
		return
	}
	if !w.dirtyAll {
		for _, path := range paths {
			w.paths[path] = struct{}{}
		}
	}
}

func (w *workspaceWatcher) close() {
	if w == nil {
		return
	}
	_ = w.source.close()
	<-w.done
}

func workspaceFingerprint(root string) (string, error) {
	git, err := gitMetadataDir(root)
	if err != nil {
		return "", err
	}
	paths := []string{
		filepath.Join(git, "HEAD"), filepath.Join(git, "index"),
		filepath.Join(root, "settings.gradle"), filepath.Join(root, "settings.gradle.kts"),
		filepath.Join(root, "build.gradle"), filepath.Join(root, "build.gradle.kts"),
		filepath.Join(root, "gradle.properties"), filepath.Join(root, ".jman.json"),
	}
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			parts = append(parts, path+"\x00missing")
			continue
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, path+"\x00"+info.Mode().String()+"\x00"+info.ModTime().UTC().String()+"\x00"+strconv.FormatInt(info.Size(), 10))
	}
	return digest([]byte(strings.Join(parts, "\n"))), nil
}

func gitMetadataDir(root string) (string, error) {
	path := filepath.Join(root, ".git")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil || info.IsDir() {
		return path, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir: "
	target := strings.TrimSpace(string(contents))
	if !strings.HasPrefix(target, prefix) {
		return "", errors.New("invalid .git file")
	}
	target = strings.TrimPrefix(target, prefix)
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return filepath.Clean(target), nil
}
