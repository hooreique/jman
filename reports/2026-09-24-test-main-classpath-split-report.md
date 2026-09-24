# Test/Main Classpath Split Trial

- **수행일:** 2026-09-24
- **테스트 이름:** `test-main-classpath-split`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266` (`9b066f3 docs: link validation record to real agent trial`)
- **OpenCode:** 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **LLM:** `openai/gpt-5.6-terra`
- **반복 수:** baseline 3회, jman 3회 (총 6 actual OpenCode sessions)

## 목적과 과제

Repair test-support-only `TestRules.isAdminFixture`, using the test source-set classpath; main source, build scripts, and legacy source were out of bounds.

## 방법

각 arm과 repetition은 새 독립 fixture repository, 별도 `OPENCODE_CONFIG_DIR`, standalone OpenCode session에서 같은 prompt와 `--auto` 정책으로 실행했다. baseline과 jman 모두 JDK 21 및 Gradle 8.14.4를 PATH에서 사용할 수 있게 해 환경 탐색 비용을 통제했다. jman arm에만 packaged `jman` command와 `jman` skill을 추가했다. build script, dependency/library artifact, generated output 수정은 금지했다.

OpenCode JSON event log의 모든 `step_finish.tokens`를 합산했다. `input`과 `cache.read`는 OpenCode가 별도로 보고하므로 분리한다. `reported total`은 input + output + cache-read, `non-cache total`은 input + output이다. 시간은 event log의 첫 timestamp부터 마지막 timestamp까지다. 성공 판정은 agent의 최종 답변이 아니라 독립 evaluator 결과다.

## 결과

독립 evaluator: **baseline 0/3, jman 3/3**.

| Metric | Baseline total | jman total | jman vs baseline |
|---|---:|---:|---:|
| non-cache input tokens | 38,406 | 50,539 | +31.6% |
| output tokens | 3,234 | 2,806 | -13.2% |
| cache-read tokens | 113,152 | 107,008 | -5.4% |
| reported total tokens | 154,792 | 160,353 | +3.6% |
| non-cache total tokens | 41,640 | 53,345 | +28.1% |
| event-log wall time | 146.000s | 206.211s | +41.2% |
| tool calls | 54 | 50 | -7.4% |

### Repetition별 결과

| Repetition | Baseline success | jman success | Baseline reported tokens | jman reported tokens | Baseline time | jman time |
|---|---|---|---:|---:|---:|---:|
| 1 | fail | pass | 50,555 | 43,968 | 44.991s | 64.204s |
| 2 | fail | pass | 41,575 | 63,831 | 38.395s | 81.640s |
| 3 | fail | pass | 62,662 | 52,554 | 62.614s | 60.367s |

## 성공 판정

Evaluator ran `:app:testClasses` and required the repaired test-support source to use `equals("ADMIN")`.

## 해석과 한계

- 실패한 arm: baseline repetition 1, baseline repetition 2, baseline repetition 3. 이 결과는 합계에 포함했으며 제외하거나 재시도하지 않았다.
- repetition은 3회뿐이며 단일 fixture task를 측정한다. 따라서 결과를 모든 Java 작업 또는 모델 설정에 일반화할 수 없다.
- OpenCode step `cost`는 0으로 보고돼, 통화 비용은 산출하지 않았다.
- 원시 OpenCode JSONL과 machine-readable aggregate는 실행 환경의 `/tmp/opencode/jman-additional-real/`에 보관됐으며 repository에는 commit하지 않는다.
