# MapStruct Generated Implementation Trial

- **수행일:** 2026-09-24
- **테스트 이름:** `mapstruct-generated-implementation`
- **jman commit:** `9b066f3454b06c67b11f1d4313cb49afd6921266` (`9b066f3 docs: link validation record to real agent trial`)
- **OpenCode:** 2.0.15 via `nix run github:numtide/llm-agents.nix#opencode2`
- **LLM:** `openai/gpt-5.6-terra`
- **반복 수:** baseline 3회, jman 3회 (총 6 actual OpenCode sessions)

## 목적과 과제

`PersonDto` was changed to `displayName`; repair source mapping configuration so the generated MapStruct mapper preserves the name, without editing generated output.

## 방법

각 arm과 repetition은 새 독립 fixture repository, 별도 `OPENCODE_CONFIG_DIR`, standalone OpenCode session에서 같은 prompt와 `--auto` 정책으로 실행했다. baseline과 jman 모두 JDK 21 및 Gradle 8.14.4를 PATH에서 사용할 수 있게 해 환경 탐색 비용을 통제했다. jman arm에만 packaged `jman` command와 `jman` skill을 추가했다. build script, dependency/library artifact, generated output 수정은 금지했다.

OpenCode JSON event log의 모든 `step_finish.tokens`를 합산했다. `input`과 `cache.read`는 OpenCode가 별도로 보고하므로 분리한다. `reported total`은 input + output + cache-read, `non-cache total`은 input + output이다. 시간은 event log의 첫 timestamp부터 마지막 timestamp까지다. 성공 판정은 agent의 최종 답변이 아니라 독립 evaluator 결과다.

## 결과

독립 evaluator: **baseline 3/3, jman 3/3**.

| Metric | Baseline total | jman total | jman vs baseline |
|---|---:|---:|---:|
| non-cache input tokens | 29,216 | 46,237 | +58.3% |
| output tokens | 1,232 | 1,969 | +59.8% |
| cache-read tokens | 71,680 | 146,432 | +104.3% |
| reported total tokens | 102,128 | 194,638 | +90.6% |
| non-cache total tokens | 30,448 | 48,206 | +58.3% |
| event-log wall time | 57.639s | 126.285s | +119.1% |
| tool calls | 33 | 47 | +42.4% |

### Repetition별 결과

| Repetition | Baseline success | jman success | Baseline reported tokens | jman reported tokens | Baseline time | jman time |
|---|---|---|---:|---:|---:|---:|
| 1 | pass | pass | 26,927 | 43,085 | 18.361s | 35.650s |
| 2 | pass | pass | 41,291 | 51,313 | 21.015s | 38.182s |
| 3 | pass | pass | 33,910 | 100,240 | 18.263s | 52.453s |

## 성공 판정

Evaluator ran the fixture Gradle `verifyStack` task.

## 해석과 한계

- 모든 repetition이 evaluator를 통과했다.
- repetition은 3회뿐이며 단일 fixture task를 측정한다. 따라서 결과를 모든 Java 작업 또는 모델 설정에 일반화할 수 없다.
- OpenCode step `cost`는 0으로 보고돼, 통화 비용은 산출하지 않았다.
- 원시 OpenCode JSONL과 machine-readable aggregate는 실행 환경의 `/tmp/opencode/jman-additional-real/`에 보관됐으며 repository에는 commit하지 않는다.
