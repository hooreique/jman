# Dependency Version Conflict Trial

- **Date:** 2026-09-24
- **Trial:** `dependency-version-conflict`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266`
- **Harness:** OpenCode 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **Model:** `openai/gpt-5.6-terra`
- **Runs:** three baseline and three jman sessions

## Task and method

A direct declaration requested `com.acme:shared-text:1.0.0`, while a Gradle constraint selected `2.4.1`. Repair `AccessService.isAdmin` without consulting the same-FQN legacy source.

Each run used a fresh fixture repository, configuration directory, and standalone OpenCode session with the same prompt and `--auto` policy. Both arms had JDK 21 and Gradle 8.14.4 on `PATH`; only jman had the packaged command and skill. Build scripts, dependency artifacts, libraries, and generated output could not change.

Totals sum OpenCode `step_finish.tokens`. `reported total = input + output + cache-read`; `non-cache total = input + output`. Wall time spans the first and final event. The evaluator compiled `AccessService` against the selected binary and tested spaced, uppercase, Unicode-whitespace admin names plus two non-admin cases.

## Results

Independent evaluator: **baseline 3/3; jman 3/3**.

| Metric | Baseline | jman | Change |
|---|---:|---:|---:|
| Non-cache input tokens | 39,630 | 28,790 | -27.4% |
| Output tokens | 3,222 | 1,854 | -42.5% |
| Cache-read tokens | 142,336 | 99,328 | -30.2% |
| Reported total tokens | 185,188 | 129,972 | -29.8% |
| Non-cache total tokens | 42,852 | 30,644 | -28.5% |
| Wall time | 123.305s | 159.245s | +29.1% |
| Tool calls | 57 | 36 | -36.8% |

| Repetition | Baseline tokens | jman tokens | Baseline time | jman time |
|---:|---:|---:|---:|---:|
| 1 | 61,255 | 45,717 | 40.675s | 55.890s |
| 2 | 73,036 | 42,826 | 43.704s | 52.508s |
| 3 | 50,897 | 41,429 | 38.926s | 50.847s |

## Limits

All runs passed, but this is one task with three repetitions. It does not establish general performance. OpenCode reported zero step cost; no currency cost was calculated. Raw JSONL and aggregate data remain outside the repository at `/tmp/opencode/jman-additional-real/`.
