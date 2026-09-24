# 벤치마크와 경제성 검증

상태: runner, deterministic fixture, 독립 evaluator, provider/harness adapter는 구현·검증됐다. 실제 모델 API 인증 또는 외부 adapter 설정이 없어서 실제 agent A/B 결과와 경제성 수치는 아직 없다. 이 문서의 가설·권장 측정 항목과 현재 runner가 수집하는 항목을 구분한다. 자세한 상태는 [검증 기록](validation.md)을 본다.

## 1. 검증할 가설

H1: jman은 동명·동일 경로의 오래된 소스로 인한 잘못된 판단과 수정을 줄인다.

H2: 정확도 저하 없이 정답까지의 토큰, 도구 호출, 시간, 금전 비용을 줄인다.

H3: 초기 import 비용은 반복 작업에서 상각된다. 한 번의 짧은 작업에서는 손해일 수도 있다.

평가 대상은 navigation latency 자체와 **에이전트의 문제 해결 성과** 두 가지다. 짧은 응답만으로 경제성이 증명되지는 않는다.

## 2. 현재 예시 환경과 목표 suite

기본 `fixture` 명령은 `commerce`와 `internal-text`를 새 독립 Git repository로 materialize하고 로컬 Maven repository에 `shared-text:2.4.1` artifact를 만든다. `stack` fixture는 Lombok, MapStruct, QueryDSL/JPA, Spring AOP 통합 검사에 사용하지만 현재 agent A/B 문제는 아니다.

```text
fixtures/                       추적되는 fixture template
  commerce/                     app + 오래된 decoy source를 가진 Gradle multi-project
  internal-text/                실제 내부 라이브러리 소스
  stack/                        generated-code와 Spring runtime fixture

<output>/                      experiment.json, report.json, report.md
  <repetition>-<arm>/          실행별 새 디렉터리
    workspace/                  agent가 수정하는 독립 Git repository
    evaluation-workspace/       종료 뒤 허용 patch만 적용하는 별도 환경
    cache/                      arm별 daemon/Gradle cache
    events.jsonl
    patch.diff
    result.json
```

기본 fixture는 로컬 Maven repository로 Nexus의 artifact resolution 상황을 재현한다. 인증, HTTP 실패, repository 정책은 아직 별도 HTTP repository scenario로 구현하지 않았으며, 기본 실행에 실제 Nexus 서비스를 요구하지 않는다.

### 목표 시나리오와 현재 검증

| 시나리오 | 함정 | 독립적인 정답 근거 |
|---|---|---|
| 내부 라이브러리 분리 | 이전 코드와 현재 JAR이 같은 FQN | Gradle 선택 artifact digest + 동작 테스트 |
| 선택 버전 충돌 | 선언 버전과 transitive constraint 최종 버전이 다름 | Gradle resolution 결과 + 해당 버전의 동작 |
| main/test 차이 | 같은 이름의 대상이 source set별로 다름 | 각 source set의 compile/test oracle |
| Lombok | getter, builder, 생성 constructor | 실제 javac/Gradle compile + 동작 테스트 |
| MapStruct | interface와 생성 구현의 mapping 차이 | processor 실행 + mapping 결과 테스트 |
| QueryDSL/JPA | Q 타입과 생성 입력의 연결 | generated artifact + query/통합 테스트 |
| composite build | JAR 대신 project substitution | Gradle 선택 component + 변경 영향 테스트 |
| 소스 미발행/불일치 | source 없음 또는 오래된 source JAR | binary 동작 + source provenance 판정 |
| cross-repository 버전 차이 | 같은 FQN이 다른 버전으로 소비됨 | consumer별 dependency identity |
| Spring AOP | 정적 호출과 proxy 실행 효과가 다름 | 실제 Spring context 테스트 |
| 갱신 | dependency 변경/branch 전환 뒤 오래된 index | 변경 후 Gradle oracle |
| 일반 탐색 대조군 | 단순 local method로 도구 이점이 작음 | 간단한 동작 테스트 |

현재 Spring fixture는 self-invocation과 proxy advice를 실행 검증한다. 조건부 bean 같은 추가 runtime scenario는 후속 suite 대상이며, 도구가 정적 분석의 한계를 표시하는지는 현재 integration response와 문서에서 점검한다.

