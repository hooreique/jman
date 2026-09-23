package jman

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Request struct {
	Command    string    `json:"command"`
	Project    string    `json:"project"`
	File       string    `json:"file,omitempty"`
	Line       int       `json:"line,omitempty"`
	Column     int       `json:"column,omitempty"`
	Symbol     string    `json:"symbol,omitempty"`
	Occurrence int       `json:"occurrence,omitempty"`
	Limit      int       `json:"limit"`
	MaxBytes   int       `json:"maxBytes"`
	Cursor     string    `json:"cursor,omitempty"`
	Deadline   time.Time `json:"deadline"`
}
type Problem struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	NextAction string `json:"nextAction,omitempty"`
}
type Response struct {
	SchemaVersion int      `json:"schemaVersion"`
	Status        string   `json:"status"`
	Query         Request  `json:"query"`
	Context       any      `json:"context,omitempty"`
	Snapshot      string   `json:"snapshot,omitempty"`
	Results       []Result `json:"results"`
	Coverage      any      `json:"coverage,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	NextCursor    string   `json:"nextCursor,omitempty"`
	Error         *Problem `json:"error,omitempty"`
}
type Result struct {
	Name         string `json:"name,omitempty"`
	Path         string `json:"path,omitempty"`
	URI          string `json:"uri,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
	EndLine      int    `json:"endLine,omitempty"`
	Origin       string `json:"origin,omitempty"`
	SourceMatch  string `json:"sourceMatch,omitempty"`
	Artifact     string `json:"artifact,omitempty"`
	Binary       string `json:"binary,omitempty"`
	BinaryDigest string `json:"binaryDigest,omitempty"`
	Snippet      string `json:"snippet,omitempty"`
	Detail       any    `json:"detail,omitempty"`
}

func reply(q Request) Response {
	return Response{SchemaVersion: 1, Status: "ok", Query: q, Results: []Result{}}
}
func failure(q Request, status, code string, e error, next string) Response {
	r := reply(q)
	r.Status = status
	r.Error = &Problem{code, e.Error(), status == "not-ready", next}
	return r
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func fileDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func CacheDir() string {
	if s := os.Getenv("JMAN_CACHE_HOME"); s != "" {
		return s
	}
	p, e := os.UserCacheDir()
	if e != nil {
		p = os.TempDir()
	}
	return filepath.Join(p, "jman")
}
func SocketPath() string {
	if s := os.Getenv("JMAN_SOCKET"); s != "" {
		return s
	}
	if p := os.Getenv("XDG_RUNTIME_DIR"); p != "" {
		return filepath.Join(p, "jman.sock")
	}
	return filepath.Join(CacheDir(), "run", "jman.sock")
}
