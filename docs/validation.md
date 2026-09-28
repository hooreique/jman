# Validation record

Last updated: 2026-09-28. Runtime versions and fixture artifacts are fixed by `flake.lock` and Nix hashes.

## Repeatable checks

```sh
nix develop --command go test -race ./...
nix develop --command go vet ./...
nix flake check --no-write-lock-file --print-build-logs
nix build .#jman .#jman-bench
```

The integration suite runs JDTLS 1.60.0, Java 21, Gradle 8.14.4, and the jman Java extension. Gradle runs offline against Nix-fixed fixture dependencies.

It verifies:

- selected JAR binding over a stale same-FQN source;
- semantic references, disk edits, add/delete updates, pagination, and stale-cursor rejection;
- Gradle version constraints, test source sets, composite substitution, missing sources, and separate Git worktrees;
- Lombok getter/builder bindings, MapStruct implementations, QueryDSL generated types, and Spring AOP self-invocation;
- benchmark isolation, custom suites, evaluator timeouts, missing usage, and provider usage accounting.

The Spring fixture proves runtime behavior; it does not make static references a proxy-runtime analyzer.

## Resource controls and profiling

The implementation in `3df104c` passed Go race tests and vet, local real
JDTLS/Gradle integration, and `nix flake check`. The final collector and runner
tests passed 12 and 8 cases respectively, including missing metrics, detached
and shared Gradle processes, PID reuse, daemon replacement, phase accounting,
real Git worktree isolation, and cleanup failures. The real integration suite
also verifies a configured heap and its effective PID/settings after restart.

[CI run 36362053903](https://github.com/hooreique/jman/actions/runs/36362053903)
passed `nix flake check` on x86_64-linux, aarch64-linux, and aarch64-darwin.
This includes platform-native process sampling during the runner tests; it is
not a cross-platform performance or memory-capacity study.

The [worktree memory report](../reports/2026-09-28-memory-worktrees-report.md)
records the pinned large-project workload, measured operating envelope,
first-navigation costs, and the limits of system-sizing guidance. Raw data is
retained outside Git under the report's documented experiment directory.
All 18 concurrent/sequential matrix trials passed, covering 648 navigation
commands across 1/3/5 active worktrees. Four additional trials passed: one and
five sessions under 4/16 GiB no-swap cgroup limits, refresh/eviction/idle
lifecycle checks, and a one-session 1024 MiB heap configuration. All 22 trials
ended with zero residual tracked processes after cleanup.

## Observed agent trials

Real OpenCode trials used `openai/gpt-5.6-terra`, three repetitions per arm, and independent evaluators. They are small, task-specific observations, not general performance claims.

| Trial | Baseline | jman | Report |
|---|---:|---:|---|
| Origin navigation | 3/3 | 3/3 | [report](../reports/2026-09-24-opencode-jdtls-origin-navigation-report.md) |
| Test/main classpath split | 0/3 | 3/3 | [report](../reports/2026-09-24-test-main-classpath-split-report.md) |
| Dependency version conflict | 3/3 | 3/3 | [report](../reports/2026-09-24-dependency-version-conflict-report.md) |
| Composite build substitution | 3/3 | 2/3 | [report](../reports/2026-09-24-composite-build-substitution-report.md) |
| Lombok generated member | 3/3 | 3/3 | [report](../reports/2026-09-24-lombok-generated-member-report.md) |
| MapStruct generated implementation | 3/3 | 3/3 | [report](../reports/2026-09-24-mapstruct-generated-implementation-report.md) |

OpenCode reported zero monetary cost in these trials. Reports therefore show tokens and wall time, not currency cost. Raw event paths are documented in each report and are not committed.

## Run another trial

```sh
OPENAI_API_KEY=... nix run .#bench -- run \
  --model YOUR_MODEL \
  --output ./local/real-experiment \
  --repetitions 5 \
  --cache dependency-warm
```

Use `--base-url`, `--api-key-env`, or `--adapter-command` for another provider or harness. Run cold and warm modes separately and retain failed runs with raw events.
