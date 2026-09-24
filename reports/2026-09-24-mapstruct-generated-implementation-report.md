# MapStruct Generated Implementation Trial

- **Date:** 2026-09-24
- **Trial:** `mapstruct-generated-implementation`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266`
- **Harness:** OpenCode 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **Model:** `openai/gpt-5.6-terra`
- **Runs:** three baseline and three jman sessions

## Task and method

`PersonDto` changed to `displayName`. Repair the source MapStruct mapping so the generated mapper preserves the name; generated output is out of bounds.

Each run used a fresh fixture repository, configuration directory, and standalone OpenCode session with the same prompt and `--auto` policy. Both arms had JDK 21 and Gradle 8.14.4 on `PATH`; only jman had the packaged command and skill. Build scripts, dependency artifacts, libraries, and generated output could not change.

Totals sum OpenCode `step_finish.tokens`. `reported total = input + output + cache-read`; `non-cache total = input + output`. Wall time spans the first and final event. `verifyStack` was the independent evaluator.

## Results

Independent evaluator: **baseline 3/3; jman 3/3**.

| Metric | Baseline | jman | Change |
|---|---:|---:|---:|
| Non-cache input tokens | 29,216 | 46,237 | +58.3% |
| Output tokens | 1,232 | 1,969 | +59.8% |
| Cache-read tokens | 71,680 | 146,432 | +104.3% |
| Reported total tokens | 102,128 | 194,638 | +90.6% |
| Non-cache total tokens | 30,448 | 48,206 | +58.3% |
| Wall time | 57.639s | 126.285s | +119.1% |
| Tool calls | 33 | 47 | +42.4% |

| Repetition | Baseline tokens | jman tokens | Baseline time | jman time |
|---:|---:|---:|---:|---:|
| 1 | 26,927 | 43,085 | 18.361s | 35.650s |
| 2 | 41,291 | 51,313 | 21.015s | 38.182s |
| 3 | 33,910 | 100,240 | 18.263s | 52.453s |

## Limits

All runs passed, but this is one task with three repetitions. It does not establish general performance. OpenCode reported zero step cost; no currency cost was calculated. Raw JSONL and aggregate data remain outside the repository at `/tmp/opencode/jman-additional-real/`.
