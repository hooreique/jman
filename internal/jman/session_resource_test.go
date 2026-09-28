package jman

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionHeapLaunchAndPIDLifecycle(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		direct bool
		config string
		heap   int
	}{
		{"launcher default", false, `{}`, 1536},
		{"launcher custom", false, `{"jdtlsMaxHeapMiB":2048}`, 2048},
		{"direct default", true, `{}`, 1536},
		{"direct custom", true, `{"jdtlsMaxHeapMiB":2048}`, 2048},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			project := filepath.Join(dir, "project")
			javaHome := filepath.Join(dir, "java")
			jdtlsHome := filepath.Join(dir, "jdtls")
			for _, p := range []string{project, filepath.Join(javaHome, "bin"), filepath.Join(jdtlsHome, "plugins")} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			argsFile := filepath.Join(dir, "args")
			initReply := `{"jsonrpc":"2.0","id":1,"result":{}}`
			ready := `{"jsonrpc":"2.0","method":"language/status","params":{"type":"Started","message":"ready"}}`
			// Wait until the client has sent initialize before replying, then drain its input.
			script := fmt.Sprintf("#!%s\nprintf '%%s\\n' \"$@\" > \"$JMAN_TEST_ARGS\"\nIFS= read -r header\nIFS= read -r blank\nprintf 'Content-Length: %d\\r\\n\\r\\n%%s' '%s'\nprintf 'Content-Length: %d\\r\\n\\r\\n%%s' '%s'\ncat >/dev/null\n", sh, len(initReply), initReply, len(ready), ready)
			launcher := filepath.Join(dir, "launcher")
			for _, p := range []string{launcher, filepath.Join(javaHome, "bin", "java")} {
				if err := os.WriteFile(p, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			jar := filepath.Join(jdtlsHome, "plugins", "org.eclipse.equinox.launcher_test.jar")
			if err := os.WriteFile(jar, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, ".jman.json"), []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("JMAN_TEST_ARGS", argsFile)
			t.Setenv("JMAN_CACHE_HOME", filepath.Join(dir, "cache"))
			t.Setenv("JMAN_JDTLS", launcher)
			t.Setenv("JMAN_JAVA_HOME", javaHome)
			t.Setenv("JMAN_EXTENSION", "test-extension.jar")
			t.Setenv("JMAN_LOMBOK_AGENT", "")
			if tc.direct {
				t.Setenv("JMAN_JDTLS_HOME", jdtlsHome)
			} else {
				t.Setenv("JMAN_JDTLS_HOME", "")
			}
			s := newSession(project)
			defer s.stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.start(ctx); err != nil {
				t.Fatal(err)
			}
			status := s.status()
			if status["pid"].(int) <= 0 || status["jdtlsMaxHeapMiB"] != tc.heap {
				t.Fatalf("unexpected running status: %v", status)
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			prefix := ""
			if !tc.direct {
				prefix = "--jvm-arg="
			}
			for _, want := range []string{prefix + "-Xms128m", fmt.Sprintf("%s-Xmx%dm", prefix, tc.heap)} {
				found := false
				for _, arg := range strings.Split(string(args), "\n") {
					if arg == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %q in launch arguments %q", want, args)
				}
			}
			s.stop()
			if status := s.status(); status["pid"] != 0 || status["state"] != "stopped" {
				t.Fatalf("process status retained after stop: %v", status)
			}
		})
	}
}
