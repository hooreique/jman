package jman

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
