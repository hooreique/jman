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

func TestFilesChanged(t *testing.T) {
	base := map[string]string{"A.java": "one", "build.gradle": "two"}
	if filesChanged(base, map[string]string{"A.java": "one", "build.gradle": "two"}) {
		t.Fatal("identical snapshots must not require sync")
	}
	if !filesChanged(base, map[string]string{"A.java": "changed", "build.gradle": "two"}) {
		t.Fatal("content rewrite must require sync")
	}
	if !filesChanged(base, map[string]string{"A.java": "one"}) {
		t.Fatal("deletion must require sync")
	}
}

func TestHeapUsageParser(t *testing.T) {
	match := heapUsage.FindStringSubmatch(" garbage-first heap   total 1048576K, used 524288K [0x0, 0x1)")
	if len(match) != 3 || match[1] != "1048576" || match[2] != "524288" {
		t.Fatalf("unexpected heap match: %v", match)
	}
}

func fakeResourceCommands(t *testing.T, ps, jcmd string) string {
	t.Helper()
	dir := t.TempDir()
	for name, script := range map[string]string{"ps": ps, "jcmd": jcmd} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = --warmup ]; then exit 0; fi\n"+script+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		// macOS may inspect a newly created executable on its first launch.
		// Keep that unrelated cold-start cost outside the 50ms request budget.
		if err := exec.Command(path, "--warmup").Run(); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestResourcesRespectDeadline(t *testing.T) {
	for _, slow := range []string{"ps", "jcmd"} {
		t.Run(slow, func(t *testing.T) {
			ps := "echo 123"
			jcmd := "echo 'garbage-first heap total 456K, used 78K'"
			if slow == "ps" {
				ps = "sleep 1.5\n" + ps
			} else {
				// The shell and its child both inherit the output pipe.
				jcmd = "sleep 1.5\n" + jcmd
			}
			fakeResourceCommands(t, ps, jcmd)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			resources := jdtResources(ctx, 1234)
			if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
				t.Fatalf("%s ignored deadline: %v", slow, elapsed)
			}
			if resources["heapUsedKiB"] != nil || resources["heapTotalKiB"] != nil {
				t.Fatalf("timed-out heap metrics should be unavailable: %v", resources)
			}
			if slow == "jcmd" && resources["rssKiB"] != 123 {
				t.Fatalf("successful RSS metric was lost: %v", resources)
			}
		})
	}
}

func TestResourcesSuccessfulAndUnavailable(t *testing.T) {
	fakeResourceCommands(t, "echo 123", "echo 'garbage-first heap total 456K, used 78K'")
	resources := jdtResources(context.Background(), 1234)
	if resources["rssKiB"] != 123 || resources["heapTotalKiB"] != 456 || resources["heapUsedKiB"] != 78 {
		t.Fatalf("unexpected metrics: %v", resources)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, resources := range []map[string]any{jdtResources(ctx, 1234), jdtResources(context.Background(), 0)} {
		for _, value := range resources {
			if value != nil {
				t.Fatalf("unavailable metrics must be null: %v", resources)
			}
		}
	}
}

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
			status := s.status(ctx)
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
			if status := s.status(ctx); status["pid"] != 0 || status["state"] != "stopped" {
				t.Fatalf("process status retained after stop: %v", status)
			}
		})
	}
}
