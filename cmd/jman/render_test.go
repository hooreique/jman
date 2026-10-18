package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hooreique/jman/internal/jman"
)

// Responses cross a JSON socket boundary before the CLI renders them.
func decoded(t *testing.T, r jman.Response) jman.Response {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out jman.Response
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func compact(t *testing.T, r jman.Response) string {
	t.Helper()
	var out bytes.Buffer
	renderCompact(&out, decoded(t, r))
	if out.Len() > r.Query.MaxBytes {
		t.Fatalf("output exceeds budget: %d > %d", out.Len(), r.Query.MaxBytes)
	}
	if !utf8.Valid(out.Bytes()) {
		t.Fatal("output splits a Unicode code point")
	}
	return out.String()
}

func TestWorkspaceSummariesOmitLargeModel(t *testing.T) {
	model := &jman.BuildModel{}
	for i := 0; i < 91; i++ {
		c := jman.BuildContext{Project: fmt.Sprintf(":p%d", i%35), SourceSet: "main"}
		for a := 0; a < 445; a++ {
			c.Artifacts = append(c.Artifacts, jman.Artifact{Path: fmt.Sprintf("/cache/%d.jar", a), Variant: strings.Repeat("MODEL_SENTINEL", 10)})
		}
		model.Contexts = append(model.Contexts, c)
	}
	problems := []any{map[string]any{"message": "package mismatch", "path": "/app/Test.java"}}
	for _, command := range []string{"prepare", "refresh", "doctor"} {
		t.Run(command, func(t *testing.T) {
			r := jman.Response{Status: "partial", Query: jman.Request{Command: command, Project: "/build", Limit: 20, MaxBytes: 8192}, Results: []jman.Result{{Name: "workspace", Detail: map[string]any{
				"model": model, "problems": problems, "settings": map[string]any{"secret": "SETTINGS_SENTINEL"}, "service": map[string]any{"state": "degraded", "pid": 123},
			}}}}
			out := compact(t, r)
			for _, want := range []string{"35 projects; 91 source sets; 445 unique artifacts", "problems: 1", "state=degraded", "status: partial", "--explain or --json"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in %s", want, out)
				}
			}
			if strings.Contains(out, "SENTINEL") || len(out) > 1024 {
				t.Fatalf("unexpected verbose output: %d bytes", len(out))
			}
			if command == "doctor" && !strings.Contains(out, "/app/Test.java; message=package mismatch") {
				t.Fatal("doctor must show actionable problems")
			}
			if command != "doctor" && !strings.Contains(out, "jman doctor --project /build") {
				t.Fatal("prepare/refresh must point to doctor")
			}
			// Summary rendering never mutates the underlying full JSON response.
			b, _ := json.Marshal(r)
			if !bytes.Contains(b, []byte("MODEL_SENTINEL")) || !bytes.Contains(b, []byte("SETTINGS_SENTINEL")) {
				t.Fatal("full details were lost")
			}
		})
	}
}

func TestDepsSummaryLimitsArtifacts(t *testing.T) {
	c := jman.BuildContext{Project: ":app", SourceSet: "main", Configuration: "compileClasspath", CompileClasspath: []string{"/hidden/path.jar"}}
	for i := 0; i < 200; i++ {
		c.Artifacts = append(c.Artifacts, jman.Artifact{Component: fmt.Sprintf("example:lib%d:1.0", i), Path: "/hidden/path.jar", Variant: "HIDDEN_VARIANT"})
	}
	r := jman.Response{Status: "ok", Query: jman.Request{Command: "deps", Limit: 3, MaxBytes: 8192}, Results: []jman.Result{{Name: ":app / main", Detail: c}}}
	out := compact(t, r)
	for _, want := range []string{"200 artifacts", "example:lib2:1.0", "197 more artifacts", "configuration=compileClasspath"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "example:lib3:") || strings.Contains(out, "/hidden/") || strings.Contains(out, "HIDDEN_VARIANT") {
		t.Fatal("deps dumped full context")
	}
}

func TestDepsLegacyMissingFieldsAreUnknown(t *testing.T) {
	r := jman.Response{Status: "ok", Query: jman.Request{Command: "deps", Limit: 20, MaxBytes: 8192}, Results: []jman.Result{{Name: "main", Detail: map[string]any{"artifacts": []any{}, "roots": []any{}}}}}
	out := compact(t, r)
	if !strings.Contains(out, "unknown classpath entries; 0 roots; unknown outputs") {
		t.Fatal(out)
	}
}

func TestNavigationSummaryKeepsProvenanceAndUncertainty(t *testing.T) {
	for _, command := range []string{"definition", "references", "implementations", "hover"} {
		t.Run(command, func(t *testing.T) {
			r := jman.Response{Status: "partial", Query: jman.Request{Command: command, Limit: 20, MaxBytes: 8192},
				Context:  map[string]any{"root": "/build", "bindingResolved": true, "build": map[string]any{"project": ":app", "compileClasspath": []string{strings.Repeat("HIDDEN", 100000)}}},
				Coverage: map[string]any{"complete": false, "projectErrors": []any{"error"}, "diagnostics": map[string]any{"critical": []any{"error"}, "advisory": []any{}, "unknown": []any{}}},
				Warnings: []string{"Bindings may be incomplete"},
				Results:  []jman.Result{{Name: "call()", Origin: "dependency", Artifact: "example:lib:1.0", SourceMatch: "coordinate", Path: "/sources/Lib.java", Line: 42, Column: 3, Snippet: "42 void call() {}\n"}},
			}
			out := compact(t, r)
			for _, want := range []string{"bindingResolved=true", "caller: project=:app", "critical=1 advisory=0 unknown=0", "warning: Bindings may be incomplete", "origin: dependency example:lib:1.0 [source-match: coordinate]", "/sources/Lib.java:42:3", "42 void call() {}", "status: partial"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in %s", want, out)
				}
			}
			if strings.Contains(out, "HIDDEN") {
				t.Fatal("context leaked full build")
			}
		})
	}
}

