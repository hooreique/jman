# Test/Main Classpath Split Trial

- **Date:** 2026-09-24
- **Trial:** `test-main-classpath-split`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266`
- **Harness:** OpenCode 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **Model:** `openai/gpt-5.6-terra`
- **Runs:** three baseline and three jman sessions

## Task

Repair test-only `TestRules.isAdminFixture` using the test source-set classpath. Main source, build scripts, and legacy source were out of bounds.

## Method

Every arm and repetition used a fresh fixture repository, `OPENCODE_CONFIG_DIR`, and standalone OpenCode session with the same prompt and `--auto` policy. Both arms had JDK 21 and Gradle 8.14.4 on `PATH`; only jman had the packaged command and skill. Changes to build scripts, dependency artifacts, libraries, and generated output were forbidden.

Totals sum OpenCode `step_finish.tokens`. `reported total = input + output + cache-read`; `non-cache total = input + output`. Wall time runs from the first to last event timestamp. An independent evaluator, not the final answer, decides success.

## Results

Independent evaluator: **baseline 0/3; jman 3/3**.

| Metric | Baseline | jman | Change |
|---|---:|---:|---:|
| Non-cache input tokens | 38,406 | 50,539 | +31.6% |
| Output tokens | 3,234 | 2,806 | -13.2% |
| Cache-read tokens | 113,152 | 107,008 | -5.4% |
| Reported total tokens | 154,792 | 160,353 | +3.6% |
| Non-cache total tokens | 41,640 | 53,345 | +28.1% |
| Wall time | 146.000s | 206.211s | +41.2% |
| Tool calls | 54 | 50 | -7.4% |

| Repetition | Baseline | jman | Baseline tokens | jman tokens | Baseline time | jman time |
|---:|---|---|---:|---:|---:|---:|
| 1 | fail | pass | 50,555 | 43,968 | 44.991s | 64.204s |
| 2 | fail | pass | 41,575 | 63,831 | 38.395s | 81.640s |
| 3 | fail | pass | 62,662 | 52,554 | 62.614s | 60.367s |

The evaluator ran `:app:testClasses` and required `equals("ADMIN")` in the repaired test-support source.

## Limits

All failures remain in the totals. This is one task with three repetitions, not a general Java or model result. OpenCode reported zero step cost, so currency cost was not calculated.
