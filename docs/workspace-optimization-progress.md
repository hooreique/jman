# Workspace optimization progress

This file is a resumable implementation log for the workspace-performance plan.

## 2026-10-16 — macOS stabilization

- Failed scan/import/sync attempts invalidate the cache and preserve a full-scan
  retry; watcher registration precedes scans of newly discovered roots.
- Full and incremental composite scans hash the same merged, sorted file map.
- Caller test/custom-source-set errors and their project compile dependencies
  participate in critical diagnostics; unrelated source sets remain advisory.
- Session status fields and watcher replacement now share the session mutex.
- macOS vnode replacement/deletion triggers watcher rebuilding; idle and
  backpressured native shutdown is bounded. Watch and scan exclusions agree.
- Regression evidence is recorded in [validation.md](validation.md). Linux
  validation and further operator-specific measurements are outside this
  branch's macOS completion criteria.

## 2026-09-29 — P0 instrumentation and P1 complete (commit `5550f4a`)

- Added `--json --timings`, reporting `scan`, change detection, sync, document
  update, binding resolution, LSP, and rendering elapsed milliseconds when those
  stages run.
- Added `bench/manual_navigation.sh`. It records three samples for each supplied
  navigation command, daemon status, JDTLS RSS, and heap information when `jcmd`
  is available. The benchmark target and commands are intentionally supplied by
  the operator rather than embedded in this repository.
- P1: a session now retains the successful sync problems, project roots,
  snapshot, and time. A query with an unchanged scan skips `jman.sync`; `refresh`,
  session startup, build changes, and detected input changes still take the full
  sync path.
- The target-workspace cold/warm measurement has not yet been recorded. It is an
  operator-run follow-up because its source locations and daemon lifecycle are
  environment-specific; the checked-in script accepts those inputs explicitly.

## Next checkpoint

1. Exercise two identical navigation requests with `--json --timings` and verify
   the second reports `sync: 0`.
2. Record a benchmark on an operator-selected large Gradle workspace.
3. Continue with P2 incremental change detection; the current P1 path still
   hashes the workspace on every request.

## Verification record

- `nix develop --command go test ./internal/jman -run
  'TestFilesChanged|TestSessionHeapLaunchAndPIDLifecycle' -count=1` passed.
- `nix develop --command go test ./...` did not complete because the pre-existing
  `TestProjectBoundary` fails in the Nix temporary-shell path. This is unrelated
  to the changed code and remains to be reproduced outside that environment.

## 2026-09-29 — P2 incremental detection core complete

- Added a recursive native filesystem watcher for workspace and discovered extra
  roots on the supported desktop platform. Other platforms retain the safe
  fingerprint/full-scan fallback until their native watcher is implemented.
  Ordinary tracked-file writes hash only the affected path; creation or removal
  of directories, watcher errors, and overflow-equivalent errors safely promote
  the next request to a full scan.
- Added a lightweight fingerprint for Gradle settings and Git metadata. Linked
  worktree `.git` files are followed, so HEAD/index changes invalidate cached
  state even if a filesystem event is missed.
- Changed warm requests to reuse the previous scan and artifact snapshot. A
  complete scan and artifact verification still run at startup, explicit refresh,
  watcher fallback, and at least every five minutes.
- `finish` now uses the watcher to detect a concurrent workspace change rather
  than reading and hashing the full workspace after each navigation request.
- Dirty filesystem events are coalesced for 75ms before synchronisation, while
  unchanged requests incur no debounce delay.

## Next checkpoint

1. Add integration coverage proving a second warm request avoids both sync and
   full scan while a saved Java/build/generated-source edit is observed.
2. Measure the target workspace using `bench/manual_navigation.sh`.
3. Begin P3: exclude stale non-model output from scans and classify diagnostics.

## 2026-09-29 — P3 scan exclusions started

- Excluded IDE metadata, legacy `bin` output, test reports, and coverage output
  from jman input scanning. Gradle `build/generated/...` paths remain included.
- Diagnostic critical/advisory classification and strict workspace mode remain
  separate P3 work items.

## 2026-09-29 — P3 diagnostic scope classification

- Problem markers now include their filesystem location. For navigation, errors
  in the caller's mapped main source set are critical; mapped test/other-module
  errors are advisory, and unmapped errors remain visible as unknown advisory
  diagnostics.
- `--strict-workspace` restores the conservative policy by making every marker
  critical. `doctor`, `prepare`, and `refresh` continue to show all markers.
- Dependency-closure classification and definition-target classification remain
  follow-up work; the current implementation deliberately does not claim that
  an advisory error is irrelevant to global reference completeness.

## 2026-09-29 — Target workspace measurement attempted

- The public evidence scope and general follow-up guidance are in
  `reports/2026-09-29-navigation-optimization-report.md`.
- Non-public workspace observations are omitted; this report does not provide
  a public cold/warm p50/p95 baseline or establish a performance result.

## 2026-09-29 — P6 health snapshot

- `reports/2026-09-29-workspace-health-report.md` retains general diagnostic
  guidance only; non-public workspace snapshots are omitted. Workspace health
  follow-up belongs to workspace owners, separate from the navigation fast path.
- P4 workspace import profiles remain blocked: the current Gradle/JDT import
  integration exposes no supported per-project import boundary. A profile must
  not be advertised until it can create an isolated imported workspace and
  demonstrate full-profile-equivalent navigation.

## Final verification

- `nix flake check` passed for the host platform, including unit, integration,
  benchmark, and package checks. Transient cache metadata warnings were emitted
  by Nix during dependency retrieval but did not fail a check.