func TestCompactByteBudgetPreservesCursorAndStatus(t *testing.T) {
	for _, budget := range []int{512, 1024, 8192} {
		for _, command := range []string{"definition", "references", "implementations", "hover", "deps", "prepare", "refresh", "doctor", "status", "stop"} {
			r := jman.Response{Status: "partial", Query: jman.Request{Command: command, Limit: 20, MaxBytes: budget}, NextCursor: "snapshot-token", Warnings: []string{"uncertain"}, Results: []jman.Result{{Name: "symbol", Snippet: strings.Repeat("가나다\n", 10000)}}}
			out := compact(t, r)
			for _, want := range []string{"warning: uncertain", "output truncated", "nextCursor: snapshot-token\nstatus: partial\n"} {
				if !strings.Contains(out, want) {
					t.Fatalf("%s budget %d: missing %q", command, budget, want)
				}
			}
		}
	}
}

func TestDoctorEntryLimit(t *testing.T) {
	r := jman.Response{Status: "partial", Query: jman.Request{Command: "doctor", Limit: 1, MaxBytes: 8192}, Results: []jman.Result{{Name: "workspace", Detail: map[string]any{"problems": []any{map[string]any{"message": "first"}, map[string]any{"message": "second"}}}}}}
	out := compact(t, r)
	if !strings.Contains(out, "message=first") || strings.Contains(out, "message=second") || !strings.Contains(out, "1 more problems") {
		t.Fatal(out)
	}
}

func TestStatusSummary(t *testing.T) {
	r := jman.Response{Status: "ok", Query: jman.Request{Command: "status", Limit: 20, MaxBytes: 8192}, Context: map[string]any{"daemonPid": 1, "maxSessions": 5, "idleTimeout": "15m"}, Results: []jman.Result{{Name: "/build", Detail: map[string]any{"state": "ready", "pid": 2, "cacheHit": true, "watcher": "active", "resources": map[string]any{"rssKiB": 1234}}}}}
	out := compact(t, r)
	for _, want := range []string{"daemonPid=1", "maxSessions=5", "state=ready", "cacheHit=true", "watcher=active", "rssKiB=1234"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	r.Results = []jman.Result{{Name: "daemon not running", Detail: map[string]string{"socket": "/daemon.sock", "nextAction": "run jman daemon"}}}
	out = compact(t, r)
	if !strings.Contains(out, "socket=/daemon.sock; nextAction=run jman daemon") {
		t.Fatal(out)
	}
}

func TestCompactErrorIsActionable(t *testing.T) {
	r := jman.Response{Status: "not-ready", Query: jman.Request{Command: "prepare", Limit: 20, MaxBytes: 8192}, Error: &jman.Problem{Code: "IMPORT_TIMEOUT", Message: strings.Repeat("failure", 10000), NextAction: "jman status"}}
	out := compact(t, r)
	for _, want := range []string{"IMPORT_TIMEOUT", "next: jman status", "status: not-ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}

func TestSummaryFieldsAreSingleLineAndBounded(t *testing.T) {
	s := short(strings.Repeat("가", 500) + "\r\nnext line")
	if len(s) > 512 || !utf8.ValidString(s) || strings.ContainsAny(s, "\r\n") || !strings.HasSuffix(s, "…") {
		t.Fatal(s)
	}
	if short("first\r\nsecond") != "first  second" {
		t.Fatal("multiline summary field was not normalized")
	}
}

func TestSessionEntryLimit(t *testing.T) {
	r := jman.Response{Status: "ok", Query: jman.Request{Command: "status", Limit: 1, MaxBytes: 8192}, Results: []jman.Result{{Name: "first"}, {Name: "second"}}}
	out := compact(t, r)
	if !strings.Contains(out, "first") || strings.Contains(out, "second") || !strings.Contains(out, "1 more results") {
		t.Fatal(out)
	}
}

func TestReadByteBudgetIncludesTruncationNotice(t *testing.T) {
	var out bytes.Buffer
	renderRead(&out, strings.Repeat("가", 170), true, 512)
	if out.Len() > 512 || !utf8.Valid(out.Bytes()) || !strings.Contains(out.String(), "narrow --lines") {
		t.Fatal(out.String())
	}
	out.Reset()
	renderRead(&out, "1 source\n", false, 512)
	if out.String() != "1 source\n" {
		t.Fatal(out.String())
	}
}

func TestExplainRetainsFullDetails(t *testing.T) {
	r := decoded(t, jman.Response{Status: "ok", Query: jman.Request{Command: "prepare", Limit: 1, MaxBytes: 512},
		Context: map[string]any{"fullContext": "CONTEXT_SENTINEL"}, Coverage: map[string]any{"fullCoverage": "COVERAGE_SENTINEL"},
		Results: []jman.Result{{Name: "workspace", Detail: map[string]any{"model": strings.Repeat("MODEL_SENTINEL", 100), "settings": "SETTINGS_SENTINEL"}, Binary: "/binary.jar", BinaryDigest: "digest"}},
	})
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	render(r, true)
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MODEL_SENTINEL", "SETTINGS_SENTINEL", "CONTEXT_SENTINEL", "COVERAGE_SENTINEL", "binary: /binary.jar", "sha256: digest"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Fatalf("missing %q in full output", want)
		}
	}
	if len(b) <= 512 {
		t.Fatal("--explain unexpectedly byte-bounded")
	}
}
