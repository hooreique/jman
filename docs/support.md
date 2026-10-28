# Support boundary

This document defines shipped behavior for 0.1. Product goals live in [requirements](requirements.md); tested evidence lives in [validation](validation.md).

## Shipped capabilities

| Area | Support |
|---|---|
| Interface | CLI, schema version 1 JSON, skill installer |
| Service | Local Unix-socket daemon, lazy startup, bounded sessions, idle eviction, Home Manager module |
| Navigation | Definition, references, implementations, hover, source read, dependency context |
| Provenance | JDT `codeSelect`, selected classpath binary, digest, Gradle component and variant |
| Freshness | Content fingerprints, watched-file updates, document/index barrier, snapshot-bound cursors |
| Generated code | Lombok agent, generated-member ownership, MapStruct and QueryDSL sources |
| Packaging | Nix flake, locked runtime, fixed integration-fixture artifacts |
| Benchmarking | Paired arms, isolated repositories/sessions/caches, independent evaluator, raw usage when available |

Content fingerprints favor correctness over speed. They can be expensive in large workspaces.

On macOS (kqueue) and Linux (inotify), native watchers track workspace changes.
Gradle-declared Java source roots override directory exclusions, including roots created after import.
Selected external artifact metadata is checked on every query; changed artifacts
are content-hashed and restart the session when their identity changes. Build
inputs changing during sync trigger a bounded model/session reload rather than
committing the new inputs against an old model. `refresh` forces revalidation.

## Memory and session controls

| Setting | Default | Scope |
|---|---|---|
| `jman daemon --max-sessions` | `5` | Maximum resident workspace sessions |
| `jman daemon --idle-timeout` | `15m` | Idle retention; minimum `1s` |
| `.jman.json` → `jdtlsMaxHeapMiB` | `1536` | Project JDTLS maximum heap in MiB; integer ≥ `128`, matching the fixed initial heap |

At capacity, the daemon replaces the least recently used non-busy session;
requests wait within their deadline if all sessions are busy. Idle eviction
also protects busy sessions. Its sweep adds up to one minute, or one second
when the configured timeout is shorter than one minute.

Home Manager exposes `services.jman.maxSessions` and `services.jman.idleTimeout`.
Automatic startup uses the defaults; restart the daemon to change startup
options. A project heap change takes effect on its next query or `refresh`.

Idle eviction, session replacement, and `stop` use normal LSP shutdown to preserve
JDTLS's disk workspace. `stop` waits for session shutdown and daemon lock release,
so the next command can safely start a new daemon. After a clean stop, unchanged
files reuse the saved content snapshot without replaying every file as created.
The next query still validates current inputs and waits for build/index readiness;
it does not require a preparatory `prepare` command. `prepare --json` exposes
`context.resumed` when a compatible clean checkpoint was restored. Missing or
unclean state takes the conservative synchronization path.

`--timings --json` includes `total` (including session retries), `modelLoad`,
`artifactValidation`, `serverStart`, `resumeScan` when applicable, and `sync`.
`workspaceRefresh`, `buildWait`, and `indexWait` break down work inside the sync
extension and are nested within `sync`, not additional elapsed time. JDTLS can
also wait for the index before invoking the extension.

`status --json` reports `context.daemonPid`, `context.maxSessions`, and
`context.idleTimeout`; session `detail` contains `pid` (zero without a running
JVM) and the effective `jdtlsMaxHeapMiB`.

Heap limits exclude JVM native memory and Gradle. Gradle daemons can outlive
jman; `./gradlew --stop` with the matching `GRADLE_USER_HOME` also stops daemons
shared with other projects.

### RAM planning guidance

Starting budgets for projects similar to the
[measured RxJava workload](../reports/2026-09-28-memory-worktrees-report.md):

| `--max-sessions` | Service memory allowance | Installed RAM guidance |
|---:|---|---|
| 1 | 4 GiB; verified with swap disabled | 8 GB starting point; 16 GB recommended |
| 3 | 10 GiB estimate; no capped trial | 16 GB or more |
| 5 | 16 GiB; verified with swap disabled | 32 GB or more |

Installed RAM values are planning estimates allowing room for the OS and other
applications, not tested machine minimums. Measure your workload before reducing
its budget. Cold imports can exceed the default 120-second request deadline;
use a longer `--timeout` when needed.

## Cache paths and cleanup

All supported platforms, including macOS, use these priorities (empty variables are ignored):

- Cache: `JMAN_CACHE_HOME` → `$XDG_CACHE_HOME/jman` → `$HOME/.cache/jman`; without a home directory, `<system temp>/jman`.
- Socket: `JMAN_SOCKET` → `$XDG_RUNTIME_DIR/jman.sock` → `<cache>/run/jman.sock`.

