package jman

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// A checkpoint is only published after successful sync and clean JDTLS exit.
// Its directory already identifies the project, tooling, build and artifacts.
// It is consumed before launching JDTLS so crashes cannot leave a trusted copy.
type workspaceCheckpoint struct {
	Version    int               `json:"version"`
	Root       string            `json:"root"`
	Files      map[string]string `json:"files"`
	ExtraRoots []string          `json:"extraRoots"`
}

func (s *Session) restoreCheckpoint() {
	s.resumed = false
	path := filepath.Join(s.workspaceDir, "checkpoint.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Failure to consume the marker must never enable the resume fast path.
	if os.Remove(path) != nil {
		return
	}
	var saved workspaceCheckpoint
	if json.Unmarshal(b, &saved) != nil || saved.Version != 1 || saved.Root != s.root || saved.Files == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(s.workspaceDir, "data", ".metadata")); err != nil {
		return
	}
	s.files = saved.Files
	s.extraRoots = saved.ExtraRoots
	s.resumed = true
	// Never restore syncValid: the new server must finish import and the
	// build/index readiness barrier before any query can use its results.
}

func (s *Session) saveCheckpoint() {
	if s.workspaceDir == "" || s.lastSnapshot == "" {
		return
	}
	b, err := json.Marshal(workspaceCheckpoint{Version: 1, Root: s.root, Files: s.files, ExtraRoots: s.extraRoots})
	if err != nil {
		return
	}
	f, err := os.CreateTemp(s.workspaceDir, ".checkpoint-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_ = os.Rename(f.Name(), filepath.Join(s.workspaceDir, "checkpoint.json"))
	}
}
