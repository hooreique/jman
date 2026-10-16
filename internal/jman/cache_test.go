package jman

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hooreique/jman/internal/lsp"
)

// Exercise the real query/cache path without launching JDTLS. The peer handles
// sync commands and drains notifications, including cancellation requests.
func cachedTestSession(t *testing.T, syncReply func() (any, bool)) (*Session, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "Example.java")
	if err := os.WriteFile(path, []byte("class Example {}"), 0600); err != nil {
		t.Fatal(err)
	}
	s := newSession(root)
	var err error
	s.files, s.lastSnapshot, s.buildHash, err = s.scan()
	if err != nil {
		t.Fatal(err)
	}
	s.syncValid = true
	s.lastScan = time.Now()
	s.ready = make(chan struct{})
	close(s.ready)
	s.startWatcher()
	a, b := net.Pipe()
	s.rpc = lsp.New(a, a, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := bufio.NewReader(b)
		for {
			header, err := r.ReadString('\n')
			if err != nil {
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:")))
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(r, body); err != nil {
				return
			}
			var request struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			if len(request.ID) == 0 {
				continue
			}
			result, failed := syncReply()
			reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
			if failed {
				reply["error"] = map[string]any{"code": -32603, "message": "injected sync failure"}
			} else {
				reply["result"] = result
			}
			data, _ := json.Marshal(reply)
			if _, err := fmt.Fprintf(b, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
		<-done
		s.stop()
	})
	return s, path
}

func TestQueryRetainsChangesAfterDebounceTimeout(t *testing.T) {
	s, path := cachedTestSession(t, func() (any, bool) { return map[string]any{}, false })
	if err := os.WriteFile(path, []byte("class Example { int value; }"), 0600); err != nil {
		t.Fatal(err)
	}
	s.watcher.mark(path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := s.Query(ctx, Request{Command: "prepare"})
	if result.Error == nil || result.Error.Code != "SYNC_TIMEOUT" || !s.watcher.pending() || s.syncValid {
		t.Fatalf("lost changes after timeout: %#v", result)
	}
	result = s.Query(context.Background(), Request{Command: "prepare"})
	if result.Status != "ok" || s.files[path] != digest([]byte("class Example { int value; }")) {
		t.Fatalf("retry did not synchronize latest contents: %#v", result)
	}
}

func TestQueryRetriesFailedSyncAndPostSyncScan(t *testing.T) {
	for _, stage := range []string{"sync", "post-scan"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			missing := filepath.Join(t.TempDir(), "missing")
			s, path := cachedTestSession(t, func() (any, bool) {
				calls++
				if calls == 1 {
					if stage == "sync" {
						return nil, true
					}
					return map[string]any{"projectRoots": []string{missing}}, false
				}
				return map[string]any{}, false
			})
			if err := os.WriteFile(path, []byte("class Example { int value; }"), 0600); err != nil {
				t.Fatal(err)
			}
			s.watcher.mark(path)
			result := s.Query(context.Background(), Request{Command: "prepare"})
			if result.Error == nil || s.syncValid || s.watcher != nil && !s.watcher.pending() {
				t.Fatalf("failed %s left a valid cache: %#v", stage, result)
			}
			if stage == "post-scan" {
				if err := os.Mkdir(missing, 0700); err != nil {
					t.Fatal(err)
				}
			}
			result = s.Query(context.Background(), Request{Command: "prepare"})
			if result.Status != "ok" || calls < 2 || s.files[path] != digest([]byte("class Example { int value; }")) {
				t.Fatalf("retry did not recover: %#v (syncs %d)", result, calls)
			}
		})
	}
}

