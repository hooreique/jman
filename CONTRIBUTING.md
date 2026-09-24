# Contributing to jman

jman의 변경은 **코드, 사용자 계약, 검증 근거**를 함께 유지해야 한다. 특히 Java symbol을 잘못된 source에 연결하지 않는 것이 핵심 제품 약속이다. 구현을 변경할 때 문서가 그보다 넓거나 좁은 주장을 하지 않도록 해 주세요.

이 저장소는 [MIT License](LICENSE)로 제공된다. 기여를 제출하면 기여분을 같은 MIT 조건으로 제공할 권한이 있음을 확인한 것으로 간주한다.

## 시작하기

지원 플랫폼의 개발 환경은 Nix가 제공한다.

```sh
nix develop
go test -race ./...
go vet ./...
```

로컬 binary와 실제 JDTLS integration suite를 실행하려면 다음을 사용한다.

```sh
go build -o local/bin/jman ./cmd/jman
python3 tests/integration.py local/bin/jman
python3 tests/bench_test.py local/bin/jman
nix flake check -L
```

`nix flake check -L`은 Go unit test, benchmark runner test, 실제 JDTLS/Gradle integration fixture를 실행한다. fixture의 Java dependency는 Nix로 고정되어 있으며 integration은 Gradle offline 모드로 실행된다.

## 문서 책임과 경계

문서는 짧고, 목적이 하나이며, 서로의 역할을 침범하지 않아야 한다. 구현 변경으로 문서를 길게 보완하기보다, **어느 문서가 그 사실의 단일 source of truth인지** 먼저 결정해 중복을 줄인다. 현재 동작을 장래 계획처럼 쓰거나, 설계 목표를 이미 제공되는 기능처럼 쓰지 않는다.

| 문서 | 책임 | 쓰지 않는 내용 |
|---|---|---|
| `README.md` | 설치, 첫 사용, 필요한 설정, 사용자가 알아야 할 핵심 한계 | 개발 절차, 내부 설계, 전체 지원 행렬, benchmark 방법론 |
| `CONTRIBUTING.md` | 유지보수 규칙, 문서 경계, 테스트, PR 기준 | 일반 사용 튜토리얼, 제품 마케팅 |
| `docs/support.md` | 현재 제공 기능, 응답/종료 코드, 명시적 한계, suite/adapter 계약 | 장기 설계 서술, 완료되지 않은 계획 |
| `docs/validation.md` | 실제로 실행한 검증과 관측된 trial | 제품 요구사항 또는 일반화된 성능 주장 |
| `docs/requirements.md` | 제품 요구사항과 미구현 목표 | 현재 구현의 유일한 상태 선언 |
| `docs/architecture.md` | 구현 구조와 설계 근거 | 사용자 getting-started 절차 |
| `docs/benchmark.md` | benchmark 가설, fixture, oracle, 실험 설계 | 관측값의 유일한 기록 |
| `skills/jman/SKILL.md` | agent에게 배포하는 최소 사용·안전 규칙 | 구현 세부사항과 긴 설명 |

문서 사이에 충돌하면 `docs/support.md`의 현재 지원 범위와 `docs/validation.md`의 실제 측정 상태를 우선한다.

### 지속 가능한 문서 유지

- 한 사실은 한 곳에서 자세히 설명하고, 다른 문서는 링크한다. 같은 명령 목록·수치·한계를 여러 문서에 복사하지 않는다.
- README는 getting started에 필요한 내용만 유지한다. 사용자가 첫 성공을 한 뒤 필요한 세부사항은 `docs/` 또는 `CONTRIBUTING.md`로 보낸다.
- 변경과 무관한 문서 재작성은 피한다. 문서 구조를 바꿀 때는 링크, 중복, source of truth를 함께 정리한다.
- 측정하지 않은 성능·비용·정확도 수치를 쓰지 않는다. 실제 trial 수치는 report와 validation에 근거·표본·한계를 함께 기록한다.
- 명령, option, JSON field, exit code처럼 코드에서 확인 가능한 사실은 `jman --help`, schema, test를 먼저 갱신하고 문서를 맞춘다.
- 새 문서는 기존 문서의 책임을 대체하는 이유가 있을 때만 추가한다. 그렇지 않으면 기존 문서를 보완한다.

## 변경 동기화

