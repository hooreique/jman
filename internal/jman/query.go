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
	"time"
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
	started := time.Now()
	r := s.query(ctx, q, 0)
	if q.Timings {
		if r.Timings == nil {
			r.Timings = map[string]int64{}
		}
		r.Timings["total"] = time.Since(started).Milliseconds()
	}
	return r
}

func (s *Session) query(ctx context.Context, q Request, retries int) Response {
	r := reply(q)
	timings := map[string]int64{}
	mark := func(name string, started time.Time) { timings[name] = time.Since(started).Milliseconds() }
	attachTimings := func() {
		if q.Timings {
			r.Timings = timings
		}
	}
	started := time.Now()
	// Consuming watcher events is transactional: no unsuccessful scan/import/sync
	// may leave the next request trusting the previous cached snapshot.
	cacheCommitted := false
	defer func() {
		if !cacheCommitted {
			s.mu.Lock()
			s.syncValid = false
			s.lastCacheHit = false
			s.mu.Unlock()
			if s.watcher != nil {
				s.watcher.markAll()
			}
		}
	}()
	fullScan := s.watcher == nil || s.lastSnapshot == "" || q.Command == "refresh" || time.Since(s.lastScan) >= 5*time.Minute
	watchAll := false
	var dirtyPaths []string
	if s.watcher != nil {
		watchAll, dirtyPaths = s.watcher.take()
		if watchAll || len(dirtyPaths) > 0 {
			// Editors and generators commonly emit several filesystem events for
			// one save. Wait only after observing activity, then merge the burst.
			select {
			case <-ctx.Done():
				return failure(q, "not-ready", "SYNC_TIMEOUT", ctx.Err(), "retry the query")
			case <-time.After(75 * time.Millisecond):
			}
			nextAll, nextPaths := s.watcher.take()
			watchAll = watchAll || nextAll
			dirtyPaths = append(dirtyPaths, nextPaths...)
		}
		if watchAll {
			fullScan = true
		}
		if fingerprint, err := workspaceFingerprint(s.root); err != nil || fingerprint != s.watchHash {
			fullScan = true
		}
	}
	var files map[string]string
	var snapshot, build string
	var e error
	if fullScan {
		s.startWatcher()
		files, snapshot, build, e = s.scan()
		mark("scan", started)
		if e != nil {
			if s.watcher != nil {
				s.watcher.restore(watchAll, dirtyPaths)
			}
			return failure(q, "error", "SCAN_FAILED", e, "")
		}
	} else if len(dirtyPaths) > 0 {
		dirtyStarted := time.Now()
		files, snapshot, build, e = s.scanDirty(dirtyPaths)
		mark("dirtyRead", dirtyStarted)
		if e != nil {
			s.watcher.restore(watchAll, dirtyPaths)
			return failure(q, "error", "SCAN_FAILED", e, "")
		}
	} else {
		files, snapshot, build = s.files, s.lastSnapshot, s.buildHash
		timings["scan"] = 0
	}
	changeStarted := time.Now()
	changed := filesChanged(s.files, files)
	mark("changeDetect", changeStarted)
	if q.Command == "refresh" || s.buildHash != "" && s.buildHash != build {
		s.stop()
		s.buildHash = ""
	}
	modelReloaded := false
	if s.buildHash == "" {
		modelStarted := time.Now()
		s.model, s.modelError = loadModel(ctx, s.root)
		mark("modelLoad", modelStarted)
		if ctx.Err() != nil {
			return failure(q, "not-ready", "MODEL_TIMEOUT", ctx.Err(), "retry the query")
		}
		s.buildHash = build
		modelReloaded = true
	}
	artifactStarted := time.Now()
	modelHash := s.artifactHash
	artifactStamp, stampErr := artifactFingerprint(s.model)
	if stampErr != nil {
		return failure(q, "partial", "ARTIFACT_CHANGED", stampErr, "jman refresh")
	}
	if modelReloaded || fullScan || artifactStamp != s.artifactStamp {
		modelHash, e = modelSnapshot(s.model)
		if e != nil {
			return failure(q, "partial", "ARTIFACT_CHANGED", e, "jman refresh")
		}
		if s.artifactHash != "" && s.artifactHash != modelHash {
			s.stop()
		}
		s.artifactHash = modelHash
		s.artifactStamp = artifactStamp
	}
	mark("artifactValidation", artifactStarted)
	startStarted := time.Now()
	if e = s.start(ctx); e != nil {
		return failure(q, "not-ready", "START_FAILED", e, "jman doctor --project "+s.root)
	}
	mark("serverStart", startStarted)
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
	if s.resumed && !s.syncValid {
		// Restored composite roots were unavailable to the initial scan. Include
		// them before computing a delta so they are not falsely reported deleted.
		resumeStarted := time.Now()
		files, snapshot, build, e = s.scan()
		mark("resumeScan", resumeStarted)
		if e != nil {
			return failure(q, "error", "SCAN_FAILED", e, "")
		}
		if build != s.buildHash {
			s.stop()
			s.buildHash = ""
			if retries >= 2 {
				return failure(q, "not-ready", "WORKSPACE_CHANGED", fmt.Errorf("build inputs changed during resume"), "retry after the build settles")
			}
			cacheCommitted = true
			return s.query(ctx, q, retries+1)
		}
		changed = filesChanged(s.files, files)
	}
	if q.Command == "refresh" {
		// An explicit refresh must not take the unchanged-checkpoint shortcut.
		s.resumed = false
	}
	needsSync := q.Command == "refresh" || !s.syncValid || changed
	s.mu.Lock()
	s.lastCacheHit = !needsSync
	s.mu.Unlock()
	problems := s.lastProblems
	if needsSync {
		s.mu.Lock()
		s.syncValid = false
		s.mu.Unlock()
		syncStarted := time.Now()
		problems, e = s.sync(ctx, files)
		mark("sync", syncStarted)
		for name, elapsed := range s.lastSyncTimings {
			timings[name] = elapsed
		}
		if e != nil {
			return failure(q, "not-ready", "SYNC_FAILED", e, "jman doctor")
		}
		// Watch discovered roots before scanning them, so changes during the scan
		// remain pending instead of disappearing in the watcher replacement gap.
		watcherStarted := time.Now()
		s.startWatcher()
		mark("watcherUpdate", watcherStarted)
		// Import can discover additional composite-build roots and generated sources.
		postSyncScanStarted := time.Now()
		postFiles, postSnapshot, postBuild, scanErr := s.scan()
		mark("postSyncScan", postSyncScanStarted)
		if scanErr != nil {
			return failure(q, "error", "SCAN_FAILED", scanErr, "")
		}
		postStamp, stampErr := artifactFingerprint(s.model)
		if postBuild != build || stampErr != nil || postStamp != artifactStamp {
			// Never commit a new build hash against the old model/session.
			s.stop()
			s.buildHash = ""
			if retries >= 2 {
				return failure(q, "not-ready", "WORKSPACE_CHANGED", fmt.Errorf("build inputs changed repeatedly during sync"), "retry after the build settles")
			}
			cacheCommitted = true // The retry owns invalidation from this point.
			return s.query(ctx, q, retries+1)
		}
		if filesChanged(s.files, postFiles) {
			problems, e = s.sync(ctx, postFiles)
			if e != nil {
				return failure(q, "not-ready", "SYNC_FAILED", e, "jman doctor")
			}
			// Recheck after the additional sync as well.
			_, latestSnapshot, latestBuild, err := s.scan()
			latestStamp, stampErr := artifactFingerprint(s.model)
			if err != nil || stampErr != nil || latestBuild != build || latestStamp != artifactStamp || latestSnapshot != postSnapshot {
				s.stop()
				s.buildHash = ""
				if retries >= 2 {
					return failure(q, "not-ready", "WORKSPACE_CHANGED", fmt.Errorf("workspace changed repeatedly during sync"), "retry after the build settles")
				}
				cacheCommitted = true
				return s.query(ctx, q, retries+1)
			}
		}
		s.files = postFiles
		s.lastSnapshot = postSnapshot
		s.buildHash = postBuild
		snapshot = postSnapshot
		s.mu.Lock()
		s.lastScan = time.Now()
		s.lastSync = time.Now()
		s.syncValid = true
		s.mu.Unlock()
	} else {
		timings["sync"] = 0
		if fullScan {
			s.mu.Lock()
			s.lastScan = time.Now()
			s.mu.Unlock()
		}
	}
	cacheCommitted = true
	r.Snapshot = digest([]byte(snapshot + modelHash))
	r.Context = map[string]any{"root": s.root, "semantics": "compile-time", "resumed": s.resumed}
	r.Coverage = map[string]any{"scope": "imported-workspace", "root": s.root, "complete": len(problems) == 0 && s.modelError == nil, "projectErrors": problems, "dynamicReferences": false}
	if s.modelError != nil {
		r.Warnings = append(r.Warnings, s.modelError.Error())
		r.Status = "partial"
	}
	if q.Command == "prepare" || q.Command == "refresh" || q.Command == "doctor" {
		if len(problems) > 0 {
			r.Status = "partial"
			r.Warnings = append(r.Warnings, "Workspace errors are listed below; inspect doctor output.")
		}
		problemDetail := problems
		if q.Command == "doctor" {
			problemDetail = describeProblems(s.model, problems)
		}
		r.Results = append(r.Results, Result{Name: "workspace", Detail: map[string]any{"service": s.status(ctx), "settings": s.settings, "model": s.model, "problems": problemDetail}})
		attachTimings()
		return r
	}
	if q.Command == "deps" {
		c := s.model.context(q.File)
		diagnostics := classifyProblems(s.model, c, problems, q.StrictWorkspace)
		r.Coverage = map[string]any{"scope": "imported-workspace", "root": s.root, "complete": diagnostics.complete() && s.modelError == nil, "projectErrors": problems, "diagnostics": diagnostics, "dynamicReferences": false}
		if len(diagnostics.Critical) > 0 {
			r.Status = "partial"
		} else if len(diagnostics.Advisory)+len(diagnostics.Unknown) > 0 {
			r.Warnings = append(r.Warnings, diagnostics.warning())
		}
		if c == nil {
			r.Status = "not-found"
		} else {
			r.Results = append(r.Results, Result{Name: c.Project + " / " + c.SourceSet, Detail: c})
		}
		attachTimings()
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
	documentStarted := time.Now()
	if e = s.open(q.File, string(data)); e != nil {
		return failure(q, "error", "DOCUMENT_FAILED", e, "")
	}
	mark("document", documentStarted)
	// The command runs behind JDTLS's document lifecycle queue; selection is from the current working copy.
	var p provenance
	resolveStarted := time.Now()
	if e = s.execute(ctx, "jman.resolve", []any{FileURI(q.File), q.Line - 1, column}, &p); e != nil {
		return failure(q, "error", "BINDING_FAILED", e, "jman doctor")
	}
	mark("bindingResolve", resolveStarted)
	bc := s.model.context(q.File)
	r.Context = map[string]any{"root": s.root, "project": p.Project, "sourceRoot": p.SourceRoot, "build": bc, "semantics": "compile-time", "bindingResolved": p.Resolved}
	// Keep default JSON context small as well: dependencies are available through `deps`.
	if bc != nil {
		r.Context = map[string]any{"root": s.root, "project": bc.Project, "sourceSet": bc.SourceSet, "configuration": bc.Configuration, "bindingResolved": p.Resolved, "semantics": "compile-time"}
	}
	diagnostics := classifyProblems(s.model, bc, problems, q.StrictWorkspace)
	r.Coverage = map[string]any{"scope": "imported-workspace", "root": s.root, "complete": diagnostics.complete() && s.modelError == nil, "projectErrors": problems, "diagnostics": diagnostics, "dynamicReferences": false}
	if len(diagnostics.Critical) > 0 {
		r.Status = "partial"
		r.Warnings = append(r.Warnings, "Errors in the caller's source set or compile dependencies may make bindings or references incomplete; inspect doctor output.")
	} else if len(diagnostics.Advisory)+len(diagnostics.Unknown) > 0 {
		r.Warnings = append(r.Warnings, diagnostics.warning())
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
		lspStarted := time.Now()
		if e = s.rpc.Call(ctx, "textDocument/hover", params, &hover); e != nil {
			return failure(q, "error", "LSP_FAILED", e, "")
		}
		mark("lsp", lspStarted)
		text := hoverText(hover.Contents)
		if len(text) > q.MaxBytes {
			r.Warnings = append(r.Warnings, "Hover documentation truncated by --max-bytes.")
		}
		r.Results = append(r.Results, Result{Name: p.Name, Snippet: clip(text, q.MaxBytes) + "\n", Detail: map[string]any{"range": hover.Range}})
		r = s.finish(q, r, snapshot, modelHash)
		attachTimings()
		return r
	}
	method := map[string]string{"definition": "textDocument/definition", "references": "textDocument/references", "implementations": "textDocument/implementation"}[q.Command]
	if method == "" {
		return failure(q, "error", "UNKNOWN_COMMAND", fmt.Errorf("unknown command %s", q.Command), "")
	}
	if q.Command == "references" {
		params["context"] = map[string]any{"includeDeclaration": false}
	}
	var raw json.RawMessage
	lspStarted := time.Now()
	if e = s.rpc.Call(ctx, method, params, &raw); e != nil {
		return failure(q, "error", "LSP_FAILED", e, "")
	}
	mark("lsp", lspStarted)
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
	renderStarted := time.Now()
	r = s.finish(q, r, snapshot, modelHash)
	mark("render", renderStarted)
	attachTimings()
	return r
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
	if s.watcher != nil {
		dirtyAll, paths := s.watcher.take()
		stamp, err := artifactFingerprint(s.model)
		if dirtyAll || len(paths) > 0 || err != nil || stamp != s.artifactStamp {
			s.watcher.restore(dirtyAll, paths)
			r.Status = "partial"
			r.NextCursor = ""
			r.Warnings = append(r.Warnings, "Workspace changed during query; repeat before relying on these results.")
		}
		return r
	}
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
