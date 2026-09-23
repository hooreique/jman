# OpenCode JDTLS Origin Navigation Trial

- **수행일:** 2026-09-24
- **테스트 이름:** `opencode-jdtls-origin-navigation`
- **jman commit:** `76bb106` (`fix: bound benchmark evaluation and reject edits to auxiliary repositories`)
- **OpenCode:** 2.0.15, `nix run github:numtide/llm-agents.nix#opencode2`
- **LLM:** `openai/gpt-5.6-terra`
- **반복 수:** 각 arm 3회, 총 6회 실제 모델 session

## 목적

저장소에 남은 과거 `TextUtil`과 실제 Gradle classpath의 내부 라이브러리 `TextUtil`이 같은 FQN을 갖는 상황에서, jman이 실제 문제 해결의 시간과 모델 token 사용량을 줄이는지 관찰한다.

## 과제와 성공 판정

각 실행은 `fixtures/commerce` template에서 새 독립 Git repository와 로컬 Maven repository를 materialize했다.

에이전트에게 주어진 과제는 다음과 같다.

1. `AccessService.isAdmin(" admin ")`의 실패 원인을 찾는다.
2. application 코드만 최소 수정한다.
3. 비관리자는 계속 거부한다.
4. 실제 호출된 `TextUtil.normalize` 구현과 dependency version을 설명한다.

각 실행이 끝난 후, 별도 evaluator가 수정된 application source만 깨끗한 fixture에 적용했다. evaluator는 javac로 compile하고 `" admin "`, `"ADMIN"`, Unicode whitespace, 일반 사용자, `"superadmin"`을 실행해 판정했다.

## 실험군과 통제

| Arm | 제공한 것 |
|---|---|
| baseline | 일반 OpenCode 파일 읽기·검색·shell 도구. `jman` command와 skill 없음. |
| jman | baseline과 동일한 도구에 packaged `jman` command를 PATH로 추가하고 `jman` skill을 추가. |

두 arm 모두:

- 같은 agent prompt, 모델, auto-approved tool 정책을 사용했다.
- 매 실행 새 fixture, 새 Git repository, 별도 `OPENCODE_CONFIG_DIR`, 별도 standalone OpenCode server를 사용했다.
- shell 기본 PATH에는 Java/Gradle을 추가하지 않았다. jman arm은 제품이 패키징한 JDTLS/JDK/Gradle 통합 정보를 이용할 수 있다.
- OpenCode JSON output의 각 `step_finish.tokens`를 합산했다.

`input`과 `cache.read`는 OpenCode가 별도 보고하므로 분리했다. `reported total`은 `input + output + cache-read`이고, `non-cache total`은 `input + output`이다.

## 결과

모든 실행이 independent evaluator를 통과했다: **baseline 3/3, jman 3/3**.

| 지표 | Baseline 합계 | jman 합계 | 변화 |
|---|---:|---:|---:|
| non-cache input tokens | 64,992 | 52,301 | **-19.5%** |
| output tokens | 5,926 | 3,431 | **-42.1%** |
| cache-read tokens | 230,400 | 227,328 | -1.3% |
| reported total tokens | 301,318 | 283,060 | **-6.1%** |
| non-cache total tokens | 70,918 | 55,732 | **-21.4%** |
| wall time | 334.275초 | 238.988초 | **-28.5%** |

### Trial별 결과

| Trial | Baseline reported tokens | jman reported tokens | Baseline time | jman time |
|---|---:|---:|---:|---:|
| 1 | 131,660 | 95,230 | 168.739초 | 84.957초 |
| 2 | 88,443 | 71,987 | 55.177초 | 73.057초 |
| 3 | 81,215 | 115,843 | 110.359초 | 80.974초 |

## 관찰

- Trial 1의 baseline은 JAR source를 직접 열고 Java/Gradle 위치를 `/nix/store`에서 탐색했다. jman은 definition 한 번으로 `com.acme:shared-text:2.4.1`, source snippet, compile classpath 문맥을 얻었다.
- 합계 기준 jman은 non-cache token을 21.4%, wall time을 28.5% 줄였다.
- Trial 3에서는 jman의 reported token이 baseline보다 컸다. 세 번의 실행만으로 모든 Java 작업에 대한 일반적인 절감 효과를 주장할 수 없다.
- OpenCode event의 step `cost`는 모두 0이었다. 따라서 이 리포트는 실제 모델 token과 시간을 측정하지만 통화 비용을 계산하지 않는다.

## 원본 산출물

실행 당시의 원시 OpenCode JSONL과 집계 JSON은 작업 환경의 다음 위치에 보관됐다.

```text
/tmp/opencode/jman-real/three-trial-report.json
/tmp/opencode/jman-real/evaluation.json
```

`/tmp` 산출물은 저장소에 commit하지 않았으므로 장기 재현에는 `jman-bench`와 위 방법을 사용한다. 이 리포트의 수치는 해당 JSONL event log의 `step_finish.tokens`와 timestamp에서 계산했다.
