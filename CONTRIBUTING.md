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

## 문서 유지 계약

각 문서의 역할을 구분한다.

| 파일 | authoritative 내용 | 변경 시점 |
|---|---|---|
| `README.md` | 설치, 현재 CLI 사용법, 정확성 경계, 공개 benchmark 요약 | 사용자에게 보이는 명령·platform·결과 요약 변경 시 |
| `docs/support.md` | **현재 제공하는** 기능, 종료 코드, 한계, suite/adapter schema | 동작·응답·제한·schema 변경 시 |
| `docs/validation.md` | 실제로 실행한 검증과 실제 model trial의 상태 | 검증 범위·명령·측정 상태 변경 시 |
| `docs/requirements.md` | 제품 요구사항과 미구현 SHOULD/MUST 목표 | 제품 요구사항 변경 시 |
| `docs/architecture.md` | 구현 구조와 후속 설계 | 구조·lifecycle·provenance 설계 변경 시 |
| `docs/benchmark.md` | benchmark 가설, fixture와 실험 설계 | suite·oracle·실험군 설계 변경 시 |
| `skills/jman/SKILL.md` | agent에게 배포되는 최소 사용 규칙 | CLI 의미 또는 안전 규칙 변경 시 |

문서 사이에 충돌이 생기면 `docs/support.md`의 현재 지원 범위와 `docs/validation.md`의 실제 측정 상태를 우선한다. 장래 목표를 현재 기능처럼 README에 쓰지 않는다.

### 변경 유형별 최소 동기화

| 변경 | 반드시 함께 검토할 파일 |
|---|---|
| CLI command, option, exit code, JSON field | `cmd/jman/main.go`, tests, `README.md`, `docs/support.md`, 필요 시 skill |
| JDT extension, binding/provenance, source origin | extension test/integration fixture, `docs/support.md`, `docs/architecture.md` |
| Gradle model/source set/composite behavior | `gradle/model.gradle`, integration fixture, `docs/support.md`, `docs/benchmark.md` |
| Lombok/MapStruct/QueryDSL/Spring behavior | `fixtures/stack`, integration test, `docs/validation.md`, relevant scope text |
| Nix package, runtime version, platform | `flake.nix`, `nix/packages/`, `flake.lock`, `README.md`, `docs/validation.md` |
| benchmark runner, usage/cost formula, evaluator | `bench/`, runner tests, `docs/support.md`, `docs/benchmark.md` |
| performance/cost/correctness claim | reproducible report under `reports/`, `docs/validation.md`, README summary |

`jman --help` is the executable source of truth for option spellings. Before updating README command examples, compare them with its output.

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
- [ ] New behavior has an appropriate unit or real JDTLS/Gradle integration test.
- [ ] Generated files, caches, credentials, and evaluator secrets are absent from the diff.
- [ ] Performance or benchmark statements link to reproducible evidence and state their limits.

## Reporting security issues

Do not include credentials, private source, internal artifact coordinates, or access tokens in public issues, fixtures, reports, or logs. For a sensitive issue, use the repository host's private security-reporting channel when available; otherwise ask a maintainer for a private contact path before sharing details.
