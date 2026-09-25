package jman

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hooreique/jman/internal/lsp"
)

type Config struct {
	JavaHome       string         `json:"javaHome"`
	GradleJavaHome string         `json:"gradleJavaHome"`
	GradleHome     string         `json:"gradleHome"`
	Offline        bool           `json:"offline"`
	GenerateTasks  []string       `json:"generateTasks"`
	Settings       map[string]any `json:"settings"`
}
type Session struct {
	root         string
	gate         chan struct{}
	rpc          *lsp.Client
	cmd          *exec.Cmd
	processDone  chan struct{}
	mu           sync.Mutex
	state        string
	message      string
	initialError string
	ready        chan struct{}
	readyOnce    sync.Once
	documents    map[string]string
	versions     map[string]int
	files        map[string]string
	extraRoots   []string
	buildHash    string
	artifactHash string
	model        *BuildModel
	modelError   error
	config       Config
	settings     map[string]any
	lastUsed     time.Time
	failures     int
	nextStart    time.Time
}

func newSession(root string) *Session {
	return &Session{root: root, gate: make(chan struct{}, 1), state: "stopped", documents: map[string]string{}, versions: map[string]int{}, files: map[string]string{}, lastUsed: time.Now()}
}
func (s *Session) acquire(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Session) release() { s.mu.Lock(); s.lastUsed = time.Now(); s.mu.Unlock(); <-s.gate }
func (s *Session) status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"root": s.root, "state": s.state, "message": s.message, "lastUsed": s.lastUsed}
}
func (s *Session) lastAccess() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.lastUsed }
func (s *Session) setState(state, message string) {
	s.mu.Lock()
	s.state = state
	s.message = message
	s.mu.Unlock()
}
func (s *Session) handler(method string, params json.RawMessage) (any, error) {
	switch method {
	case "language/status":
		var p struct{ Type, Message string }
		_ = json.Unmarshal(params, &p)
		if p.Type == "Started" {
			s.setState("indexing", p.Message)
			s.readyOnce.Do(func() { close(s.ready) })
		} else if p.Type == "Error" {
			s.mu.Lock()
			s.initialError = p.Message
			s.mu.Unlock()
			s.setState("degraded", p.Message)
			s.readyOnce.Do(func() { close(s.ready) })
		} else {
			s.setState("importing", p.Message)
		}
	case "workspace/configuration":
		var p struct{ Items []struct{ Section string } }
		_ = json.Unmarshal(params, &p)
		out := make([]any, len(p.Items))
		for i, item := range p.Items {
			var v any = s.settings
			for _, k := range strings.Split(item.Section, ".") {
				if m, ok := v.(map[string]any); ok {
					v = m[k]
				} else {
					v = nil
				}
			}
			out[i] = v
		}
		return out, nil
	case "workspace/workspaceFolders":
		return []any{map[string]any{"uri": FileURI(s.root), "name": filepath.Base(s.root)}}, nil
	case "workspace/applyEdit":
		return map[string]any{"applied": false, "failureReason": "jman navigation does not apply workspace edits"}, nil
	case "window/showMessageRequest":
		return nil, nil
	}
	return nil, nil
}
func (s *Session) start(ctx context.Context) error {
	if s.rpc != nil {
		select {
		case <-s.rpc.Done():
			s.stop()
			s.failedStart("JDTLS exited unexpectedly")
		default:
			return nil
		}
	}
	if time.Now().Before(s.nextStart) {
		return fmt.Errorf("JDTLS restart backoff until %s", s.nextStart.Format(time.RFC3339))
	}
	data, e := os.ReadFile(filepath.Join(s.root, ".jman.json"))
	if e == nil {
		if e = json.Unmarshal(data, &s.config); e != nil {
			return fmt.Errorf(".jman.json: %w", e)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	javaHome := s.config.JavaHome
	if javaHome == "" {
		javaHome = os.Getenv("JMAN_JAVA_HOME")
	}
	gradleHome := s.config.GradleHome
	if gradleHome == "" {
		gradleHome = os.Getenv("JMAN_GRADLE_HOME")
	}
	gradleJava := s.config.GradleJavaHome
	if gradleJava == "" {
		gradleJava = os.Getenv("JAVA_HOME")
		if gradleJava == "" {
			gradleJava = os.Getenv("JMAN_JAVA_HOME")
		}
	}
	s.settings = map[string]any{"java": map[string]any{
		"home": javaHome, "autobuild": map[string]any{"enabled": true},
		"configuration": map[string]any{"updateBuildConfiguration": "automatic"},
		"jdt":           map[string]any{"ls": map[string]any{"lombokSupport": map[string]any{"enabled": true}}},
		"import":        map[string]any{"gradle": map[string]any{"enabled": true, "home": gradleHome, "java": map[string]any{"home": gradleJava}, "wrapper": map[string]any{"enabled": true}, "offline": map[string]any{"enabled": s.config.Offline}, "annotationProcessing": map[string]any{"enabled": true}}, "maven": map[string]any{"enabled": true}, "generatesMetadataFilesAtProjectRoot": false},
		"maven":         map[string]any{"downloadSources": true},
	}}
	merge(s.settings, s.config.Settings)
	launcher := os.Getenv("JMAN_JDTLS")
	if launcher == "" {
		launcher = "jdtls"
	}
	identity := digest([]byte(s.root + "\x00" + launcher + "\x00" + os.Getenv("JMAN_EXTENSION") + "\x00" + os.Getenv("JMAN_LOMBOK_AGENT") + "\x00" + javaHome + "\x00" + s.buildHash + "\x00" + s.artifactHash))[:24]
	dir := filepath.Join(CacheDir(), "workspaces", identity)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	log, e := os.OpenFile(filepath.Join(dir, "stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	cmd := exec.Command(launcher, "-data", filepath.Join(dir, "data"), "-configuration", filepath.Join(dir, "config"), "--jvm-arg=-Xms128m", "--jvm-arg=-Xmx1536m")
	if home := os.Getenv("JMAN_JDTLS_HOME"); home != "" {
		jars, err := filepath.Glob(filepath.Join(home, "plugins", "org.eclipse.equinox.launcher_*.jar"))
		if err != nil || len(jars) != 1 {
			log.Close()
			return fmt.Errorf("expected one Equinox launcher under %s", home)
		}
		cmd = exec.Command(filepath.Join(javaHome, "bin", "java"),
			"-Declipse.application=org.eclipse.jdt.ls.core.id1", "-Dosgi.bundles.defaultStartLevel=4", "-Declipse.product=org.eclipse.jdt.ls.core.product",
			"-Dosgi.sharedConfiguration.area="+filepath.Join(home, jdtlsConfigDir), "-Dosgi.sharedConfiguration.area.readOnly=true", "-Dosgi.configuration.cascaded=true",
			"-Xms128m", "-Xmx1536m", "--add-modules=ALL-SYSTEM", "--add-opens", "java.base/java.util=ALL-UNNAMED", "--add-opens", "java.base/java.lang=ALL-UNNAMED",
			"-jar", jars[0], "-data", filepath.Join(dir, "data"), "-configuration", filepath.Join(dir, "config"))
	}
	cmd.Dir = s.root
	if agent := os.Getenv("JMAN_LOMBOK_AGENT"); agent != "" {
		if os.Getenv("JMAN_JDTLS_HOME") != "" {
			cmd.Args = append(cmd.Args[:1], append([]string{"-javaagent:" + agent}, cmd.Args[1:]...)...)
		} else {
			cmd.Args = append(cmd.Args, "--jvm-arg=-javaagent:"+agent)
		}
	}
	cmd.Env = append(os.Environ(), "JAVA_HOME="+javaHome)
	cmd.Stderr = log
	cmd.SysProcAttr = childProcessAttrs()
	stdin, e := cmd.StdinPipe()
	if e != nil {
		log.Close()
		return e
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		stdin.Close()
		log.Close()
		return e
	}
	if e = cmd.Start(); e != nil {
		stdin.Close()
		log.Close()
		s.failedStart(e.Error())
		return e
	}
	s.cmd = cmd
	s.processDone = make(chan struct{})
	s.ready = make(chan struct{})
	s.readyOnce = sync.Once{}
	s.mu.Lock()
	s.initialError = ""
	s.mu.Unlock()
	s.setState("starting", "")
	s.rpc = lsp.New(stdout, stdin, s.handler)
	done := s.processDone
	go func() { _ = cmd.Wait(); _ = log.Close(); close(done) }()
	bundles := []string{}
	if p := os.Getenv("JMAN_EXTENSION"); p != "" {
		bundles = append(bundles, p)
	} else {
		s.stop()
		return fmt.Errorf("JMAN_EXTENSION is required; use the Nix package or development shell")
	}
	var result any
	err := s.rpc.Call(ctx, "initialize", map[string]any{
		"processId": os.Getpid(), "rootUri": FileURI(s.root), "workspaceFolders": []any{map[string]any{"uri": FileURI(s.root), "name": filepath.Base(s.root)}},
		"capabilities":          map[string]any{"workspace": map[string]any{"configuration": true, "workspaceFolders": true}, "textDocument": map[string]any{"definition": map[string]any{"linkSupport": true}}},
		"initializationOptions": map[string]any{"bundles": bundles, "settings": s.settings, "extendedClientCapabilities": map[string]any{"classFileContentsSupport": true, "progressReportProvider": true}},
	}, &result)
	if err != nil {
		s.stop()
		if ctx.Err() == nil {
			s.failedStart(err.Error())
		}
		return err
	}
	if e = s.rpc.Notify("initialized", map[string]any{}); e != nil {
		s.stop()
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.rpc.Done():
		return fmt.Errorf("JDTLS exited; inspect %s", filepath.Join(dir, "stderr.log"))
	case <-s.ready:
	}
	s.failures = 0
	s.nextStart = time.Time{}
	return nil
}
func (s *Session) failedStart(message string) {
	s.failures++
	delay := time.Second * time.Duration(1<<min(s.failures-1, 5))
	s.nextStart = time.Now().Add(delay)
	s.setState("failed", message)
}
func merge(dst, src map[string]any) {
	for k, v := range src {
		if m, ok := v.(map[string]any); ok {
			if d, ok := dst[k].(map[string]any); ok {
				merge(d, m)
				continue
			}
		}
		dst[k] = v
	}
}
func (s *Session) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-s.processDone:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			<-s.processDone
		}
		if s.rpc != nil {
			<-s.rpc.Done()
		}
	}
	s.rpc = nil
	s.cmd = nil
	s.documents = map[string]string{}
	s.versions = map[string]int{}
	s.config = Config{}
	s.setState("stopped", "")
}
func (s *Session) execute(ctx context.Context, command string, args []any, out any) error {
	return s.rpc.Call(ctx, "workspace/executeCommand", map[string]any{"command": command, "arguments": args}, out)
}

// Hash inputs, not mtimes: branch switches and same-size rewrites must invalidate results.
func scan(root string) (map[string]string, string, string, error) {
	files := map[string]string{}
	var names []string
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".gradle", ".metadata", "node_modules", ".jman-cache":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		ext := filepath.Ext(name)
		if ext != ".java" && ext != ".gradle" && ext != ".kts" && ext != ".toml" && ext != ".properties" && ext != ".lockfile" && ext != ".jar" && name != ".jman.json" && name != "pom.xml" && name != ".classpath" {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		files[path] = digest(b)
		names = append(names, path)
		return nil
	})
	if e != nil {
		return nil, "", "", e
	}
	sort.Strings(names)
	var all, build strings.Builder
	for _, p := range names {
		v := p + "\x00" + files[p] + "\n"
		all.WriteString(v)
		if filepath.Ext(p) != ".java" {
			build.WriteString(v)
		}
	}
	return files, digest([]byte(all.String())), digest([]byte(build.String())), nil
}
func (s *Session) sync(ctx context.Context, files map[string]string) ([]any, error) {
	changes := []any{}
	for path, hash := range files {
		if s.files[path] != hash {
			kind := 2
			if _, ok := s.files[path]; !ok {
				kind = 1
			}
			changes = append(changes, map[string]any{"uri": FileURI(path), "type": kind})
		}
	}
	for path := range s.files {
		if _, ok := files[path]; !ok {
			changes = append(changes, map[string]any{"uri": FileURI(path), "type": 3})
		}
	}
	if len(changes) > 0 {
		if e := s.rpc.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": changes}); e != nil {
			return nil, e
		}
	}
	for path, old := range s.documents {
		b, e := os.ReadFile(path)
		if e != nil {
			_ = s.rpc.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": FileURI(path)}})
			delete(s.documents, path)
			continue
		}
		if old != string(b) {
			if e = s.open(path, string(b)); e != nil {
				return nil, e
			}
		}
	}
	var out struct {
		Problems     []any    `json:"problems"`
		ProjectRoots []string `json:"projectRoots"`
	}
	if e := s.execute(ctx, "jman.sync", []any{}, &out); e != nil {
		return nil, e
	}
	s.files = files
	s.extraRoots = out.ProjectRoots
	s.setState("ready", "")
	if len(out.Problems) > 0 {
		s.setState("degraded", fmt.Sprintf("%d project errors", len(out.Problems)))
	}
	return out.Problems, nil
}
func (s *Session) scan() (map[string]string, string, string, error) {
	files, snapshot, build, e := scan(s.root)
	if e != nil {
		return nil, "", "", e
	}
	roots := append([]string{}, s.extraRoots...)
	sort.Strings(roots)
	for _, root := range roots {
		if root == s.root || strings.HasPrefix(root, s.root+string(os.PathSeparator)) {
			continue
		}
		extra, hash, bh, err := scan(root)
		if err != nil {
			return nil, "", "", err
		}
		for k, v := range extra {
			files[k] = v
		}
		snapshot = digest([]byte(snapshot + root + hash))
		build = digest([]byte(build + root + bh))
	}
	return files, snapshot, build, nil
}
func (s *Session) open(path, text string) error {
	old, exists := s.documents[path]
	if exists && old == text {
		return nil
	}
	s.versions[path]++
	doc := map[string]any{"uri": FileURI(path), "version": s.versions[path]}
	var e error
	if exists {
		e = s.rpc.Notify("textDocument/didChange", map[string]any{"textDocument": doc, "contentChanges": []any{map[string]any{"text": text}}})
	} else {
		doc["languageId"] = "java"
		doc["text"] = text
		e = s.rpc.Notify("textDocument/didOpen", map[string]any{"textDocument": doc})
	}
	if e == nil {
		s.documents[path] = text
	}
	return e
}
