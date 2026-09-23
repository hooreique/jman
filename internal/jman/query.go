package jman

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

type point struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type span struct {
	Start point `json:"start"`
	End   point `json:"end"`
}
type location struct {
	URI                  string `json:"uri"`
	Range                span   `json:"range"`
	TargetURI            string `json:"targetUri"`
	TargetRange          span   `json:"targetRange"`
	TargetSelectionRange span   `json:"targetSelectionRange"`
}
type provenance struct {
	Resolved                bool   `json:"resolved"`
	Name                    string `json:"name"`
	Project                 string `json:"project"`
	SourceRoot              string `json:"sourceRoot"`
	Binary                  string `json:"binary"`
	SourceAttachment        string `json:"sourceAttachment"`
	BinaryMember            bool   `json:"binaryMember"`
	HasSourceRange          bool   `json:"hasSourceRange"`
	AttachedSourceAvailable bool   `json:"attachedSourceAvailable"`
	GeneratedMember         bool   `json:"generatedMember"`
	Member                  bool   `json:"member"`
	Handle                  string `json:"handle"`
}

func (s *Session) Query(ctx context.Context, q Request) Response {
	r := reply(q)
	files, snapshot, build, e := s.scan()
	if e != nil {
		return failure(q, "error", "SCAN_FAILED", e, "")
	}
	if q.Command == "refresh" || s.buildHash != "" && s.buildHash != build {
		s.stop()
		s.buildHash = ""
	}
	if s.buildHash == "" {
		s.model, s.modelError = loadModel(ctx, s.root)
		s.buildHash = build
	}
	modelHash, e := modelSnapshot(s.model)
	if e != nil {
		return failure(q, "partial", "ARTIFACT_CHANGED", e, "jman refresh")
	}
	if s.artifactHash != "" && s.artifactHash != modelHash {
		s.stop()
	}
	s.artifactHash = modelHash
	if e = s.start(ctx); e != nil {
		return failure(q, "not-ready", "START_FAILED", e, "jman doctor --project "+s.root)
	}
	select {
	case <-s.ready:
	case <-ctx.Done():
		return failure(q, "not-ready", "IMPORT_TIMEOUT", ctx.Err(), "jman status")
	case <-s.rpc.Done():
		return failure(q, "error", "SERVER_EXITED", fmt.Errorf("JDTLS exited"), "jman refresh")
	}
	s.mu.Lock()
	initError := s.initialError
	s.mu.Unlock()
	if initError != "" {
		return failure(q, "not-ready", "IMPORT_FAILED", fmt.Errorf("%s", initError), "jman refresh")
	}
	problems, e := s.sync(ctx, files)
	if e != nil {
		return failure(q, "not-ready", "SYNC_FAILED", e, "jman doctor")
	}
	// Import can discover additional composite-build roots and generated sources.
	_, snapshot, _, e = s.scan()
	if e != nil {
		return failure(q, "error", "SCAN_FAILED", e, "")
	}
	r.Snapshot = digest([]byte(snapshot + modelHash))
	r.Context = map[string]any{"root": s.root, "semantics": "compile-time"}
	r.Coverage = map[string]any{"scope": "imported-workspace", "root": s.root, "complete": len(problems) == 0 && s.modelError == nil, "projectErrors": problems, "dynamicReferences": false}
	if s.modelError != nil {
		r.Warnings = append(r.Warnings, s.modelError.Error())
		r.Status = "partial"
	}
	if len(problems) > 0 {
		r.Warnings = append(r.Warnings, "Project errors may make bindings or references incomplete; inspect doctor output.")
		r.Status = "partial"
	}
	if q.Command == "prepare" || q.Command == "refresh" || q.Command == "doctor" {
		r.Results = append(r.Results, Result{Name: "workspace", Detail: map[string]any{"service": s.status(), "settings": s.settings, "model": s.model, "problems": problems}})
		return r
	}
	if q.Command == "deps" {
		c := s.model.context(q.File)
		if c == nil {
			r.Status = "not-found"
		} else {
			r.Results = append(r.Results, Result{Name: c.Project + " / " + c.SourceSet, Detail: c})
		}
		return r
	}
	data, e := os.ReadFile(q.File)
	if e != nil {
		return failure(q, "error", "READ_FAILED", e, "")
	}
	column, e := Position(string(data), q.Line, q.Column, q.Symbol, q.Occurrence)
	if e != nil {
		return failure(q, "ambiguous", "INVALID_LOCATION", e, "")
	}
	if e = s.open(q.File, string(data)); e != nil {
		return failure(q, "error", "DOCUMENT_FAILED", e, "")
	}
	// The command runs behind JDTLS's document lifecycle queue; selection is from the current working copy.
	var p provenance
	if e = s.execute(ctx, "jman.resolve", []any{FileURI(q.File), q.Line - 1, column}, &p); e != nil {
		return failure(q, "error", "BINDING_FAILED", e, "jman doctor")
	}
	bc := s.model.context(q.File)
	r.Context = map[string]any{"root": s.root, "project": p.Project, "sourceRoot": p.SourceRoot, "build": bc, "semantics": "compile-time", "bindingResolved": p.Resolved}
	// Keep default JSON context small as well: dependencies are available through `deps`.
	if bc != nil {
		r.Context = map[string]any{"root": s.root, "project": bc.Project, "sourceSet": bc.SourceSet, "configuration": bc.Configuration, "bindingResolved": p.Resolved, "semantics": "compile-time"}
	}
	params := map[string]any{"textDocument": map[string]any{"uri": FileURI(q.File)}, "position": point{q.Line - 1, column}}
	if !p.Resolved {
		r.Status = "partial"
		r.Error = &Problem{Code: "UNRESOLVED_BINDING", Message: "JDT could not uniquely bind this location", Retryable: len(problems) > 0, NextAction: "jman doctor"}
		return r
	}
	if q.Command == "hover" {
		var hover struct {
			Contents json.RawMessage `json:"contents"`
			Range    *span           `json:"range"`
		}
		if e = s.rpc.Call(ctx, "textDocument/hover", params, &hover); e != nil {
			return failure(q, "error", "LSP_FAILED", e, "")
		}
		text := hoverText(hover.Contents)
		if len(text) > q.MaxBytes {
			r.Warnings = append(r.Warnings, "Hover documentation truncated by --max-bytes.")
		}
		r.Results = append(r.Results, Result{Name: p.Name, Snippet: clip(text, q.MaxBytes) + "\n", Detail: map[string]any{"range": hover.Range}})
		return s.finish(q, r, snapshot, modelHash)
	}
	method := map[string]string{"definition": "textDocument/definition", "references": "textDocument/references", "implementations": "textDocument/implementation"}[q.Command]
	if method == "" {
		return failure(q, "error", "UNKNOWN_COMMAND", fmt.Errorf("unknown command %s", q.Command), "")
	}
	if q.Command == "references" {
		params["context"] = map[string]any{"includeDeclaration": false}
	}
	var raw json.RawMessage
	if e = s.rpc.Call(ctx, method, params, &raw); e != nil {
		return failure(q, "error", "LSP_FAILED", e, "")
	}
	var locations []location
	if len(raw) > 0 && raw[0] == '{' {
		var loc location
		e = json.Unmarshal(raw, &loc)
		locations = []location{loc}
	} else {
		e = json.Unmarshal(raw, &locations)
	}
	if e != nil {
		return failure(q, "error", "PROTOCOL_ERROR", e, "")
	}
	for i := range locations {
		if locations[i].TargetURI != "" {
			locations[i].URI = locations[i].TargetURI
			locations[i].Range = locations[i].TargetSelectionRange
		}
	}
	sort.Slice(locations, func(i, j int) bool {
		a, b := locations[i], locations[j]
		if a.URI != b.URI {
			return a.URI < b.URI
		}
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		return a.Range.Start.Character < b.Range.Start.Character
	})
	start := 0
	key := digest([]byte(fmt.Sprintf("%s:%s:%d:%d:%s:%d", q.Command, q.File, q.Line, column, q.Symbol, q.Occurrence)))
	if q.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		parts := strings.Split(string(b), ":")
		if e != nil || len(parts) != 3 || parts[0] != r.Snapshot || parts[1] != key {
			return failure(q, "error", "STALE_CURSOR", fmt.Errorf("cursor does not match query snapshot"), "repeat the query without --cursor")
		}
		start, e = strconv.Atoi(parts[2])
		if e != nil || start < 0 || start > len(locations) {
			return failure(q, "error", "INVALID_CURSOR", fmt.Errorf("invalid cursor offset"), "")
		}
	}
	used := 0
	end := start
	for ; end < len(locations) && end < start+q.Limit; end++ {
		loc := locations[end]
		item := Result{Name: p.Name, URI: loc.URI, Line: loc.Range.Start.Line + 1, Column: loc.Range.Start.Character + 1, EndLine: loc.Range.End.Line + 1}
		var text string
		path := URIPath(loc.URI)
		if path != "" {
			item.Path = path
			item.Origin = "workspace-source"
			b, err := os.ReadFile(path)
			if err != nil {
				r.Warnings = append(r.Warnings, err.Error())
				r.Status = "partial"
			} else {
				text = string(b)
			}
			if strings.Contains(filepath.ToSlash(path), "/generated/") || strings.Contains(filepath.ToSlash(path), "/generated-sources/") {
				item.Origin = "generated-source"
			}
		}
		if path == "" {
			if e = s.rpc.Call(ctx, "java/classFileContents", map[string]any{"uri": loc.URI}, &text); e != nil {
				r.Warnings = append(r.Warnings, e.Error())
				r.Status = "partial"
			}
			item.Origin = "decompiled"
			item.SourceMatch = "missing"
			if q.Command == "definition" && p.AttachedSourceAvailable && p.SourceAttachment != "" {
				if _, e := os.Stat(p.SourceAttachment); e == nil {
					item.Origin = "dependency-source"
					item.SourceMatch = "unverified"
				}
			}
			if text == "" {
				item.Origin = "binary-only"
			} else {
				item.Path, e = cacheSource(loc.URI, text)
				if e != nil {
					return failure(q, "error", "CACHE_FAILED", e, "")
				}
			}
		}
		if q.Command == "definition" {
			if bc != nil && path != "" {
				if target := s.model.context(path); target != nil && target.Project == bc.Project && bc.SourceSet == "main" && target.SourceSet == "test" {
					r.Status = "partial"
					r.Warnings = append(r.Warnings, "JDT selected test source from a main source-set caller; build model disagrees. Do not rely on this definition.")
				}
			}
			item.Binary = p.Binary
			if p.Binary != "" {
				if hash, err := fileDigest(p.Binary); err == nil {
					item.BinaryDigest = hash
				}
				if bc != nil {
					for _, a := range bc.Artifacts {
						if samePath(a.Path, p.Binary) {
							item.Artifact = a.Component
							if a.ModuleComponent && item.Origin == "dependency-source" && filepath.Base(p.SourceAttachment) == strings.TrimSuffix(filepath.Base(p.Binary), ".jar")+"-sources.jar" {
								item.SourceMatch = "coordinate-only"
							}
							break
						}
					}
				}
				if bc != nil && item.Artifact == "" {
					r.Status = "partial"
					r.Warnings = append(r.Warnings, "JDT binary is not in the exported Gradle context; run refresh and inspect deps.")
				}
			}
			if p.GeneratedMember || p.Member && !p.BinaryMember && !p.HasSourceRange {
				item.Origin = "generated-source"
				r.Warnings = append(r.Warnings, "Generated member: location may identify its owning source rather than a generated method body.")
			}
		}
		if text != "" {
			item.Column = publicColumn(text, loc.Range.Start)
			item.Snippet = excerpt(text, item.Line, 20)
		}
		size := len(item.Snippet) + len(item.Path) + len(item.Name) + 200
		if used+size > q.MaxBytes && len(r.Results) > 0 {
			break
		}
		if size > q.MaxBytes {
			item.Snippet = clip(item.Snippet, max(0, q.MaxBytes-len(item.Path)-len(item.Name)-200))
			r.Warnings = append(r.Warnings, "Source excerpt truncated by --max-bytes.")
		}
		used += size
		r.Results = append(r.Results, item)
	}
	if end < len(locations) {
		r.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s:%d", r.Snapshot, key, end)))
	}
	if len(locations) == 0 && r.Status == "ok" {
		r.Status = "not-found"
	}
	if q.Command == "implementations" {
		r.Warnings = append(r.Warnings, "Static implementation candidates do not determine Spring bean or proxy selection.")
	}
	return s.finish(q, r, snapshot, modelHash)
}
func hoverText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	var text func(any) string
	text = func(v any) string {
		switch v := v.(type) {
		case string:
			return v
		case map[string]any:
			if s, ok := v["value"].(string); ok {
				return s
			}
		case []any:
			var parts []string
			for _, p := range v {
				parts = append(parts, text(p))
			}
			return strings.Join(parts, "\n\n")
		}
		return ""
	}
	return text(value)
}
func (s *Session) finish(q Request, r Response, before, modelBefore string) Response {
	_, after, _, e := s.scan()
	modelAfter, me := modelSnapshot(s.model)
	if e != nil || me != nil || before != after || modelBefore != modelAfter {
		r.Status = "partial"
		r.NextCursor = ""
		r.Warnings = append(r.Warnings, "Workspace changed during query; repeat before relying on these results.")
	}
	return r
}
func samePath(a, b string) bool {
	aa, e := filepath.EvalSymlinks(a)
	if e != nil {
		aa = a
	}
	bb, e := filepath.EvalSymlinks(b)
	if e != nil {
		bb = b
	}
	return aa == bb
}
func cacheSource(uri, text string) (string, error) {
	dir := filepath.Join(CacheDir(), "sources", digest([]byte(uri+"\x00"+text)))
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	path := filepath.Join(dir, "Source.java")
	if _, e := os.Stat(path); e == nil {
		return path, nil
	}
	tmp, e := os.CreateTemp(dir, ".source-")
	if e != nil {
		return "", e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.WriteString(text); e != nil {
		tmp.Close()
		return "", e
	}
	if e = tmp.Chmod(0444); e != nil {
		tmp.Close()
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	return path, os.Rename(tmp.Name(), path)
}
func publicColumn(text string, p point) int {
	lines := strings.Split(text, "\n")
	if p.Line < 0 || p.Line >= len(lines) {
		return p.Character + 1
	}
	units := utf16.Encode([]rune(lines[p.Line]))
	if p.Character > len(units) {
		return p.Character + 1
	}
	return len(utf16.Decode(units[:p.Character])) + 1
}
func excerpt(text string, line, count int) string {
	lines := strings.Split(text, "\n")
	start := max(0, line-1)
	end := min(len(lines), start+count)
	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%d %s\n", i+1, lines[i])
	}
	if end < len(lines) {
		b.WriteString("… (use jman read for more)\n")
	}
	return b.String()
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n] + "\n…\n"
}
