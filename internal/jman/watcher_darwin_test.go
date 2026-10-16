//go:build darwin

package jman

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitForWatch(t *testing.T, w *workspaceWatcher, path string, wantAll bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		all, paths := w.take()
		if all {
			if !wantAll {
				t.Fatal("ordinary file write requested a full scan")
			}
			return
		}
		for _, got := range paths {
			if !wantAll && got == path {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watcher did not report the expected change")
}

func TestDarwinWatcherFileLifecycle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Example.java")
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(path, "class Example {}")
	w, err := newWorkspaceWatcher([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { w.close() }()
	write(path, "class Example { int value; }")
	waitForWatch(t, w, path, false)
	// Atomic saves replace the watched inode. Rebuilding must watch the new one.
	tmp := filepath.Join(root, "replacement")
	write(tmp, "class Example { int other; }")
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, w, path, true)
	w.close()
	w, err = newWorkspaceWatcher([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	write(path, "class Example { int again; }")
	waitForWatch(t, w, path, false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, w, path, true)
}

func TestDarwinWatcherClosesIdleAndBackpressured(t *testing.T) {
	for _, busy := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "Example.java")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		w, err := newPlatformWatcher([]string{root})
		if err != nil {
			t.Fatal(err)
		}
		if busy {
			// Simulate an undrained event queue without a workspace consumer.
			for i := 0; i < cap(w.eventC); i++ {
				w.eventC <- path
			}
			if err := os.WriteFile(path, []byte("class Example {}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan struct{})
		go func() { _ = w.close(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("native watcher shutdown blocked")
		}
		_ = w.close()
	}
}

func TestDarwinWatcherDeclaredSourcesOverrideExcludes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "reports", "coverage")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "Example.java")
	if err := os.WriteFile(path, []byte("class Example {}"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := newWorkspaceWatcher([]string{root, source}, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	if err := os.WriteFile(path, []byte("class Example { int value; }"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, w, path, false)
}

func TestDarwinWatcherExcludesDeclaredSourceSiblings(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "reports", "coverage")
	tracked := filepath.Join(source, "Example.java")
	unrelated := []string{
		filepath.Join(root, "reports", "Unrelated.java"),
		filepath.Join(root, "reports", "other", "Other.java"),
		filepath.Join(root, "reports", "coverage-sibling", "Other.java"),
	}
	for _, path := range append([]string{tracked}, unrelated...) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newPlatformWatcher([]string{root, source}, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	for _, path := range w.paths {
		for _, excluded := range unrelated {
			if path == excluded || excluded != unrelated[0] && path == filepath.Dir(excluded) {
				t.Fatalf("watcher registered excluded path %s", path)
			}
		}
	}
	for _, path := range unrelated {
		if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case path := <-w.events():
		t.Fatalf("excluded file write emitted event %q", path)
	case err := <-w.errors():
		t.Fatal(err)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDarwinWatcherMissingDeclaredSourceRoot(t *testing.T) {
	s := newSession(t.TempDir())
	source := filepath.Join(s.root, "reports", "coverage")
	s.model = &BuildModel{Contexts: []BuildContext{{Roots: []string{source}}}}
	s.startWatcher()
	defer s.stop()
	if s.watcher == nil {
		t.Fatal("watcher unavailable")
	}
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, s.watcher, source, true)
	s.startWatcher()
	path := filepath.Join(source, "Example.java")
	if err := os.WriteFile(path, []byte("class Example {}"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForWatch(t, s.watcher, path, true)
}
