package jman

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheDir(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdg := filepath.Join(root, "xdg")
	override := filepath.Join(root, "override")
	for _, tc := range []struct {
		name, home, xdg, override, want string
	}{
		{"default", home, "", "", filepath.Join(home, ".cache", "jman")},
		{"xdg", home, xdg, "", filepath.Join(xdg, "jman")},
		{"override", home, xdg, override, override},
		{"xdg without home", "", xdg, "", filepath.Join(xdg, "jman")},
		{"override without home", "", "", override, override},
		{"temporary fallback", "", "", "", filepath.Join(os.TempDir(), "jman")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", tc.home)
			t.Setenv("XDG_CACHE_HOME", tc.xdg)
			t.Setenv("JMAN_CACHE_HOME", tc.override)
			if got := CacheDir(); got != tc.want {
				t.Fatalf("CacheDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSocketPath(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdg := filepath.Join(root, "xdg")
	cache := filepath.Join(root, "cache")
	runtime := filepath.Join(root, "runtime")
	socket := filepath.Join(root, "explicit.sock")
	t.Setenv("HOME", home)
	for _, tc := range []struct {
		name, xdg, cache, runtime, socket, want string
	}{
		{"default", "", "", "", "", filepath.Join(home, ".cache", "jman", "run", "jman.sock")},
		{"xdg cache", xdg, "", "", "", filepath.Join(xdg, "jman", "run", "jman.sock")},
		{"cache override", xdg, cache, "", "", filepath.Join(cache, "run", "jman.sock")},
		{"runtime", xdg, cache, runtime, "", filepath.Join(runtime, "jman.sock")},
		{"socket override", xdg, cache, runtime, socket, socket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", tc.xdg)
			t.Setenv("JMAN_CACHE_HOME", tc.cache)
			t.Setenv("XDG_RUNTIME_DIR", tc.runtime)
			t.Setenv("JMAN_SOCKET", tc.socket)
			if got := SocketPath(); got != tc.want {
				t.Fatalf("SocketPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
