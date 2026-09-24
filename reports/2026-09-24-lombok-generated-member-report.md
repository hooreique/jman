# Lombok Generated Member Trial

- **Date:** 2026-09-24
- **Trial:** `lombok-generated-member`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266`
- **Harness:** OpenCode 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **Model:** `openai/gpt-5.6-terra`
- **Runs:** three baseline and three jman sessions

## Task and method

A Lombok `Person.builder()` call omitted `name`. Repair tracked source so `StackCheck` receives the expected person; generated output is out of bounds.

Each run used a fresh fixture repository, configuration directory, and standalone OpenCode session with the same prompt and `--auto` policy. Both arms had JDK 21 and Gradle 8.14.4 on `PATH`; only jman had the packaged command and skill. Build scripts, dependency artifacts, libraries, and generated output could not change.

Totals sum OpenCode `step_finish.tokens`. `reported total = input + output + cache-read`; `non-cache total = input + output`. Wall time spans the first and final event. `verifyStack` was the independent evaluator.

## Results

Independent evaluator: **baseline 3/3; jman 3/3**.

| Metric | Baseline | jman | Change |
|---|---:|---:|---:|
| Non-cache input tokens | 28,661 | 40,068 | +39.8% |
| Output tokens | 1,091 | 2,075 | +90.2% |
| Cache-read tokens | 71,680 | 156,160 | +117.9% |
| Reported total tokens | 101,432 | 198,303 | +95.5% |
| Non-cache total tokens | 29,752 | 42,143 | +41.6% |
| Wall time | 64.320s | 139.916s | +117.5% |
| Tool calls | 35 | 50 | +42.9% |

| Repetition | Baseline tokens | jman tokens | Baseline time | jman time |
|---:|---:|---:|---:|---:|
| 1 | 33,627 | 62,405 | 20.226s | 39.882s |
| 2 | 33,440 | 72,865 | 22.562s | 47.145s |
| 3 | 34,365 | 63,033 | 21.532s | 52.889s |

## Limits

All runs passed, but this is one task with three repetitions. It does not establish general performance. OpenCode reported zero step cost; no currency cost was calculated.
