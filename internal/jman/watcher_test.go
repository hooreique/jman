package jman

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanDirtyUpdatesOnlyChangedFile(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "A.java")
	b := filepath.Join(root, "B.java")
	if err := os.WriteFile(a, []byte("class A {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("class B {}"), 0600); err != nil {
		t.Fatal(err)
	}
	files, _, _, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(root)
	s.files = files
	if err := os.WriteFile(a, []byte("class A { int value; }"), 0600); err != nil {
		t.Fatal(err)
	}
	next, _, _, err := s.scanDirty([]string{a})
	if err != nil {
		t.Fatal(err)
	}
	if next[a] == files[a] || next[b] != files[b] {
		t.Fatalf("unexpected incremental snapshot: %v", next)
	}
}

func TestGitMetadataDirFollowsLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	metadata := filepath.Join(root, "metadata")
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: metadata\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := gitMetadataDir(root)
	if err != nil || got != metadata {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWatcherRestoreMergesNewEvents(t *testing.T) {
	w := &workspaceWatcher{paths: map[string]struct{}{}}
	w.mark("A.java")
	all, paths := w.take()
	w.mark("B.java")
	w.restore(all, paths)
	all, paths = w.take()
	if all || len(paths) != 2 {
		t.Fatalf("restore lost an event: all=%v, paths=%v", all, paths)
	}
	w.mark("C.java")
	w.restore(true, nil)
	all, paths = w.take()
	if !all || len(paths) != 0 {
		t.Fatalf("full scan must dominate paths: all=%v, paths=%v", all, paths)
	}
}

type closedWatcher struct {
	eventC chan string
	errorC chan error
}

func (w closedWatcher) events() <-chan string { return w.eventC }
func (w closedWatcher) errors() <-chan error  { return w.errorC }
func (w closedWatcher) close() error          { return nil }

func TestWatcherSourceExitRequiresFullScan(t *testing.T) {
	for _, closeEvents := range []bool{false, true} {
		source := closedWatcher{eventC: make(chan string), errorC: make(chan error)}
		w := &workspaceWatcher{source: source, done: make(chan struct{}), paths: map[string]struct{}{}}
		if closeEvents {
			close(source.eventC)
		} else {
			close(source.errorC)
		}
		go w.run()
		select {
		case <-w.done:
		case <-time.After(time.Second):
			t.Fatal("watcher did not detect native source exit")
		}
		all, paths := w.take()
		if !all || len(paths) != 0 {
			t.Fatalf("native source exit left a clean snapshot: all=%v, paths=%v", all, paths)
		}
	}
}