변경 유형별로 다음 문서와 테스트를 함께 검토한다.

| 변경 | 반드시 함께 검토할 파일 |
|---|---|
| CLI command, option, exit code, JSON field | `cmd/jman/main.go`, tests, `README.md`, `docs/support.md`, 필요 시 skill |
| JDT extension, binding/provenance, source origin | extension test/integration fixture, `docs/support.md`, `docs/architecture.md` |
| Gradle model/source set/composite behavior | `gradle/model.gradle`, integration fixture, `docs/support.md`, `docs/benchmark.md` |
| Lombok/MapStruct/QueryDSL/Spring behavior | `fixtures/stack`, integration test, `docs/validation.md`, relevant scope text |
| Nix package, runtime version, platform | `flake.nix`, `nix/packages/`, `flake.lock`, `README.md`, `docs/validation.md` |
| benchmark runner, usage/cost formula, evaluator | `bench/`, runner tests, `docs/support.md`, `docs/benchmark.md` |
| performance/cost/correctness claim | reproducible report under `reports/`, `docs/validation.md`, README summary |

`jman --help` is the executable source of truth for option spellings. README 예시를 갱신하기 전에 help 출력과 비교한다.

## Code guidelines

- Run `gofmt -w cmd internal` for Go changes. Keep `go test -race ./...` and `go vet ./...` clean.
- Do not resolve a symbol by matching filename, FQN, repository name, or nearby Git checkout. Start from the caller binding and selected classpath.
- Keep selected binary provenance separate from source provenance. Do not claim a source JAR is the binary's exact source unless that proof exists.
- Preserve canonical paths, bounded responses, deadlines, cancellation, isolated session state, and snapshot-bound cursors.
- Never add credentials, local caches, build outputs, `.gradle`, IDE metadata, generated fixtures, API logs, or `/tmp` trial data to Git.
- New external artifacts must have fixed hashes and belong in the Nix packaging/fixture closure. Do not make tests depend on Maven Central or a private Nexus at test time.
- Treat Spring runtime behavior, reflection, and dynamic queries as runtime concerns unless a test proves a precise static claim.

## Tests and fixtures

Prefer a test at the lowest level that proves the change:

1. Go unit test for parsing, protocol, state, pagination, response formatting, or cancellation.
2. Actual JDTLS integration test for binding, classpath, source attachment, generated code, Gradle conflict, source set, or composite build behavior.
3. Independent compiler/runtime oracle for an agent task or behavior claim.

`fixtures/commerce` is the dependency-provenance fixture. `fixtures/stack` exercises Lombok, MapStruct, QueryDSL/JPA, and Spring AOP. Keep generated output out of Git; the fixture must generate it from tracked inputs.

When adding an agent benchmark scenario, keep evaluator code outside the agent workspace, create fresh repositories for each arm, record failures as well as successes, and do not use a scripted adapter result as evidence of LLM performance.

## Benchmark claims

Claims about actual agent tokens, time, success, or cost require all of the following:

1. A committed report in `reports/` describing model, harness, prompt, arms, repetitions, cache state, evaluator, and limitations.
2. Raw-event retention location and an explanation of what is or is not committed.
3. Independent evaluation of the changed code, not a jman answer as the oracle.
4. A clear distinction between observed data, calculated prices, synthetic runner tests, and general conclusions.

If provider usage is missing, report it as missing—never infer token counts from characters or replace it with zero.

## Pull request checklist

- [ ] Scope is focused; unrelated user changes were not reverted.
- [ ] Go code is formatted and relevant tests pass.
- [ ] `nix flake check -L` passes, or the limitation is explained.
- [ ] CLI/help, README, support scope, skill, and design docs were reviewed per the table above.
- [ ] 문서 책임과 경계를 확인했고, 같은 사실을 불필요하게 중복하지 않았다.
- [ ] New behavior has an appropriate unit or real JDTLS/Gradle integration test.
- [ ] Generated files, caches, credentials, and evaluator secrets are absent from the diff.
- [ ] Performance or benchmark statements link to reproducible evidence and state their limits.

## Reporting security issues

Do not include credentials, private source, internal artifact coordinates, or access tokens in public issues, fixtures, reports, or logs. For a sensitive issue, use the repository host's private security-reporting channel when available; otherwise ask a maintainer for a private contact path before sharing details.