현재 agent 과제는 내부 라이브러리 분리와 실제 코드 수정 한 가지다. 나머지 표의 항목은 integration fixture 또는 후속 A/B suite 대상으로, 이 한 문제의 결과를 전체 Java 환경의 성능으로 일반화하지 않는다.

## 3. 실험군

- **A: baseline** — 파일 읽기/검색/shell, Gradle, 동일한 repository/source 접근 권한. jman과 jman skill은 없음.
- **B: jman** — A와 같은 환경에 jman과 짧은 skill 추가.
- **C: 일반 LSP 대조군, 미구현** — 같은 JDTLS와 classpath를 일반 LSP adapter로 노출. 일반적인 의미 탐색의 효과와 jman의 출처/응답 설계 효과를 구분한다.

각 paired trial에서 동일 모델 버전, sampling 설정, 문제 문구, 시간/토큰 예산, 시작 commit, 파일과 의존성을 사용한다. 도구 접근 정보와 skill만 실험군에 맞게 바뀐다. 동일 seed 지원 여부도 기록하며 같은 seed를 결과 동일성 보장으로 해석하지 않는다.

baseline도 `javap`, unzip, dependencyInsight, 소스 다운로드 등을 이용할 수 있다. jman만 정답 source에 접근할 수 있게 만들지 않는다.

agent는 매번 새 session에서 시작한다. 순서는 randomize하고, 양 실험군의 수정 파일·history·daemon·캐시는 격리한다. 네트워크 artifact warmness는 같게 유지한다.

## 4. 정답 판정

1. **도구 oracle:** 위치에서 선택된 실제 component/binary, symbol signature, source kind가 fixture manifest 및 Gradle/bytecode 근거와 맞는가?
2. **작업 oracle:** hidden test, 동작 결과, 허용된 수정 범위를 만족하는가?
3. **설명 oracle:** 정답 필드(artifact/version, 실제 원인, 영향 범위)를 근거와 함께 설명하는가?

정답은 jman 결과에서 생성하지 않는다. fixture 제작 단계에서 Gradle resolution과 실행 결과로 확립한다. 기본 실패 상태에서 테스트가 실제로 실패하고 기준 수정에서는 통과하는지도 확인한다.

자유로운 agent shell에서 단지 `evaluation/`이라는 폴더 이름만으로 정답이 보호되지는 않는다. agent filesystem에는 evaluator 데이터와 hidden tests를 mount하지 않는다. 종료 후 별도 환경에서 patch를 적용해 검사한다.

자연어 설명은 구조화된 사실 확인을 우선하고, human/LLM rubric 판단은 보조로 표시한다. 정답 판정 모델의 비용은 agent 실행 비용과 분리한다.

## 5. 기록할 이벤트와 지표

agent adapter는 provider/harness가 제공하는 raw usage를 보존한다. 기록할 수 없는 토큰 항목은 `unavailable`로 두며 응답 글자 수를 실제 청구 토큰으로 바꾸지 않는다.

### 현재 Run metadata

현재는 fixture/repository revision, task, arm, repetition, model, cache mode, skill hash, jman version, 시작 시각, 예산과 종료 사유를 기록한다. provider/harness가 제공하면 raw usage도 보존한다. provider version, sampling, hardware/OS의 완전한 수집은 adapter 확장 항목이다.

### 이벤트

model request/response와 usage, tool 시작/종료/결과 크기, daemon/import 시작/완료, 오류·재시도, patch, evaluator 결과. harness 내부 재시도와 압축/요약 호출도 노출되는 범위에서 포함한다.

### 현재 지표와 목표 지표

- 작업 성공률, 잘못된 dependency 선택률, 잘못된 파일 수정률.
- 전체 및 정답 도달까지의 wall time, tool call 수, 읽은 파일/출력 byte 수.
- input/output/cache-read/reasoning token: provider 의미를 보존하고 중복 합산하지 않는다. cache-write는 현재 별도 집계하지 않는다.
- 명시한 가격 파일로 계산한 추정 LLM 비용을 기록한다. provider의 실제 청구 금액은 현재 수집하지 않는다.
- daemon과 관찰된 자식 프로세스의 sampled peak RSS/CPU time을 기록한다. Gradle daemon, 짧은 프로세스, import time, 질의 latency p50/p95는 현재 완전하게 측정하지 않는다.
- skill, tool schema, tool output을 포함한 전체 model input 비용.

