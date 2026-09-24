# OpenCode JDTLS Origin Navigation Trial

- **Date:** 2026-09-24
- **Trial:** `opencode-jdtls-origin-navigation`
- **jman commit:** `76bb106` (`fix: bound benchmark evaluation and reject edits to auxiliary repositories`)
- **Harness:** OpenCode 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **Model:** `openai/gpt-5.6-terra`
- **Runs:** three baseline and three jman sessions

## Task

The fixture contains a stale `TextUtil` and the Gradle-selected internal-library `TextUtil` with the same FQN. The agent had to repair `AccessService.isAdmin(" admin ")`, change only application code, preserve non-admin rejection, and identify the called implementation and dependency version.

After every run, an independent evaluator applied only application-source edits to a clean fixture. It compiled with `javac` and tested spaced, uppercase, Unicode-whitespace admin names, an ordinary user, and `superadmin`.

## Method

Baseline had normal OpenCode file, search, and shell tools without jman or its skill. The jman arm had the same tools plus the packaged command and skill. Every run used a fresh fixture repository, `OPENCODE_CONFIG_DIR`, and standalone server with the same prompt, model, and auto-approved tool policy.

OpenCode reports `input` and `cache.read` separately. `reported total = input + output + cache-read`; `non-cache total = input + output`. Totals sum `step_finish.tokens`; wall time spans the first and final event timestamp.

## Results

Independent evaluator: **baseline 3/3; jman 3/3**.

| Metric | Baseline | jman | Change |
|---|---:|---:|---:|
| Non-cache input tokens | 64,992 | 52,301 | -19.5% |
| Output tokens | 5,926 | 3,431 | -42.1% |
| Cache-read tokens | 230,400 | 227,328 | -1.3% |
| Reported total tokens | 301,318 | 283,060 | -6.1% |
| Non-cache total tokens | 70,918 | 55,732 | -21.4% |
| Wall time | 334.275s | 238.988s | -28.5% |

| Repetition | Baseline tokens | jman tokens | Baseline time | jman time |
|---:|---:|---:|---:|---:|
| 1 | 131,660 | 95,230 | 168.739s | 84.957s |
| 2 | 88,443 | 71,987 | 55.177s | 73.057s |
| 3 | 81,215 | 115,843 | 110.359s | 80.974s |

## Interpretation and limits

In the first baseline run, the agent opened JAR sources and searched Java and Gradle locations. The jman arm obtained `com.acme:shared-text:2.4.1`, a source excerpt, and compile-classpath context from one definition query.

The aggregate favors jman for non-cache tokens and wall time, but trial 3 used more reported tokens with jman. This single task and three repetitions do not establish a general benefit. OpenCode reported zero step cost, so no currency cost is calculated.
