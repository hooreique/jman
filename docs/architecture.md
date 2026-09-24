# 아키텍처와 구현 순서

상태: 0.1 구현 구조와 후속 설계를 함께 기록한다. 실제 JDTLS/Gradle fixture 검증을 마쳤지만, 이 문서의 모든 확장 항목이 제공되는 것은 아니다. 현재 지원은 [지원 범위](support.md), 실행 결과는 [검증 기록](validation.md)을 따른다.

## 1. 구성

```text
AI agent / developer
       |
    jman CLI
       | Unix domain socket, versioned local API
    jmand                          user service / lazy start
       |
       +-- workspace session A ---- JDTLS JVM A (+ small Java extension)
       +-- workspace session B ---- JDTLS JVM B (+ small Java extension)
       |
       +-- source/artifact cache

jman-bench -> agent adapter -> fresh agent session -> CLI/tools
           -> independent evaluator -> raw events + report
```

사용자가 제안한 `jmanctl` 역할을 공개 명령 `jman`으로 부른다. 워크스페이스 본체는 `jmand` 내부 session 모듈로 시작한다. 별도 wrapper 프로세스를 workspace마다 추가할 필요가 생기면 그때 분리한다. JDTLS JVM 자체가 workspace별 장애 격리를 제공한다.

`jman-skill-helper`는 `jman skill install --target PATH`로 통합한다. 배포하는 skill 버전과 설치 내용을 추적하고, 같은 버전 재설치는 멱등적으로 처리하며 사용자 수정과 충돌하면 알려 준다.

## 2. 언어와 생태계 선택

| 구성 | 선택 | 이유 |
|---|---|---|
| CLI, daemon, session, skill installer | Go | 빠른 CLI 기동, 프로세스·socket·동시 요청 관리, 단일 배포물, 단순한 Nix 빌드 |
| JDTLS extension | Java | IJavaElement, binding, classpath container 등 JDT 모델에 직접 접근 |
| Gradle model 보강 | 작은 Gradle plugin/init script + 필요 시 Java | Gradle이 선택한 component/variant/artifact 정보를 추출 |
| benchmark runner / report | Python | JSONL 처리, 통계·실험 분석, agent adapter 구현 생태계 |
| benchmark fixture | Java + Gradle | 실제 사용 환경과 annotation processor 검증 |
| 패키징 | Nix flakes | JDTLS/JDK/extension/CLI의 호환 조합 고정 |

Go가 Java 의미 분석을 재구현하지 않게 하고, Python이 daemon 운영의 필수 런타임이 되지 않게 한다. 언어 수는 세 가지지만 각각 운영, JVM 분석, 실험 분석이라는 역할을 갖는다.

## 3. Workspace session의 단위

기본 단위는 **worktree 안의 Gradle build root와 그 build가 선언한 프로젝트 집합**이다.

- 하나의 multi-project build는 하나의 session으로 import한다.
- 한 worktree에 독립적인 Gradle build root가 여럿이면 session을 나눌 수 있다.
- composite build는 Gradle이 선언한 관계를 따라 import한다. 단순히 인접한 저장소들을 합치지 않는다.
- session identity는 canonical root, worktree 경계, build root 집합, 설정 profile로 만든다. head commit은 identity가 아니라 revision이다.
- 서로 다른 session이 같은 실제 composite source root를 포함할 수 있다. mutable JDT workspace는 공유하지 않으며 변경은 각각 반영한다.
- 선택 JDK, JDTLS/extension 버전, Gradle 설정이 바뀌면 해당 인덱스를 재검증/재생성한다.

JDTLS 실행 JDK, Gradle daemon JDK, project toolchain JDK는 별개다. 현재 upstream은 JDTLS 실행에 Java 21 이상을 요구하지만 제품은 검증한 release와 실행 JDK를 고정한다.

## 4. 질의 경로