func TestCompositeFullAndIncrementalSnapshotsAgree(t *testing.T) {
	s := newSession(t.TempDir())
	extra := t.TempDir()
	s.extraRoots = []string{extra, extra, filepath.Join(extra, "nested")}
	if err := os.Mkdir(filepath.Join(extra, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(extra, "nested", "Example.java")
	if err := os.WriteFile(path, []byte("class Example {}"), 0600); err != nil {
		t.Fatal(err)
	}
	files, snapshot, build, err := s.scan()
	if err != nil {
		t.Fatal(err)
	}
	s.files = files
	_, incremental, incrementalBuild, err := s.scanDirty([]string{path})
	if err != nil || incremental != snapshot || incrementalBuild != build {
		t.Fatalf("full/incremental mismatch: %s/%s, build %s/%s, %v", snapshot, incremental, build, incrementalBuild, err)
	}
	if err := os.WriteFile(path, []byte("class Example { int value; }"), 0600); err != nil {
		t.Fatal(err)
	}
	_, snapshot, build, err = s.scan()
	if err != nil {
		t.Fatal(err)
	}
	_, incremental, incrementalBuild, err = s.scanDirty([]string{path})
	if err != nil || incremental != snapshot || incrementalBuild != build {
		t.Fatal("changed composite snapshots disagree")
	}
}

func TestQueryRetainsDirtyStateAfterArtifactFailure(t *testing.T) {
	s, _ := cachedTestSession(t, func() (any, bool) { return map[string]any{}, false })
	s.model = &BuildModel{Contexts: []BuildContext{{Artifacts: []Artifact{{Path: filepath.Join(t.TempDir(), "missing.jar")}}}}}
	s.watcher.markAll()
	result := s.Query(context.Background(), Request{Command: "prepare"})
	if result.Error == nil || result.Error.Code != "ARTIFACT_CHANGED" || s.syncValid || !s.watcher.pending() {
		t.Fatalf("artifact failure lost dirty state: %#v", result)
	}
	s.model = nil
	if result = s.Query(context.Background(), Request{Command: "prepare"}); result.Status != "ok" {
		t.Fatalf("artifact retry failed: %#v", result)
	}
}

func TestStatusConcurrentWithCacheUpdates(t *testing.T) {
	s, path := cachedTestSession(t, func() (any, bool) { return map[string]any{}, false })
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				s.status(context.Background())
			}
		}
	}()
	defer func() { close(done); wg.Wait() }()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("class Example { int v%d; }", i)), 0600); err != nil {
			t.Fatal(err)
		}
		s.watcher.mark(path)
		if result := s.Query(context.Background(), Request{Command: "prepare"}); result.Status != "ok" {
			t.Fatal(result)
		}
	}
	s.stop()
}

func TestDeclaredIgnoredSourceRootsMatchIncrementalScan(t *testing.T) {
	s := newSession(t.TempDir())
	root := filepath.Join(s.root, "reports", "coverage")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Example.java")
	if err := os.WriteFile(path, []byte("class Example {}"), 0600); err != nil {
		t.Fatal(err)
	}
	s.model = &BuildModel{Contexts: []BuildContext{{Roots: []string{root}}}}
	s.startWatcher()
	defer s.stop()
	files, snapshot, build, err := s.scan()
	if err != nil || files[path] == "" {
		t.Fatalf("declared source was excluded: %v", err)
	}
	s.files = files
	_, incremental, incrementalBuild, err := s.scanDirty([]string{path})
	if err != nil || snapshot != incremental || build != incrementalBuild {
		t.Fatal("declared source full/incremental scans differ")
	}
	s.watcher.mark(path)
	if !s.watcher.pending() {
		t.Fatal("declared source event was excluded")
	}
}

