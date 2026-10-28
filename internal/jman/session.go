package jman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hooreique/jman/internal/lsp"
)

type Config struct {
	JDTLSMaxHeapMiB *int           `json:"jdtlsMaxHeapMiB,omitempty"`
	JavaHome        string         `json:"javaHome"`
	GradleJavaHome  string         `json:"gradleJavaHome"`
	GradleHome      string         `json:"gradleHome"`
	Offline         bool           `json:"offline"`
	GenerateTasks   []string       `json:"generateTasks"`
	Settings        map[string]any `json:"settings"`
}
type Session struct {
	root            string
	gate            chan struct{}
	rpc             *lsp.Client
	cmd             *exec.Cmd
	processDone     chan struct{}
	processClean    bool
	mu              sync.Mutex
	state           string
	message         string
	initialError    string
	ready           chan struct{}
	readyOnce       sync.Once
	documents       map[string]string
	versions        map[string]int
	files           map[string]string
	extraRoots      []string
	syncValid       bool
	lastSnapshot    string
	lastProblems    []any
	lastSyncTimings map[string]int64
	lastSync        time.Time
	watcher         *workspaceWatcher
	watchHash       string
	lastScan        time.Time
	lastCacheHit    bool
	buildHash       string
	artifactHash    string
	artifactStamp   string
	model           *BuildModel
	modelError      error
	config          Config
	settings        map[string]any
	lastUsed        time.Time
	failures        int
	nextStart       time.Time
	pid             int
	maxHeapMiB      int
	workspaceDir    string
	resumed         bool
}

