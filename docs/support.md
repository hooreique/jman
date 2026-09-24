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

Content fingerprints favor correctness over speed. They can be expensive in large workspaces. The default two-session limit and JVM heap setting are not a process-wide RSS limit.

## What results mean

- Selecting a binary does not prove that an attached sources JAR came from the same build.
- `implementations` returns static type candidates; it does not select a Spring runtime bean.
- Build-model or binding failures produce `partial` or error results, not a successful empty answer.
- References cover the imported workspace, not unknown downstream repositories.
- A compiler/runtime fixture or scripted adapter does not prove an LLM cost reduction.

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

`--limit` bounds result count. `--max-bytes` bounds source excerpts, not the full JSON response.

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
  "project": "/run/workspace/commerce",
  "prompt": "...",
  "deadlineSeconds": 600,
  "maxTokens": 200000,
  "model": "model-id",
  "output": "/run/adapter-result.json"
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
