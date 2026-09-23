package jman

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type Artifact struct {
	Path             string            `json:"path"`
	Component        string            `json:"component"`
	Variant          string            `json:"variant"`
	Attributes       map[string]string `json:"attributes"`
	ProjectComponent bool              `json:"projectComponent"`
	ModuleComponent  bool              `json:"moduleComponent"`
}
type BuildContext struct {
	Project       string     `json:"project"`
	SourceSet     string     `json:"sourceSet"`
	Configuration string     `json:"configuration"`
	Roots         []string   `json:"roots"`
	Artifacts     []Artifact `json:"artifacts"`
}
type BuildModel struct {
	Contexts []BuildContext `json:"contexts"`
}

func gradleCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	name := filepath.Join(root, "gradlew")
	if _, e := os.Stat(name); e != nil {
		name = "gradle"
		if executable := os.Getenv("JMAN_GRADLE"); executable != "" {
			name = executable
		}
		var config Config
		if b, e := os.ReadFile(filepath.Join(root, ".jman.json")); e == nil && json.Unmarshal(b, &config) == nil && config.GradleHome != "" {
			name = filepath.Join(config.GradleHome, "bin", "gradle")
			if _, e := os.Stat(name); e != nil {
				name = filepath.Join(config.GradleHome, "bin", "gradlew")
			}
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	return cmd
}
func loadModel(ctx context.Context, root string) (*BuildModel, error) {
	isGradle := false
	for _, name := range []string{"settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts"} {
		if _, e := os.Stat(filepath.Join(root, name)); e == nil {
			isGradle = true
		}
	}
	if !isGradle {
		return nil, nil
	}
	script := os.Getenv("JMAN_GRADLE_MODEL")
	if script == "" {
		return nil, fmt.Errorf("JMAN_GRADLE_MODEL is not configured")
	}
	dir := filepath.Join(CacheDir(), "models", digest([]byte(root))[:24])
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	output := filepath.Join(dir, "model.json")
	_ = os.Remove(output)
	args := []string{"--console=plain", "--no-configuration-cache", "--init-script", script, "jmanExportModel"}
	var config Config
	if b, e := os.ReadFile(filepath.Join(root, ".jman.json")); e == nil {
		if e = json.Unmarshal(b, &config); e != nil {
			return nil, e
		}
	}
	if config.Offline {
		args = append(args, "--offline")
	}
	cmd := gradleCommand(ctx, root, args...)
	cmd.Env = append(os.Environ(), "JMAN_MODEL_PATH="+output)
	if config.GradleJavaHome != "" {
		cmd.Env = append(cmd.Env, "JAVA_HOME="+config.GradleJavaHome)
	}
	log, e := os.Create(filepath.Join(dir, "gradle.log"))
	if e != nil {
		return nil, e
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Run(); e != nil {
		return nil, fmt.Errorf("Gradle model export failed: %w; inspect %s", e, log.Name())
	}
	b, e := os.ReadFile(output)
	if e != nil {
		return nil, e
	}
	var model BuildModel
	e = json.Unmarshal(b, &model)
	return &model, e
}
func (m *BuildModel) context(file string) *BuildContext {
	if m == nil {
		return nil
	}
	var best *BuildContext
	length := 0
	for i := range m.Contexts {
		c := &m.Contexts[i]
		for _, root := range c.Roots {
			if strings.HasPrefix(file, root+string(os.PathSeparator)) && len(root) > length {
				best = c
				length = len(root)
			}
		}
	}
	return best
}
func modelSnapshot(model *BuildModel) (string, error) {
	if model == nil {
		return "", nil
	}
	b, _ := json.Marshal(model)
	var inputs strings.Builder
	inputs.Write(b)
	seen := map[string]bool{}
	for _, c := range model.Contexts {
		for _, a := range c.Artifacts {
			// Project dependencies are bound to imported source; their not-yet-built
			// output JARs are not a prerequisite for navigation.
			if a.ProjectComponent {
				continue
			}
			if seen[a.Path] {
				continue
			}
			seen[a.Path] = true
			info, e := os.Stat(a.Path)
			if e != nil {
				return "", e
			}
			if info.IsDir() {
				continue
			}
			hash, e := fileDigest(a.Path)
			if e != nil {
				return "", e
			}
			inputs.WriteString(hash)
		}
	}
	return digest([]byte(inputs.String())), nil
}
func Generate(ctx context.Context, root string) error {
	b, e := os.ReadFile(filepath.Join(root, ".jman.json"))
	if e != nil {
		return e
	}
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		return e
	}
	if len(c.GenerateTasks) == 0 {
		return fmt.Errorf("set generateTasks in .jman.json before using --generate")
	}
	args := append([]string{"--console=plain"}, c.GenerateTasks...)
	if c.Offline {
		args = append(args, "--offline")
	}
	cmd := gradleCommand(ctx, root, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if c.GradleJavaHome != "" {
		cmd.Env = append(os.Environ(), "JAVA_HOME="+c.GradleJavaHome)
	}
	return cmd.Run()
}
