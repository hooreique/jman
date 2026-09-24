# Validation record

Last updated: 2026-09-24. Runtime versions and fixture artifacts are fixed by `flake.lock` and Nix hashes.

## Repeatable checks

```sh
nix develop --command go test -race ./...
nix develop --command go vet ./...
nix flake check --no-write-lock-file --print-build-logs
nix build .#jman .#jman-bench
```

The flake checks include Go race tests and explicit vet checks, the packaged benchmark CLI smoke check, benchmark-runner tests, and the integration suite. The local/CI command and platform matrix are documented in [CONTRIBUTING.md](../CONTRIBUTING.md).

The integration suite runs JDTLS 1.60.0, Java 21, Gradle 8.14.4, and the jman Java extension. Gradle runs offline against Nix-fixed fixture dependencies.

It verifies:

- selected JAR binding over a stale same-FQN source;
- semantic references, disk edits, add/delete updates, pagination, and stale-cursor rejection;
- Gradle version constraints, test source sets, composite substitution, missing sources, and separate Git worktrees;
- Lombok getter/builder bindings, MapStruct implementations, QueryDSL generated types, and Spring AOP self-invocation;
- benchmark isolation, custom suites, evaluator timeouts, missing usage, and provider usage accounting.

The Spring fixture proves runtime behavior; it does not make static references a proxy-runtime analyzer.

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
