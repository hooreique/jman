package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jman-dev/jman/internal/jman"
)

const help = `jman — build-aware Java navigation

Usage: jman COMMAND [FILE:LINE[:COLUMN]] [OPTIONS]

Commands:
  definition       Resolve the selected symbol and show source + provenance
  references       Find uses in the imported workspace
  implementations  Find static implementation candidates
  hover            Show type/signature documentation
  deps             Show caller's selected Gradle dependencies
  read PATH        Read source (--lines START:END)
  prepare          Import and warm the current build (--generate runs configured tasks)
  refresh          Reimport after build/dependency changes
  status           List active workspace sessions
  doctor           Diagnose build/classpath/processor problems
  skill install    Install the bundled skill (--target DIRECTORY)
  daemon           Run the user service (--max-sessions N)

Options:
  --project PATH   Explicit Gradle build root (otherwise discovered)
  --symbol NAME    Select identifier on the given line
  --occurrence N   Disambiguate repeated identifiers (1-based)
  --file PATH --line N --column N   Explicit location; Unicode columns are 1-based
  --limit N        Results per page (default 20)
  --max-bytes N    Source excerpt budget (default 8192)
  --cursor TOKEN   Next page of the same snapshot
  --timeout 120s   End-to-end deadline, including startup and queue
  --json          Versioned structured response
  --explain       Include full provenance and coverage in text output

Environment: JMAN_SOCKET, JMAN_CACHE_HOME; project settings: .jman.json
`

