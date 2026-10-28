package jman

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckpointRestoresOnlyCleanCompatibleWorkspace(t *testing.T) {
	t.Setenv("JMAN_CACHE_HOME", t.TempDir())
	s := newSession(t.TempDir())
	s.workspaceDir = filepath.Join(CacheDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(s.workspaceDir, "data", ".metadata"), 0700); err != nil {
		t.Fatal(err)
	}
	s.files = map[string]string{filepath.Join(s.root, "Example.java"): "old"}
	s.lastSnapshot = "saved"
	s.extraRoots = []string{t.TempDir()}
	s.saveCheckpoint()
	r := newSession(s.root)
	r.workspaceDir = s.workspaceDir
	r.restoreCheckpoint()
	if !r.resumed || filesChanged(s.files, r.files) || len(r.extraRoots) != 1 || r.syncValid {
		t.Fatalf("invalid restored state: %#v", r)
	}
	r.restoreCheckpoint()
	if r.resumed {
		t.Fatal("a consumed checkpoint survived a simulated crash")
	}
	for _, content := range []string{"broken", `{"version":2,"root":"wrong","files":{}}`} {
		if err := os.WriteFile(filepath.Join(s.workspaceDir, "checkpoint.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		r.restoreCheckpoint()
		if r.resumed {
			t.Fatal("invalid checkpoint was trusted")
		}
	}
	s.saveCheckpoint()
	r.root = "different-root"
	r.restoreCheckpoint()
	if r.resumed {
		t.Fatal("checkpoint for another project was trusted")
	}
}

func TestCheckpointServer(t *testing.T) {
	if os.Getenv("JMAN_TEST_CHECKPOINT_SERVER") != "1" {
		return
	}
	r := bufio.NewReader(os.Stdin)
	for {
		header, err := r.ReadString('\n')
		if err != nil {
			os.Exit(1)
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:")))
		if err != nil {
			os.Exit(1)
		}
		_, _ = r.ReadString('\n')
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			os.Exit(1)
		}
		var q struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(b, &q)
		switch q.Method {
		case "exit":
			os.Exit(0)
		case "shutdown":
			_ = os.WriteFile(os.Getenv("JMAN_TEST_SHUTDOWN_MARKER"), []byte("shutdown"), 0600)
		}
		if len(q.ID) > 0 {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": nil})
			fmt.Printf("Content-Length: %d\r\n\r\n%s", len(b), b)
		}
		if q.Method == "initialize" {
			b := []byte(`{"jsonrpc":"2.0","method":"language/status","params":{"type":"Started"}}`)
			fmt.Printf("Content-Length: %d\r\n\r\n%s", len(b), b)
		}
	}
}

func TestCleanStopPublishesCheckpointAndRestartConsumesIt(t *testing.T) {
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec \"$JMAN_TEST_BINARY\" -test.run=TestCheckpointServer -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JMAN_TEST_BINARY", binary)
	t.Setenv("JMAN_TEST_CHECKPOINT_SERVER", "1")
	t.Setenv("JMAN_TEST_SHUTDOWN_MARKER", filepath.Join(dir, "shutdown"))
	t.Setenv("JMAN_JDTLS", launcher)
	t.Setenv("JMAN_JDTLS_HOME", "")
	t.Setenv("JMAN_EXTENSION", "test")
	t.Setenv("JMAN_LOMBOK_AGENT", "")
	t.Setenv("JMAN_CACHE_HOME", filepath.Join(dir, "cache"))
	s := newSession(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.start(ctx); err != nil {
		t.Fatal(err)
	}
	workspace := s.workspaceDir
	if err := os.MkdirAll(filepath.Join(workspace, "data", ".metadata"), 0700); err != nil {
		t.Fatal(err)
	}
	s.files = map[string]string{"Example.java": "hash"}
	s.lastSnapshot = "snapshot"
	s.syncValid = true
	s.stop()
	if _, err := os.Stat(filepath.Join(dir, "shutdown")); err != nil {
		t.Fatal("shutdown was not sent", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "checkpoint.json")); err != nil {
		t.Fatal("checkpoint was not published", err)
	}
	if err := s.start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.stop()
	if !s.resumed || s.files["Example.java"] != "hash" || s.syncValid {
		t.Fatal("restart did not restore the clean checkpoint")
	}
	if _, err := os.Stat(filepath.Join(workspace, "checkpoint.json")); !os.IsNotExist(err) {
		t.Fatal("checkpoint was not consumed")
	}
	s.stop()
	if _, err := os.Stat(filepath.Join(workspace, "checkpoint.json")); !os.IsNotExist(err) {
		t.Fatal("a resumed server without successful sync published a checkpoint")
	}
}