Use the same environment for the daemon and CLI. To clear the default cache, stop any Home Manager service first, then run:

```sh
jman stop
rm -rf ~/.cache/jman
```

If overridden, remove the configured cache instead. This leaves Gradle caches and runtime files outside that directory untouched.

On macOS, stop the old daemon before upgrading. The previous `~/Library/Caches/jman` cache is not migrated or deleted automatically; remove it manually when no longer needed.

## What results mean

- Selecting a binary does not prove that an attached sources JAR came from the same build.
- `implementations` returns static type candidates; it does not select a Spring runtime bean.
- Build-model or binding failures produce `partial` or error results, not a successful empty answer.
- References cover the imported workspace, not unknown downstream repositories.
- A compiler/runtime fixture or scripted adapter does not prove an LLM cost reduction.
- Caller-source-set errors and source sets whose outputs are on the caller's
  Gradle-selected compile classpath are critical (including selected test variants);
  other mapped errors are advisory and unmapped errors remain visible as unknown.
  `--strict-workspace` makes every workspace error critical.

## Not supported

- MCP, natural-language queries, and unsaved editor buffers.
- References across independently registered repositories.
- A verified artifact-to-commit source-provenance registry.
- Spring conditional-bean, profile, pointcut, or runtime-trace analysis.
- SQL-level JPA analysis.
- Compatibility guarantees for arbitrary annotation processors.
- Dynamic memory sizing, a process-wide RSS cap, or cache garbage collection.
- An adversarial benchmark sandbox or statistically conclusive multi-task results.

## Exit codes

| Code | Meaning |
|---:|---|
| 0 | Completed query (`ok` or ready `not-found`) |
| 2 | Invalid or ambiguous CLI input |
| 3 | `partial` or `not-ready` result |
| 4 | Protocol or execution failure |

## Output controls

Default text summarizes workspace health, build counts, caller context, and
diagnostic counts instead of dumping the Gradle model. `doctor` lists actionable
problems; `deps` lists dependency coordinates. `--limit` (default 20) bounds
navigation pages and displayed summary entries (problems, artifacts, sessions).
Omitted entries include a count and instructions for viewing more.

`--max-bytes` (default 8192) bounds default text as well as source excerpts.
Warnings and coverage precede results; truncation keeps the response status and
next-page cursor. To see cut-off results from the current page, rerun with a
larger budget before advancing the cursor. Very long summary fields are clipped
to 512 bytes; full values remain available in detailed output.

`--explain` renders full text details, including build model, settings, provenance,
and coverage. `--json` preserves the full schema-1 response for machine readers.
Neither has a total-response byte limit; large builds can produce megabytes.
For tool-assisted exploration prefer default text, or redirect full JSON to a
file and select the needed fields. Source excerpts remain byte-bounded in both modes.

## Benchmark suite contract

Use `jman-bench run --suite /path/to/suite.json` to supply a project and task.

```json
{
  "template": "./template",
  "project": "application",
  "repositories": ["application", "shared-library"],
  "prompt": "Describe the task and required change.",
  "allowedEdits": ["src/main/java/*"],
  "prepare": ["./gradlew", "classes"],
  "evaluate": ["python3", "{suite}/evaluate.py"],
  "explanationTerms": ["expected-artifact", "expected-version"]
}
```

The template path is relative to the manifest. `project` must be an included repository. The runner creates fresh repositories without copied `.git`, `.gradle`, or build output. After the agent exits, it applies only allowed edits to a fresh evaluation workspace. `{suite}` resolves to the manifest directory, keeping evaluator code outside the agent workspace. A zero evaluator exit code passes.

## Adapter contract

An `--adapter-command` reads JSON from `JMAN_BENCH_INPUT`:

```json
{
  "schemaVersion": 1,
  "project": "<absolute-project-path>",
  "prompt": "...",
  "deadlineSeconds": 600,
  "maxTokens": 200000,
  "model": "model-id",
  "output": "<output-json-path>"
}
```

It writes the requested output file:

```json
{
  "reason": "completed",
  "answer": "...",
  "usage": {"input": 12000, "output": 1500, "cacheRead": 2000, "reasoning": 500},
  "usageSource": "provider-or-harness-name"
}
```

Use `null` when usage is unavailable. `input` includes `cacheRead`; `output` includes `reasoning`. The adapter must retain raw provider data and normalize its accounting. Prices are USD per million tokens:

```json
{"input": 1.0, "output": 4.0, "cacheRead": 0.1}
```

The built-in runner removes its API key from shell-tool environments. External adapters receive the environment needed to call their provider and must isolate credentials from tool environments and logs.