1. CLI가 경로를 build root에 매핑하고 daemon에 deadline 포함 요청을 보낸다.
2. daemon이 session lock 아래 기동을 단일화한다. 동시에 요청해도 같은 session의 JVM을 중복 생성하지 않는다.
3. session이 import/build state와 파일 변경을 동기화한다.
4. 표준 LSP definition/references/implementation/hover를 실행한다.
5. JDT extension이 필요 시 binding의 declaring element, classpath entry, binary location, generated origin을 보강한다.
6. Gradle model에서 binary path와 선택 component, variant, 의존 경로를 연결한다.
7. source attachment를 확인하고 캐시 파일/짧은 snippet을 준비한다.
8. freshness와 coverage를 포함한 공통 응답으로 변환한다.

**LSP 위치만으로 provenance를 역추정하는 구현은 핵심 요구사항을 만족하지 못할 수 있다.** extension과 Gradle 보강의 최소 범위를 기술 검증 단계에서 확정한다. version conflict/substitution 정보가 필요한데 가져오지 못했으면 명시적으로 unknown을 반환한다.

### Source resolution

실제 JDT binding 대상 → 선택 binary → 연결된 sources JAR → 검증 가능한 저장소 mapping → 디컴파일/시그니처 순으로 진행한다. 독립 저장소 mapping이 있어도 compiler binding 자체는 바뀌지 않는다.

source JAR 내부 경로와 checksum을 보존하고 읽기 전용 캐시에 안전하게 추출한다. binary와 source cache는 내용 주소로 공유할 수 있으나 workspace별 mutable index와 provenance 문맥은 분리한다.

Nexus 인증과 private repository 설정은 기존 Gradle 환경을 이용한다. 별도 인증 저장소를 만들지 않고 로그/리포트에 credentials를 남기지 않는다. 오프라인·권한 오류·소스 미발행은 서로 구별한다.

## 5. 준비 상태, 자원, 복구

상태는 `starting → importing → indexing → ready`와 `degraded`, `failed`, `stopped`를 구별한다. LSP initialize 응답만으로 ready를 선언하지 않는다.

고정 JDTLS 버전에서 import 완료 신호, 관련 build job 완료, classpath 검사와 synchronization barrier를 조합해 질의별 준비 조건을 검증한다. 모든 파일의 정상 컴파일을 요구하지는 않는다. 문제가 있는 project/source set이 coverage에 어떻게 영향을 주는지 표시한다.

- 활성 JVM 수, import 동시성, 총 메모리 예산을 설정한다.
- 요청 중인 session은 보호하고 idle session을 LRU로 종료한다.
- deadline, request cancellation, bounded queue, crash backoff를 구현한다.
- 읽기 요청은 가능한 범위에서 병렬 처리하되 import/refresh/model 교체는 직렬화한다.
- 한 요청이 취소되어도 다른 요청이 기다리는 공통 import를 중단하지 않는다.
- daemon 재시작, stale socket, JVM crash 뒤 복구를 검증한다.
- `status`는 작은 요약, `doctor --json`은 JDK/Gradle/Lombok/processor/classpath의 상세 진단을 제공한다.

package, app, dev shell은 `aarch64-darwin`, `aarch64-linux`, `x86_64-linux`에서 평가·제공한다. Unix socket과 lazy start는 세 대상에서 공통으로 사용한다. Home Manager의 systemd user service는 Linux에서만 만들며, Darwin에서는 패키지 설치와 수동 daemon 실행을 사용한다.

## 6. flake 패키징 계약

현재 아래 outputs를 제공한다. package 구현은 `nix/packages/*/package.nix`에 분리했고, `overlays.default`는 caller의 nixpkgs로 재빌드하며 `overlays.pinned`는 이 flake의 고정 package를 제공한다.

- `packages.<system>.jman`: CLI, daemon, skill, 고정 JDTLS와 호환 extension을 묶은 wrapper.
- `packages.<system>.jman-bench`: Python runner와 report 도구.
- `apps.<system>.default`, `apps.<system>.bench`.
- `devShells.<system>.default`: Go, Java, Python, Nix 검증에 필요한 도구.
- `checks.<system>`: unit/protocol contract, 작은 실제 JDTLS fixture smoke test, fixture oracle 검사.
- `homeManagerModules.default`: user service와 자원 설정.