추가한 tool output은 뒤이은 여러 model request에 반복 입력될 수 있으므로 한 번의 출력 토큰만 세지 않는다. 전체 request usage가 주 지표다.

### 현재 집계와 권장 분석

성공 사례의 비용과 전체 시도 비용을 모두 보고한다. 빨리 오답을 낸 실행이 싸다는 이유로 우수하다고 판단하지 않는다.

`cost per success = 모든 실행 비용 합 / 성공 실행 수`를 보고하고 성공 0건이면 정의 불가로 표시한다. 이는 관측된 집계값이며 자동 재시도 정책의 기대 비용과 같다고 가정하지 않는다.

현재 runner는 arm별 성공률/median time/token 합계와 같은 repetition의 paired time delta 및 간단한 bootstrap interval을 report한다. task 단위 cluster bootstrap, 성공률의 신뢰구간, 여러 task에 대한 추론은 후속 분석 항목이다. 작은 suite의 결과를 모든 Java 프로젝트로 일반화하지 않는다.

초기 탐색은 task/arm당 5회 정도로 시작하되 결과를 확증적 증거로 부르지 않는다. pilot 변동성을 확인한 후 본실험의 반복 수와 주요 지표를 고정한다. 중간에 유리한 실행만 골라 중단하지 않는다.

## 6. Cold / warm과 손익분기점

적어도 세 상태를 구별한다.

1. dependency cache cold + JDTLS cold: repository download 포함.
2. dependency cache warm + JDTLS cold: 최초 import/index 비용 포함.
3. dependency cache warm + JDTLS warm: 준비된 workspace의 반복 사용.

각 상태의 기준선 환경도 대응되게 구성한다. benchmark용 상태 초기화와 예열 시간을 기록하고 warm 결과에서 제외한 준비 비용을 별도로 보여 준다.

단일 작업, 같은 workspace에서 연속 작업, 여러 workspace를 바꾸는 세 가지 사용 패턴을 측정한다. warm 실험에서도 agent session은 새로 시작할 수 있으며 서비스 warmness와 대화 기억을 혼동하지 않는다.

시간 손익분기점의 단순 추정은 `준비 시간 / 작업당 평균 절약 시간`이다. 절약 시간이 0 이하이면 손익분기점이 없다. 가격을 부여하지 않은 CPU/메모리 비용을 임의로 달러 비용에 합치지 않고 별도 보고한다.

## 7. 현재 runner 계약

명령:

```sh
jman-bench fixture ./local/example
jman-bench validate --output ./local/oracle-check
jman-bench run --model YOUR_MODEL --output ./local/experiment --arms baseline,jman --repetitions 5 --cache dependency-warm
jman-bench report ./local/experiment
```

사용자 suite는 `--suite suite.json`으로 넣는다. manifest는 template, project, repositories, prompt, allowedEdits, evaluate argv를 정의한다. 선택적으로 prepare와 explanationTerms를 둘 수 있다. agent adapter는 launch, tool access, event stream, usage, cancellation, final patch 수집을 맡는다. 특정 harness 종속 부분은 adapter에 한정한다.

CLI 옵션의 cache 명칭과 실제 cache 초기화 절차를 manifest에 기록한다. 재개 시 완료 run을 덮어쓰지 않고, 실패한 run도 report에 유지한다. raw data만으로 집계를 다시 생성할 수 있어야 한다.

## 8. 성공 기준

첫 단계의 필수 조건:

- 핵심 내부 라이브러리 오인 fixture에서 deterministic binding oracle 통과.
- incomplete import와 source mismatch를 정상 성공처럼 반환하지 않음.
- A/B 양쪽을 동일 문제에서 끝까지 실행하고 raw usage와 독립 evaluator 결과 확보.

경제성 주장은 실제 실험 뒤에 한다. 채택 목표는 **정확도 비열등 + 전체 비용 감소**, 또는 비용 증가를 명시하면서도 중요한 오답을 충분히 줄이는 것이다. 비열등 허용 폭과 실질적인 비용 절감 최소치는 pilot 후 본실험 전에 고정한다.

산출물에는 개선된 사례, 악화된 사례, timeout/error, cold-start 부담, 측정하지 못한 항목을 함께 포함한다.