func main()            { os.Exit(run(os.Args[1:])) }
func fail(e error) int { fmt.Fprintln(os.Stderr, "jman:", e); return 2 }
func run(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Print(help)
		return 0
	}
	if args[0] == "--version" {
		fmt.Println("jman 0.1.0 (schema 1)")
		return 0
	}
	command := args[0]
	args = args[1:]
	if command == "daemon" {
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		n := flags.Int("max-sessions", 2, "maximum active JDTLS sessions")
		if e := flags.Parse(args); e != nil {
			return fail(e)
		}
		if e := jman.RunDaemon(*n); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 4
		}
		return 0
	}
	if command == "skill" {
		return installSkill(args)
	}
	q := jman.Request{Command: command, Limit: 20, MaxBytes: 8192}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.StringVar(&q.Project, "project", "", "build root")
	flags.StringVar(&q.File, "file", "", "source file")
	flags.IntVar(&q.Line, "line", 0, "line")
	flags.IntVar(&q.Column, "column", 0, "column")
	flags.StringVar(&q.Symbol, "symbol", "", "identifier")
	flags.IntVar(&q.Occurrence, "occurrence", 0, "occurrence")
	flags.IntVar(&q.Limit, "limit", 20, "page limit")
	flags.IntVar(&q.MaxBytes, "max-bytes", 8192, "excerpt byte budget")
	flags.StringVar(&q.Cursor, "cursor", "", "page cursor")
	jsonOut := flags.Bool("json", false, "JSON output")
	explain := flags.Bool("explain", false, "full details")
	timeout := flags.Duration("timeout", 120*time.Second, "deadline")
	lines := flags.String("lines", "1:80", "source range")
	generate := flags.Bool("generate", false, "run configured generation tasks")
	// Go's flag parser stops at a positional argument; accept conventional flags after LOCATION.
	var positional []string
	var options []string
	booleans := map[string]bool{"--json": true, "--explain": true, "--generate": true, "--help": true, "-h": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			options = append(options, a)
			if !strings.Contains(a, "=") && !booleans[a] && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
		} else {
			positional = append(positional, a)
		}
	}
	if e := flags.Parse(options); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return fail(e)
	}
	if len(positional) > 1 {
		return fail(fmt.Errorf("expected one location"))
	}
	if len(positional) == 1 {
		file, line, column, e := jman.ParseLocation(positional[0])
		if e != nil {
			return fail(e)
		}
		q.File = file
		if q.Line == 0 {
			q.Line = line
		}
		if q.Column == 0 {
			q.Column = column
		}
	}
	if q.Limit < 1 || q.Limit > 1000 || q.MaxBytes < 512 || *timeout <= 0 {
		return fail(fmt.Errorf("limit must be 1..1000, max-bytes >=512, timeout positive"))
	}
	if command == "read" {
		return readSource(q.File, *lines, q.MaxBytes, *jsonOut)
	}
	allowed := map[string]bool{"definition": true, "references": true, "implementations": true, "hover": true, "prepare": true, "refresh": true, "status": true, "doctor": true, "deps": true}
	if !allowed[command] {
		return fail(fmt.Errorf("unknown command %q; use jman --help", command))
	}
	var e error
	if q.File != "" {
		q.File, e = jman.Canonical(q.File)
		if e != nil {
			return fail(e)
		}
	}
	if command != "status" {
		if q.Project == "" {
			path := q.File
			if path == "" {
				path = "."
			}
			q.Project, e = jman.FindProject(path)
		} else {
			q.Project, e = jman.Canonical(q.Project)
		}
		if e != nil {
			return fail(e)
		}
	}
	if command == "definition" || command == "references" || command == "implementations" || command == "hover" || command == "deps" {
		if q.File == "" {
			return fail(fmt.Errorf("%s requires a source file", command))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	q.Deadline, _ = ctx.Deadline()
	if *generate {
		if command != "prepare" {
			return fail(fmt.Errorf("--generate is only valid with prepare"))
		}
		if e = jman.Generate(ctx, q.Project); e != nil {
			return fail(e)
		}
	}
	response, e := jman.Client(ctx, q)
	if e != nil {
		response = jman.Response{SchemaVersion: 1, Status: "error", Query: q, Results: []jman.Result{}, Error: &jman.Problem{Code: "TRANSPORT_FAILED", Message: e.Error(), Retryable: true, NextAction: "inspect daemon.log or retry with a longer --timeout"}}
	}
	if *jsonOut {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(response)
	} else {
		render(response, *explain)
	}
	switch response.Status {
	case "ok", "not-found":
		return 0
	case "ambiguous":
		return 2
	case "partial", "not-ready":
		return 3
	default:
		return 4
	}
}
func render(r jman.Response, explain bool) {
	if r.Error != nil {
		fmt.Printf("%s: %s\n", r.Error.Code, r.Error.Message)
		if r.Error.NextAction != "" {
			fmt.Println("next:", r.Error.NextAction)
		}
	}
	for _, v := range r.Results {
		fmt.Println(v.Name)
		if v.Origin != "" {
			fmt.Printf("origin: %s", v.Origin)
			if v.Artifact != "" {
				fmt.Printf(" %s", v.Artifact)
			}
			if v.SourceMatch != "" {
				fmt.Printf(" [source-match: %s]", v.SourceMatch)
			}
			fmt.Println()
		}
		if v.Path != "" {
			fmt.Printf("source: %s:%d:%d\n", v.Path, v.Line, v.Column)
		}
		if v.Snippet != "" {
			fmt.Print(v.Snippet)
		}
		if v.Detail != nil {
			b, _ := json.MarshalIndent(v.Detail, "", "  ")
			fmt.Println(string(b))
		}
		if explain && v.Binary != "" {
			fmt.Printf("binary: %s\nsha256: %s\n", v.Binary, v.BinaryDigest)
		}
	}
	if r.Context != nil {
		b, _ := json.Marshal(r.Context)
		fmt.Println("context:", string(b))
	}
	if r.Coverage != nil {
		if explain {
			b, _ := json.Marshal(r.Coverage)
			fmt.Println("coverage:", string(b))
		} else {
			fmt.Println("coverage: imported workspace; static Java analysis")
		}
	}
	for _, w := range r.Warnings {
		fmt.Println("warning:", w)
	}
	if r.NextCursor != "" {
		fmt.Println("nextCursor:", r.NextCursor)
	}
	if r.Status != "ok" {
		fmt.Println("status:", r.Status)
	}
}
func readSource(path, lines string, limit int, jsonOut bool) int {
	parts := strings.Split(lines, ":")
	if len(parts) != 2 {
		return fail(fmt.Errorf("--lines must be START:END"))
	}
	start, e := strconv.Atoi(parts[0])
	if e != nil {
		return fail(e)
	}
	end, e := strconv.Atoi(parts[1])
	if e != nil || start < 1 || end < start {
		return fail(fmt.Errorf("invalid line range"))
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return fail(e)
	}
	all := strings.Split(string(b), "\n")
	var out strings.Builder
	truncated := false
	for i := start - 1; i < end && i < len(all); i++ {
		line := fmt.Sprintf("%d %s\n", i+1, all[i])
		if out.Len()+len(line) > limit {
			truncated = true
			break
		}
		out.WriteString(line)
	}
	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "path": path, "text": out.String(), "truncated": truncated})
	} else {
		fmt.Print(out.String())
		if truncated {
			fmt.Println("… truncated; narrow --lines or increase --max-bytes")
		}
	}
	return 0
}
func installSkill(args []string) int {
	if len(args) == 0 || args[0] != "install" {
		return fail(fmt.Errorf("usage: jman skill install --target DIRECTORY"))
	}
	flags := flag.NewFlagSet("skill install", flag.ContinueOnError)
	target := flags.String("target", "", "skill directory")
	if e := flags.Parse(args[1:]); e != nil {
		return fail(e)
	}
	if *target == "" {
		return fail(fmt.Errorf("--target is required"))
	}
	source := os.Getenv("JMAN_SKILL")
	if source == "" {
		source = "skills/jman/SKILL.md"
	}
	b, e := os.ReadFile(source)
	if e != nil {
		return fail(e)
	}
	dst := filepath.Join(*target, "SKILL.md")
	if old, e := os.ReadFile(dst); e == nil {
		if string(old) != string(b) {
			return fail(fmt.Errorf("%s differs; preserve or move it before installing", dst))
		}
		fmt.Println("already installed:", dst)
		return 0
	}
	if e = os.MkdirAll(*target, 0755); e != nil {
		return fail(e)
	}
	f, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return fail(e)
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		return fail(e)
	}
	if ce != nil {
		return fail(ce)
	}
	fmt.Println("installed:", dst)
	return 0
}