출력 대상은 `aarch64-darwin`, `aarch64-linux`, `x86_64-linux`다. CI/개발 환경에서 실제 통합 검사를 실행한 대상은 현재 `x86_64-linux`이며, 다른 두 대상은 flake 평가까지만 확인했다.

`flake.lock`, JDTLS/extension/Lombok, build dependencies를 고정한다. 패키지 빌드와 deterministic checks는 Nix sandbox에서 실행되며 임의의 Gradle 네트워크 다운로드에 의존하지 않게 한다. 고정 artifact mirror/dependency closure를 먼저 준비한다.

실제 사용자 프로젝트의 의존성 해석은 실행 시 Gradle 환경에서 수행한다. Nix store에는 mutable JDTLS configuration이나 workspace data를 쓰지 않는다. LLM API를 호출하는 benchmark는 `nix flake check` 밖에서 실행한다.

## 7. 구현 순서와 통과 조건

### 완료: 단계 0 — 가장 어려운 가정 검증

작은 caller build, 내부 라이브러리 binary/sources, 동일 FQN의 오래된 decoy source를 만든다. 고정 JDTLS에 실제 질의를 보내 JAR 선택, classpath provenance, source 읽기를 확인한다. Lombok getter/builder, Gradle source generation도 최소 fixture로 검증한다.

통과 조건: 실제 binary binding을 증거로 제시할 수 있고, 소스 불일치·생성 실패를 올바르게 표현한다. 불가능한 API 지점과 필요한 extension 범위가 문서화되어야 한다.

### 완료: 단계 1 — 수직 MVP

flake, `jman definition/read/status/doctor/prepare`, 최소 daemon/session, skill, 내부 JAR 착각 benchmark 한 문제와 두 실험군 실행을 끝까지 연결한다.

통과 조건 중 binding/worktree/독립 evaluator는 충족했다. 실제 모델 API 인증 또는 외부 adapter가 없어 경제성 raw report는 아직 생성하지 않았으며, 합성 adapter smoke 결과를 경제성 증거로 사용하지 않는다.

### 대부분 완료: 단계 2 — 첫 제품 요구사항

references/implementations/hover/refresh, source set/variant/composite build, generated source, file freshness, timeout/crash recovery, resource limits, user service를 완성한다.

references/implementations/hover/refresh, source set, composite build, generated source, file freshness, timeout/backoff, user service를 구현·검증했다. 전체 MUST 충족 여부는 `requirements.md`의 목표와 `support.md`의 미지원 목록을 대조해 판단한다.

### 진행 예정: 단계 3 — 실제 agent 비교 실험과 확장

여러 모델/문제/반복으로 효과를 검증하고 bottleneck을 개선한다. cross-repository references, Spring 관계, MCP adapter는 효과와 필요를 확인해 추가한다.

## 8. 설계 근거와 확인할 부분

2026-09-24 확인한 upstream 문서/소스:

- [JDTLS README](https://github.com/eclipse-jdtls/eclipse.jdt.ls/blob/main/README.md): Gradle/Buildship, workspace별 data directory, 실행 JDK 요구사항.
- [JDTLS plugin.xml](https://github.com/eclipse-jdtls/eclipse.jdt.ls/blob/main/org.eclipse.jdt.ls.core/plugin.xml): classpath/source attachment 명령, delegate command extension, source/decompiler provider.
- [vscode-java 설정](https://github.com/redhat-developer/vscode-java/blob/master/package.json): 실행/Gradle/project JDK 분리, Lombok, Gradle annotation processing 설정.

이는 구현 선택의 upstream 근거다. headless JDTLS, Lombok agent, Gradle import는 고정 release에서 integration test로 검증했다. 다만 VS Code client 설정 전체가 headless JDTLS에 자동 적용된다고 가정하지 않으며, 지원 행렬의 실제 실행 대상은 `validation.md`에 기록한다.