func TestExcludedPathsMatchFullAndIncrementalScans(t *testing.T) {
	s := newSession(t.TempDir())
	extra := t.TempDir()
	s.extraRoots = []string{extra, filepath.Join(s.root, "reports")}
	source := filepath.Join(s.root, "reports", "coverage")
	s.model = &BuildModel{Contexts: []BuildContext{{Roots: []string{source}}}}
	paths := []string{
		filepath.Join(s.root, "reports", "Unrelated.java"),
		filepath.Join(s.root, "reports", "other", "build.gradle"),
		filepath.Join(s.root, "reports", "coverage-sibling", "Other.java"),
		filepath.Join(s.root, ".git", "Hidden.java"),
		filepath.Join(extra, "coverage", "External.java"),
		filepath.Join(t.TempDir(), "Outside.java"),
		filepath.Join(source, "Example.java"),
		filepath.Join(source, "reports", "Nested.java"),
		filepath.Join(extra, "Visible.java"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, snapshot, build, err := s.scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("unexpected full scan files: %v", files)
	}
	s.files = files
	for _, action := range []string{"unchanged", "modified", "deleted", "created"} {
		t.Run(action, func(t *testing.T) {
			for _, path := range paths {
				if action == "deleted" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if action != "unchanged" {
					if err := os.WriteFile(path, []byte(action), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			next, incremental, incrementalBuild, err := s.scanDirty(paths)
			if err != nil {
				t.Fatal(err)
			}
			full, fullSnapshot, fullBuild, err := s.scan()
			if err != nil || filesChanged(next, full) || incremental != fullSnapshot || incrementalBuild != fullBuild {
				t.Fatalf("full/incremental mismatch: %v, %v, %v", next, full, err)
			}
			if action == "unchanged" && (incremental != snapshot || incrementalBuild != build) {
				t.Fatal("excluded files invalidated the snapshot")
			}
			s.files = next
		})
	}
}

func TestGradleReportsProjectsMatchFullAndIncrementalScans(t *testing.T) {
	for _, structure := range []string{"root", "subproject"} {
		t.Run(structure, func(t *testing.T) {
			workspace := t.TempDir()
			project := filepath.Join(workspace, "reports")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			if structure == "root" {
				workspace = project
			}
			build := filepath.Join(project, "build.gradle")
			if err := os.WriteFile(build, []byte("dependencies { implementation files('a.jar') }"), 0600); err != nil {
				t.Fatal(err)
			}
			s := newSession(workspace)
			if structure == "subproject" {
				s.model = &BuildModel{ProjectRoots: []string{workspace, project}}
			}
			output := filepath.Join(project, "reports", "Output.java")
			if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(output, []byte("unrelated"), 0600); err != nil {
				t.Fatal(err)
			}
			files, before, beforeBuild, err := s.scan()
			if err != nil || files[build] == "" || files[output] != "" {
				t.Fatalf("project build excluded or output included: %v, %v", files, err)
			}
			s.files = files
			s.startWatcher()
			defer s.stop()
			if err := os.WriteFile(build, []byte("dependencies { implementation files('b.jar') }"), 0600); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for !s.watcher.pending() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !s.watcher.pending() {
				t.Fatal("project build change was not watched")
			}
			next, incremental, incrementalBuild, err := s.scanDirty([]string{build, output})
			if err != nil {
				t.Fatal(err)
			}
			full, snapshot, buildHash, err := s.scan()
			if err != nil || filesChanged(next, full) || incremental != snapshot || incrementalBuild != buildHash || snapshot == before || buildHash == beforeBuild {
				t.Fatalf("build change/full/incremental mismatch: %v", err)
			}
		})
	}
}

func TestExcludedDirtyFileDoesNotSyncOrInvalidateSnapshot(t *testing.T) {
	syncs := 0
	s, _ := cachedTestSession(t, func() (any, bool) {
		syncs++
		return map[string]any{}, false
	})
	source := filepath.Join(s.root, "reports", "coverage")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	excluded := filepath.Join(s.root, "reports", "Unrelated.java")
	if err := os.WriteFile(excluded, []byte("class Unrelated {}"), 0600); err != nil {
		t.Fatal(err)
	}
	s.model = &BuildModel{Contexts: []BuildContext{{Roots: []string{source}}}}
	s.startWatcher()
	before := s.Query(context.Background(), Request{Command: "prepare"})
	if before.Status != "ok" {
		t.Fatal(before)
	}
	previousSyncs := syncs
	if err := os.WriteFile(excluded, []byte("class Unrelated { int value; }"), 0600); err != nil {
		t.Fatal(err)
	}
	// Defense in depth: even an unexpected event must obey full-scan rules.
	s.watcher.mark(excluded)
	after := s.Query(context.Background(), Request{Command: "prepare"})
	if after.Status != "ok" || after.Snapshot != before.Snapshot || syncs != previousSyncs {
		t.Fatalf("excluded event changed cached results: %#v (syncs %d -> %d)", after, previousSyncs, syncs)
	}
}

func TestArtifactFingerprintDetectsRestoredMtimeRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.jar")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	model := &BuildModel{Contexts: []BuildContext{{Artifacts: []Artifact{{Path: path}}}}}
	before, err := artifactFingerprint(model)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := modelSnapshot(model)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := artifactFingerprint(model)
	newHash, _ := modelSnapshot(model)
	if err != nil || before == after || hash == newHash {
		t.Fatalf("external rewrite was not detected: %v", err)
	}
}

func TestQueryDoesNotCommitBuildChangedDuringSync(t *testing.T) {
	t.Setenv("JMAN_CACHE_HOME", t.TempDir())
	t.Setenv("JMAN_GRADLE_MODEL", "unused-by-fake-wrapper")
	t.Setenv("JMAN_JDTLS_HOME", "")
	t.Setenv("JMAN_JDTLS", filepath.Join(t.TempDir(), "no-jdtls"))
	var root string
	s, path := cachedTestSession(t, func() (any, bool) {
		if err := os.WriteFile(filepath.Join(root, "settings.gradle"), []byte("// new build"), 0600); err != nil {
			t.Error(err)
		}
		return map[string]any{}, false
	})
	root = s.root
	if err := os.WriteFile(filepath.Join(root, "gradlew"), []byte("#!/bin/sh\necho '{\"contexts\":[{\"project\":\":reloaded\"}]}' > \"$JMAN_MODEL_PATH\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.watcher.mark(path)
	s.syncValid = false
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := s.Query(ctx, Request{Command: "prepare"})
	if s.syncValid || s.model == nil || len(s.model.Contexts) != 1 || s.model.Contexts[0].Project != ":reloaded" {
		t.Fatalf("changed build committed with old session: %#v", result)
	}
}