func newSession(root string) *Session {
	return &Session{root: root, gate: make(chan struct{}, 1), state: "stopped", documents: map[string]string{}, versions: map[string]int{}, files: map[string]string{}, lastUsed: time.Now(), maxHeapMiB: 1536}
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
func (s *Session) status(ctx context.Context) map[string]any {
	s.mu.Lock()
	pid := s.pid
	state, message, lastUsed, maxHeap := s.state, s.message, s.lastUsed, s.maxHeapMiB
	lastSync, syncCached, lastScan, cacheHit := s.lastSync, s.syncValid, s.lastScan, s.lastCacheHit
	watcher := s.watcher
	s.mu.Unlock()
	watcherState := "unavailable"
	dirty := false
	if watcher != nil {
		watcherState = "active"
		dirty = watcher.pending()
	}
	resources := jdtResources(ctx, pid)
	return map[string]any{"root": s.root, "state": state, "message": message, "lastUsed": lastUsed, "pid": pid, "jdtlsMaxHeapMiB": maxHeap, "lastFullSync": lastSync, "syncCached": syncCached, "cacheHit": cacheHit, "watcher": watcherState, "dirty": dirty, "lastFullScan": lastScan, "resources": resources}
}

var heapUsage = regexp.MustCompile(`(?m)garbage-first heap\s+total\s+(\d+)K, used\s+(\d+)K`)

func jdtResources(ctx context.Context, pid int) map[string]any {
	resources := map[string]any{"rssKiB": nil, "heapUsedKiB": nil, "heapTotalKiB": nil}
	if pid <= 0 {
		return resources
	}
	// Resource metrics are best-effort and must not outlive the request or
	// spend an unbounded time attaching to an unresponsive JVM.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if out, err := resourceOutput(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(pid)); err == nil {
		if rss, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
			resources["rssKiB"] = rss
		}
	}
	if out, err := resourceOutput(ctx, "jcmd", strconv.Itoa(pid), "GC.heap_info"); err == nil {
		if match := heapUsage.FindStringSubmatch(string(out)); len(match) == 3 {
			resources["heapTotalKiB"], _ = strconv.Atoi(match[1])
			resources["heapUsedKiB"], _ = strconv.Atoi(match[2])
		}
	}
	return resources
}
func resourceOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = childProcessAttrs()
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// Also bound pipe draining if a descendant retains stdout after cancellation.
	cmd.WaitDelay = 10 * time.Millisecond
	return cmd.Output()
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
	maxHeapMiB, e := s.config.maxHeapMiB()
	if e != nil {
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
	s.workspaceDir = dir
	s.restoreCheckpoint()
	log, e := os.OpenFile(filepath.Join(dir, "stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	heapArg := fmt.Sprintf("-Xmx%dm", maxHeapMiB)
	cmd := exec.Command(launcher, "-data", filepath.Join(dir, "data"), "-configuration", filepath.Join(dir, "config"), "--jvm-arg=-Xms128m", "--jvm-arg="+heapArg)
	if home := os.Getenv("JMAN_JDTLS_HOME"); home != "" {
		jars, err := filepath.Glob(filepath.Join(home, "plugins", "org.eclipse.equinox.launcher_*.jar"))
		if err != nil || len(jars) != 1 {
			log.Close()
			return fmt.Errorf("expected one Equinox launcher under %s", home)
		}
		cmd = exec.Command(filepath.Join(javaHome, "bin", "java"),
			"-Declipse.application=org.eclipse.jdt.ls.core.id1", "-Dosgi.bundles.defaultStartLevel=4", "-Declipse.product=org.eclipse.jdt.ls.core.product",
			"-Dosgi.sharedConfiguration.area="+filepath.Join(home, jdtlsConfigDir), "-Dosgi.sharedConfiguration.area.readOnly=true", "-Dosgi.configuration.cascaded=true",
			"-Xms128m", heapArg, "--add-modules=ALL-SYSTEM", "--add-opens", "java.base/java.util=ALL-UNNAMED", "--add-opens", "java.base/java.lang=ALL-UNNAMED",
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
	s.processClean = false
	s.pid = cmd.Process.Pid
	s.maxHeapMiB = maxHeapMiB
	s.mu.Unlock()
	s.setState("starting", "")
	s.rpc = lsp.New(stdout, stdin, s.handler)
	done := s.processDone
	go func() {
		err := cmd.Wait()
		_ = log.Close()
		s.mu.Lock()
		s.pid = 0
		s.processClean = err == nil
		s.mu.Unlock()
		close(done)
	}()
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
func (c Config) maxHeapMiB() (int, error) {
	if c.JDTLSMaxHeapMiB == nil {
		return 1536, nil
	}
	if *c.JDTLSMaxHeapMiB < 128 {
		return 0, fmt.Errorf(".jman.json: jdtlsMaxHeapMiB must be an integer >= 128")
	}
	return *c.JDTLSMaxHeapMiB, nil
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
	s.replaceWatcher(nil)
	clean := false
	if s.cmd != nil && s.cmd.Process != nil {
		// LSP shutdown saves the workspace and index before exit. A signal alone
		// leaves Eclipse recovering an unclean workspace on every restart.
		shutdown := make(chan bool, 1)
		rpc := s.rpc
		go func() {
			defer close(shutdown)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if rpc == nil || rpc.Call(ctx, "shutdown", nil, nil) != nil {
				shutdown <- false
				return
			}
			shutdown <- rpc.Notify("exit", nil) == nil
		}()
		select {
		case clean = <-shutdown:
		case <-time.After(5 * time.Second):
		}
		if !clean {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
		}
		select {
		case <-s.processDone:
		case <-time.After(3 * time.Second):
			clean = false
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			<-s.processDone
		}
		if s.rpc != nil {
			<-s.rpc.Done()
		}
		// Ensure the shutdown goroutine has stopped before clearing s.rpc.
		select {
		case <-shutdown:
		default:
			if !clean {
				<-shutdown
			}
		}
		s.mu.Lock()
		clean = clean && s.processClean
		s.mu.Unlock()
	}
	if clean && s.syncValid {
		s.saveCheckpoint()
	}
	s.rpc = nil
	s.cmd = nil
	s.documents = map[string]string{}
	s.versions = map[string]int{}
	s.config = Config{}
	s.workspaceDir = ""
	s.resumed = false
	s.mu.Lock()
	s.syncValid = false
	s.lastSnapshot = ""
	s.lastProblems = nil
	s.lastSync = time.Time{}
	s.watchHash = ""
	s.lastScan = time.Time{}
	s.lastCacheHit = false
	s.mu.Unlock()
	s.setState("stopped", "")
}
func (s *Session) execute(ctx context.Context, command string, args []any, out any) error {
	return s.rpc.Call(ctx, "workspace/executeCommand", map[string]any{"command": command, "arguments": args}, out)
}

// Hash inputs, not mtimes: branch switches and same-size rewrites must invalidate results.
func scan(root string) (map[string]string, string, string, error) {
	return scanTree(root, false)
}

func scanTree(root string, declared bool, projects ...[]string) (map[string]string, string, string, error) {
	files := map[string]string{}
	var sourceRoots []string
	if declared {
		sourceRoots = []string{root}
	}
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !scanPathIncluded(path, d.IsDir(), []string{root}, sourceRoots, projects...) {
			if d.IsDir() {
				for _, roots := range projects {
					for _, root := range roots {
						if withinRoot(root, path) {
							return nil
						}
					}
				}
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !trackedFile(d.Name()) {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		files[path] = digest(b)
		return nil
	})
	if e != nil {
		return nil, "", "", e
	}
	return snapshotFiles(files)
}
func ignoredScanDirectory(name string) bool {
	switch name {
	case ".git", ".gradle", ".metadata", "node_modules", ".jman-cache", ".idea", ".settings", "bin", "reports", "test-results", "coverage":
		return true
	}
	return false
}
func trackedFile(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".java" || ext == ".gradle" || ext == ".kts" || ext == ".toml" || ext == ".properties" || ext == ".lockfile" || ext == ".jar" || name == ".jman.json" || name == "pom.xml" || name == ".classpath"
}
func snapshotFiles(files map[string]string) (map[string]string, string, string, error) {
	names := make([]string, 0, len(files))
	for path := range files {
		names = append(names, path)
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
func (s *Session) scanDirty(paths []string) (map[string]string, string, string, error) {
	files := make(map[string]string, len(s.files))
	for path, hash := range s.files {
		files[path] = hash
	}
	roots, sourceRoots := s.scanRoots(), s.model.sourceRoots()
	projectRoots := s.projectRoots()
	for _, path := range paths {
		if !scanPathIncluded(path, false, roots, sourceRoots, projectRoots) || !trackedFile(filepath.Base(path)) {
			delete(files, path)
			continue
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			delete(files, path)
			continue
		}
		if err != nil {
			return nil, "", "", err
		}
		if info.IsDir() {
			return nil, "", "", fmt.Errorf("directory changed: %s", path)
		}
		hash, err := fileDigest(path)
		if err != nil {
			return nil, "", "", err
		}
		files[path] = hash
	}
	return snapshotFiles(files)
}
func (s *Session) startWatcher() {
	roots := s.scanRoots()
	sourceRoots := s.model.sourceRoots()
	projectRoots := s.projectRoots()
	roots = append(roots, projectRoots...)
	for _, root := range sourceRoots {
		// Watch the nearest existing ancestor for sources created after import.
		for {
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				break
			}
			parent := filepath.Dir(root)
			if parent == root {
				break
			}
			root = parent
		}
		roots = append(roots, root)
	}
	watcher, err := newWorkspaceWatcher(roots, sourceRoots, projectRoots)
	if err != nil {
		watcher = nil
	}
	s.replaceWatcher(watcher)
	if hash, err := workspaceFingerprint(s.root); err == nil {
		s.watchHash = hash
	}
}

// Publish the replacement before closing the old watcher. Status readers only
// observe the pointer under mu; native shutdown must not hold the session lock.
func (s *Session) replaceWatcher(watcher *workspaceWatcher) {
	s.mu.Lock()
	old := s.watcher
	s.watcher = watcher
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
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
		Problems     []any            `json:"problems"`
		ProjectRoots []string         `json:"projectRoots"`
		Timings      map[string]int64 `json:"timings"`
	}
	// A clean restored workspace with unchanged inputs needs a readiness
	// barrier, not another recursive refresh. Changed/unknown state still uses
	// the conservative refresh path.
	if e := s.execute(ctx, "jman.sync", []any{map[string]any{"refresh": !s.resumed || len(changes) > 0}}, &out); e != nil {
		return nil, e
	}
	s.files = files
	s.extraRoots = out.ProjectRoots
	s.lastProblems = out.Problems
	s.lastSyncTimings = out.Timings
	s.setState("ready", "")
	if len(out.Problems) > 0 {
		s.setState("degraded", fmt.Sprintf("%d project errors", len(out.Problems)))
	}
	return out.Problems, nil
}
func filesChanged(previous, current map[string]string) bool {
	if len(previous) != len(current) {
		return true
	}
	for path, hash := range current {
		if previous[path] != hash {
			return true
		}
	}
	return false
}
func (s *Session) scan() (map[string]string, string, string, error) {
	projectRoots := s.projectRoots()
	files, _, _, e := scanTree(s.root, false, projectRoots)
	if e != nil {
		return nil, "", "", e
	}
	roots := append(append([]string{}, s.extraRoots...), projectRoots...)
	sort.Strings(roots)
	for _, root := range roots {
		if withinRoot(root, s.root) {
			continue
		}
		extra, _, _, err := scanTree(root, false, projectRoots)
		if err != nil {
			return nil, "", "", err
		}
		for k, v := range extra {
			files[k] = v
		}
	}
	for _, root := range s.model.sourceRoots() {
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			continue
		}
		extra, _, _, err := scanTree(root, true)
		if err != nil {
			return nil, "", "", err
		}
		for path, hash := range extra {
			files[path] = hash
		}
	}
	return snapshotFiles(files)
}

func (s *Session) projectRoots() []string {
	var roots []string
	if s.model != nil {
		roots = append(roots, s.model.ProjectRoots...)
	}
	// The first scan precedes model export, so protect the Gradle workspace root
	// immediately. Subprojects come from Gradle, not directory-name heuristics.
	if isGradleProject(s.root) {
		roots = append(roots, s.root)
	}
	return roots
}

func (s *Session) scanRoots() []string {
	roots := []string{s.root}
	for _, root := range s.extraRoots {
		if !withinRoot(root, s.root) {
			roots = append(roots, root)
		}
	}
	return roots
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
