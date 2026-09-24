# Composite Build Substitution Trial

- **Date:** 2026-09-24
- **Trial:** `composite-build-substitution`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266`
- **Harness:** OpenCode 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **Model:** `openai/gpt-5.6-terra`
- **Runs:** three baseline and three jman sessions

## Task and method

A Gradle composite build substituted `com.acme:shared-text` with `../internal-text`. Repair `AccessService.isAdmin` using the source selected by the composite build.

Each run used a fresh fixture repository, configuration directory, and standalone OpenCode session with the same prompt and `--auto` policy. Both arms had JDK 21 and Gradle 8.14.4 on `PATH`; only jman had the packaged command and skill. Build scripts, dependency artifacts, libraries, and generated output could not change.

Totals sum OpenCode `step_finish.tokens`. `reported total = input + output + cache-read`; `non-cache total = input + output`. Wall time spans the first and final event. The evaluator compiled `AccessService` against the selected binary and tested spaced, uppercase, Unicode-whitespace admin names plus two non-admin cases.

## Results

Independent evaluator: **baseline 3/3; jman 2/3**.

| Metric | Baseline | jman | Change |
|---|---:|---:|---:|
| Non-cache input tokens | 30,152 | 36,496 | +21.0% |
| Output tokens | 2,385 | 2,276 | -4.6% |
| Cache-read tokens | 106,496 | 134,656 | +26.4% |
| Reported total tokens | 139,033 | 173,428 | +24.7% |
| Non-cache total tokens | 32,537 | 38,772 | +19.2% |
| Wall time | 102.170s | 189.099s | +85.1% |
| Tool calls | 52 | 50 | -3.8% |

| Repetition | Baseline | jman | Baseline tokens | jman tokens | Baseline time | jman time |
|---:|---|---|---:|---:|---:|---:|
| 1 | pass | pass | 42,595 | 49,069 | 33.321s | 59.867s |
| 2 | pass | fail | 40,466 | 70,803 | 36.418s | 50.881s |
| 3 | pass | pass | 55,972 | 53,556 | 32.431s | 78.351s |

## Limits

The failed jman run remains in totals. This is one task with three repetitions, not a general Java or model result. OpenCode reported zero step cost, so currency cost was not calculated. Raw JSONL and aggregate data were kept under `/tmp/opencode/jman-additional-real/` and are not committed.
