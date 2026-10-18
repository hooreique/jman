package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/hooreique/jman/internal/jman"
)

func render(r jman.Response, explain bool) {
	if explain {
		renderFull(r, true)
		return
	}
	renderCompact(os.Stdout, r)
}

func renderRead(out io.Writer, text string, truncated bool, budget int) {
	const notice = "\n… truncated; narrow --lines or increase --max-bytes\n"
	if truncated {
		fmt.Fprint(out, string(validPrefix([]byte(text), max(0, budget-len(notice)))), notice)
	} else {
		fmt.Fprint(out, text)
	}
}

// Keep the response status and snapshot cursor even when the body is cut short.
// JSON and --explain intentionally retain the full schema-1 response.
func renderCompact(out io.Writer, r jman.Response) {
	budget := r.Query.MaxBytes
	if budget < 512 {
		budget = 8192
	}
	footer := ""
	if r.NextCursor != "" {
		footer += "nextCursor: " + r.NextCursor + "\n"
	}
	if r.Status != "ok" || r.Query.Command == "prepare" || r.Query.Command == "refresh" || r.Query.Command == "stop" {
		footer += "status: " + r.Status + "\n"
	}
	const notice = "\n… output truncated; narrow --limit or increase --max-bytes; --json / --explain show full details\n"
	w := &budgetWriter{out: out, remaining: max(0, budget-len(footer)-len(notice))}
	if r.Error != nil {
		fmt.Fprintf(w, "%s: %s\n", short(r.Error.Code), short(r.Error.Message))
		if r.Error.NextAction != "" {
			fmt.Fprintln(w, "next:", short(r.Error.NextAction))
		}
	}
	// Put uncertainty before potentially large result lists so truncation cannot
	// make a partial result look like a trustworthy successful query.
	for _, warning := range r.Warnings {
		fmt.Fprintln(w, "warning:", short(warning))
	}
	if c := object(r.Context); len(c) > 0 {
		fields(w, "context", c, "root", "project", "sourceRoot", "sourceSet", "configuration", "semantics", "bindingResolved", "daemonPid", "maxSessions", "idleTimeout")
		if build := object(c["build"]); len(build) > 0 {
			fields(w, "caller", build, "project", "sourceSet", "configuration")
		}
	}
	if c := object(r.Coverage); len(c) > 0 {
		fmt.Fprintf(w, "coverage: imported workspace; static Java analysis; complete=%v; errors=%d\n", c["complete"], len(items(c["projectErrors"])))
		if d := object(c["diagnostics"]); len(d) > 0 {
			fmt.Fprintf(w, "diagnostics: critical=%d advisory=%d unknown=%d\n", len(items(d["critical"])), len(items(d["advisory"])), len(items(d["unknown"])))
		}
	}
	limit := r.Query.Limit
	if limit < 1 {
		limit = 20
	}
	for i, v := range r.Results {
		if i >= limit || w.truncated {
			if i < len(r.Results) && !w.truncated {
				fmt.Fprintf(w, "… %d more results; increase --limit or use --json\n", len(r.Results)-i)
			}
			break
		}
		fmt.Fprintln(w, short(v.Name))
		if v.Origin != "" {
			fmt.Fprint(w, "origin: ", short(v.Origin))
			if v.Artifact != "" {
				fmt.Fprint(w, " ", short(v.Artifact))
			}
			if v.SourceMatch != "" {
				fmt.Fprintf(w, " [source-match: %s]", short(v.SourceMatch))
			}
			fmt.Fprintln(w)
		}
		if v.Path != "" {
			fmt.Fprintf(w, "source: %s:%d:%d\n", short(v.Path), v.Line, v.Column)
		}
		if v.Snippet != "" {
			fmt.Fprint(w, v.Snippet)
		}
		detail := object(v.Detail)
		switch r.Query.Command {
		case "prepare", "refresh", "doctor":
			fields(w, "service", object(detail["service"]), "state", "message", "pid", "cacheHit", "watcher")
			modelSummary(w, object(detail["model"]))
			problems := items(detail["problems"])
			fmt.Fprintf(w, "problems: %d\n", len(problems))
			if r.Query.Command == "doctor" {
				for _, problem := range problems[:min(limit, len(problems))] {
					p := object(problem)
					fields(w, "problem", p, "project", "sourceSet", "location", "path", "message")
				}
				if len(problems) > limit {
					fmt.Fprintf(w, "… %d more problems; increase --limit or use --json\n", len(problems)-limit)
				}
			} else if len(problems) > 0 {
				fmt.Fprintln(w, "next: jman doctor --project", short(r.Query.Project))
			}
			fmt.Fprintln(w, "details: --explain or --json (full build model and settings)")
		case "deps":
			fields(w, "build", detail, "project", "sourceSet", "configuration")
			artifacts := items(detail["artifacts"])
			fmt.Fprintf(w, "dependencies: %d artifacts; %s classpath entries; %d roots; %s outputs\n", len(artifacts), countField(detail, "compileClasspath"), len(items(detail["roots"])), countField(detail, "outputs"))
			for _, artifact := range artifacts[:min(limit, len(artifacts))] {
				a := object(artifact)
				name := a["component"]
				if name == nil || name == "" {
					name = a["path"]
				}
				fmt.Fprintln(w, "  ", short(fmt.Sprint(name)))
			}
			if len(artifacts) > limit {
				fmt.Fprintf(w, "… %d more artifacts; increase --limit or use --json\n", len(artifacts)-limit)
			}
			fmt.Fprintln(w, "details: --explain or --json (selected paths, variants and attributes)")
		case "status":
			fields(w, "session", detail, "state", "message", "pid", "jdtlsMaxHeapMiB", "cacheHit", "syncCached", "watcher", "dirty", "socket", "nextAction")
			fields(w, "resources", object(detail["resources"]), "rssKiB", "heapUsedKiB", "heapTotalKiB")
		}
	}
	if w.truncated {
		fmt.Fprint(out, notice)
	}
	fmt.Fprint(out, footer)
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func items(v any) []any {
	a, _ := v.([]any)
	return a
}

func countField(values map[string]any, key string) string {
	if _, ok := values[key]; !ok {
		return "unknown"
	}
	return fmt.Sprint(len(items(values[key])))
}

func fields(w io.Writer, label string, values map[string]any, keys ...string) {
	var parts []string
	for _, key := range keys {
		if value, ok := values[key]; ok && value != "" {
			parts = append(parts, key+"="+short(fmt.Sprint(value)))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, "%s: %s\n", label, strings.Join(parts, "; "))
	}
}

func modelSummary(w io.Writer, model map[string]any) {
	if len(model) == 0 {
		fmt.Fprintln(w, "model: unavailable")
		return
	}
	contexts := items(model["contexts"])
	projects, artifacts := map[string]bool{}, map[string]bool{}
	for _, value := range contexts {
		c := object(value)
		if name, ok := c["project"].(string); ok {
			projects[name] = true
		}
		for _, value := range items(c["artifacts"]) {
			if path, ok := object(value)["path"].(string); ok {
				artifacts[path] = true
			}
		}
	}
	fmt.Fprintf(w, "model: %d projects; %d source sets; %d unique artifacts\n", len(projects), len(contexts), len(artifacts))
}

func short(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	if len(s) > 512 {
		return string(validPrefix([]byte(s), 509)) + "…"
	}
	return s
}

func validPrefix(p []byte, n int) []byte {
	n = min(n, len(p))
	for n > 0 && n < len(p) && !utf8.RuneStart(p[n]) {
		n--
	}
	return p[:n]
}

type budgetWriter struct {
	out       io.Writer
	remaining int
	truncated bool
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.truncated {
		return n, nil
	}
	if n > w.remaining {
		p = validPrefix(p, w.remaining)
		w.truncated = true
	}
	_, err := w.out.Write(p)
	w.remaining -= len(p)
	return n, err
}
