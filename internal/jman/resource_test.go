package jman

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestJDTLSHeapValidation(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int
		invalid     bool
	}{
		{"omitted", `{}`, 1536, false},
		{"zero", `{"jdtlsMaxHeapMiB":0}`, 0, true},
		{"negative", `{"jdtlsMaxHeapMiB":-1}`, 0, true},
		{"below initial heap", `{"jdtlsMaxHeapMiB":127}`, 0, true},
		{"minimum", `{"jdtlsMaxHeapMiB":128}`, 128, false},
		{"custom", `{"jdtlsMaxHeapMiB":2048}`, 2048, false},
		{"fraction", `{"jdtlsMaxHeapMiB":128.5}`, 0, true},
		{"string", `{"jdtlsMaxHeapMiB":"2048"}`, 0, true},
		{"overflow", `{"jdtlsMaxHeapMiB":999999999999999999999999}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c Config
			err := json.Unmarshal([]byte(tc.input), &c)
			got := 0
			if err == nil {
				got, err = c.maxHeapMiB()
			}
			if tc.invalid {
				if err == nil {
					t.Fatalf("accepted invalid heap %s", tc.input)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got heap %d, error %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestIdleEvictionBoundaryAndBusyProtection(t *testing.T) {
	now := time.Now()
	expired := newSession("expired")
	expired.lastUsed = now.Add(-time.Minute)
	recent := newSession("recent")
	recent.lastUsed = now.Add(-time.Minute + time.Nanosecond)
	busy := newSession("busy")
	busy.lastUsed = now.Add(-2 * time.Minute)
	if err := busy.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer busy.release()
	m := &manager{
		sessions: map[string]*Session{"expired": expired, "recent": recent, "busy": busy},
		max:      3, idleTimeout: time.Minute,
	}
	m.evictIdle(now)
	if m.sessions["expired"] != nil || m.sessions["recent"] == nil || m.sessions["busy"] == nil {
		t.Fatalf("wrong sessions after eviction: %v", m.sessions)
	}
	m.evictIdle(now.Add(time.Nanosecond))
	if len(m.sessions) != 1 || m.sessions["busy"] == nil {
		t.Fatalf("expiry boundary failed or busy session removed: %v", m.sessions)
	}
}

func TestDaemonRejectsInvalidLimitsBeforeStartup(t *testing.T) {
	for _, tc := range []struct {
		max  int
		idle time.Duration
	}{
		{0, time.Minute}, {-1, time.Minute}, {1, 0}, {1, -time.Second}, {1, time.Second - time.Nanosecond},
	} {
		if err := RunDaemon(tc.max, tc.idle); err == nil {
			t.Fatalf("accepted max=%d, idle=%s", tc.max, tc.idle)
		}
	}
}
