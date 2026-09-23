# 검증 기록

2026-09-24, `x86_64-linux`. 재현에 필요한 버전은 `flake.lock`과 Nix artifact hash로 고정한다.

## 실행 검사

```sh
nix develop --command go test -race ./...
nix develop --command go vet ./...
nix flake check -L
nix build .#jman .#jman-bench
```

실제 JDTLS 통합 검사는 mock language server가 아니라 JDTLS 1.60.0, Java 21, Gradle 8.14.4와 Java extension을 실행한다. fixture 의존성은 Nix로 고정하고 Gradle은 offline 모드에서 사용한다.

### 탐색 및 빌드 모델

- 같은 FQN의 오래된 소스가 있어도 실제 JAR의 구현과 digest 선택.
- 의미 기반 references가 과거 선언을 섞지 않음.
- 디스크 수정 뒤 snapshot 갱신과 독립 javac/runtime evaluator 통과.
- 소스 추가/삭제, 페이지 분할, 오래된 cursor 거부.
- 선언 버전과 Gradle constraint가 선택한 버전의 차이.
- test source set의 `testCompileClasspath` 문맥.
- 별도 저장소의 composite build substitution을 실제 소스로 연결.
- sources JAR이 없으면 원본 소스로 표시하지 않음.
- 동일 Git 저장소의 별도 worktree를 서로 다른 session으로 취급.

### 사용 기술스택

- Lombok 1.18.48 agent를 통한 getter/builder 해석과 생성 멤버 표시.
- MapStruct 1.6.3 구현 생성, 구현 탐색, 실제 mapping 결과.
- QueryDSL 5.1.0 + Jakarta Persistence의 Q 타입 생성과 필드 탐색.
- Spring 6.2.3 context, proxy advice, self-invocation 시 advice 우회의 실제 실행.

Spring 실행 검사는 정적 references가 runtime proxy 경로를 전부 해석한다는 의미가 아니다.

### 벤치마크 실행기

- baseline/jman 두 실험군의 fresh repository, 실행 결과, 독립 평가, report 생성.
- 사용자 suite template과 외부 평가 명령 연결.
- provider usage가 없을 때 0으로 취급하지 않음.
- mock HTTP provider를 이용한 tool-call round trip과 cache/reasoning 중복 합산 방지.
- 합성 smoke 결과에는 `evidenceKind: synthetic-smoke` 표시.

## 실제 AI 경제성 측정 상태

**미실행.** 개발 환경에 실제 모델을 호출할 API 인증 또는 외부 agent adapter 설정이 없었다. 따라서 성공률 개선, 토큰 절감률, 비용 절감률 수치를 보고하지 않는다. mock provider와 scripted adapter 결과는 실행기 검증에만 사용한다.

실험을 재개하는 명령:

```sh
OPENAI_API_KEY=... nix run .#bench -- run \
  --model YOUR_MODEL \
  --output ./local/real-experiment \
  --repetitions 5 \
  --cache dependency-warm
```

다른 provider/harness는 `--base-url`, `--api-key-env`, `--adapter-command`로 연결한다. cold와 jdtls-warm 실험도 별도 디렉터리에서 반복하고 원본 events와 실패 실행을 함께 보존한다.
