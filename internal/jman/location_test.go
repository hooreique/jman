package jman

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnicodeAndAmbiguity(t *testing.T) {
	text := "// 😀 한글\nString x = f() + f();"
	got, e := Position(text, 1, 4, "", 0)
	if e != nil || got != 3 {
		t.Fatalf("%d %v", got, e)
	}
	got, e = Position(text, 1, 5, "", 0)
	if e != nil || got != 5 {
		t.Fatalf("surrogate conversion %d %v", got, e)
	}
	if _, e = Position(text, 2, 0, "f", 0); e == nil {
		t.Fatal("ambiguous token accepted")
	}
	got, e = Position(text, 2, 0, "f", 2)
	if e != nil || got != 17 {
		t.Fatalf("second token %d %v", got, e)
	}
	if _, e = Position("foo foobar", 1, 0, "foo", 0); e != nil {
		t.Fatal(e)
	}
}
func TestProjectBoundary(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "settings.gradle")
	if e := os.WriteFile(outer, nil, 0600); e != nil {
		t.Fatal(e)
	}
	nested := filepath.Join(root, "other")
	if e := os.MkdirAll(filepath.Join(nested, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := FindProject(nested); e == nil {
		t.Fatal("discovery crossed repository boundary")
	}
	if e := os.WriteFile(filepath.Join(nested, "build.gradle"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	found, e := FindProject(nested)
	if e != nil || found != nested {
		t.Fatalf("%s %v", found, e)
	}
}
func TestSnapshotUsesContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "A.java")
	_ = os.WriteFile(path, []byte("class A {}"), 0600)
	_, a, build, e := scan(root)
	if e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(path)
	_ = os.WriteFile(path, []byte("class B {}"), 0600)
	_ = os.Chtimes(path, info.ModTime(), info.ModTime())
	_, b, next, e := scan(root)
	if e != nil {
		t.Fatal(e)
	}
	if a == b {
		t.Fatal("same-size rewrite not detected")
	}
	if build != next {
		t.Fatal("source edit invalidated build model")
	}
}
