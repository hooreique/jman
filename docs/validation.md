# Validation record

Last updated: 2026-10-16. Runtime versions and fixture artifacts are fixed by `flake.lock` and Nix hashes.

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

## macOS optimization stabilization

On aarch64-darwin, Go race tests and vet passed. The targeted watcher/cache,
source-set, composite-snapshot, status-concurrency, and canonical-path regression
tests also passed ten consecutive race-detector runs. The existing project-root
test now compares canonical paths, including macOS's `/tmp` symlink.

`nix flake check --no-write-lock-file --print-build-logs` completed successfully,
including race/vet, real JDTLS source-set/composite regressions, benchmark tests,
and package checks. Linux checks were deliberately not run. Nix emitted a
non-fatal `audit-tmpdir.sh` fixup-hook segmentation fault while building the
extension; the build and all checks still completed with exit status zero.
This is a packaging-tool caveat, not a silently omitted test failure.

The [controlled large-workspace warm trial](../reports/2026-10-16-macos-warm-navigation-report.md)
records 60 successful cache-hit navigation requests on 6,003 Java files. It is
synthetic, not a representative workspace or Linux latency measurement.

The subsequent [freshness regression checks](../reports/2026-10-16-macos-freshness-workspace-report.md)
verify external JAR changes and declared excluded source roots against forced
refresh. Non-public workspace measurements are omitted. Additional Go
race regressions exercise mid-sync model reload, selected variant/classpath
classification, and native watching of initially missing source roots.

## optimize completion — 2026-10-16

- Validated code revision: `02a6eeb` (final code tested before commit; subsequent
  changes only update documentation and remove completed checklists).
- Environment: aarch64-darwin, repository-pinned Nix/Java 21/Gradle 8.14.4/JDTLS
  1.60.0. Linux runtime checks were not run.
- Targeted Go scan/cache/watcher/recovery/concurrency regressions passed. Real
  Gradle/JDTLS regressions passed for both a `reports` project root and a
  `:reports` subproject: selecting existing JAR B instead of A automatically
  changed the definition and snapshot, matching forced refresh.
- `nix flake check --no-write-lock-file --print-build-logs` passed, including
  race/vet, integration compile/runtime oracles, diagnostics, resource/status,
  benchmark, and package checks. Existing unrelated-output exclusions passed.
- The integration runner's settled warm smoke passed: three consecutive
  `prepare` cache hits with `dirty: false`, followed by three each of definition,
  references, and implementations for `PersonMapper.toDto`. All nine retained
  expected results, snapshot, warnings and coverage, with `scan: 0`, `sync: 0`.
- Environment workaround: canonical `TMPDIR` for the targeted integration run.
  The final flake check needed no workaround or cleanup retry.

## Resource controls and profiling

Implementation `3df104c` passed Go race tests, vet, real JDTLS/Gradle integration,
and 20 collector/runner tests. [CI](https://github.com/hooreique/jman/actions/runs/36370361049)
passed `nix flake check` on x86_64-linux, aarch64-linux, and aarch64-darwin,
including native process sampling and configured-heap verification.

The [RxJava memory study](../reports/2026-09-28-memory-worktrees-report.md)
records 22 successful Linux x86_64 trials covering concurrent worktrees,
constrained memory, session lifecycle, and heap configuration.

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
