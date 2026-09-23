# jman — JDTLS Manager

Java 개발자를 돕는 AI agent가 **현재 호출 지점의 실제 정의와 참조**를 적은 탐색 비용으로 확인하도록 돕는 도구.

주요 대상은 Gradle multi-project / multi-repository 환경과 Spring, Lombok, JPA, QueryDSL, MapStruct를 사용하는 프로젝트다. 특히 현재 classpath의 내부 라이브러리와 저장소에 남아 있는 오래된 동명 코드를 구별하는 것을 우선한다.

## 설치와 시작

현재 패키징 대상은 `x86_64-linux`다. Nix가 JDK, JDTLS, Lombok agent와 jman extension의 버전을 함께 고정한다.

```sh
nix build
./result/bin/jman --help

# 또는 설치 없이 실행
nix run . -- definition path/to/File.java:42 --symbol normalize
```

프로젝트 디렉터리에서 다음 명령을 사용한다. 첫 요청이 user-scoped daemon과 해당 build의 JDTLS를 시작한다.

```sh
jman definition app/src/main/java/example/OrderService.java:42 --symbol normalize
jman references app/src/main/java/example/OrderService.java:42 --symbol normalize
jman doctor
jman status
jman stop
```

결과에는 실제 binding의 시그니처, 선택된 Gradle artifact, source set, binary SHA-256, 짧은 소스가 포함된다. `--json`으로 구조화 응답, `--explain`으로 자세한 텍스트를 얻는다. 외부 소스도 반환된 캐시 경로에서 읽을 수 있다.

```sh
jman read /path/from/definition/Source.java --lines 20:60
jman references path/to/File.java:42 --symbol normalize --limit 10
jman implementations path/to/Mapper.java:7 --symbol toDto
jman hover path/to/File.java:42 --symbol normalize
jman deps path/to/File.java
```

`--symbol`은 지정한 줄의 토큰을 선택한다. 같은 줄에 여러 번 나오면 `--occurrence 2`로 선택한다. 줄/열은 1부터 시작하고 열 단위는 Unicode code point다. 위치 없이 이름만으로 정의를 추측하지 않는다.

### 프로젝트 설정

기본적으로 Gradle wrapper와 build root를 찾는다. 명시적으로 지정하려면 `--project /path/to/build`를 사용한다. 선택 설정은 build root의 `.jman.json`에 둔다.

```json
{
  "offline": false,
  "generateTasks": [":app:compileJava"],
  "settings": {
    "java": {
      "configuration": {
        "runtimes": [{"name": "JavaSE-17", "path": "/path/to/jdk17"}]
      }
    }
  }
}
```

- `javaHome`: JDTLS 실행 JDK. 기본은 패키지의 JDK 21.
- `gradleJavaHome`: Gradle 실행 JDK. project toolchain과 별개다.
- `gradleHome`: wrapper가 없을 때 사용할 Gradle 설치 경로.
- `settings`: JDTLS에 전달할 중첩 Java 설정.
- `jman prepare --generate`: 설정한 생성 task를 실행한 뒤 import한다.
- `jman refresh`: 의존성 재해석과 JDTLS 재시작.

Lombok의 getter/builder는 생성 멤버로 표시하고 원본 필드/타입 위치로 안내한다. MapStruct와 QueryDSL은 Gradle annotation processing과 생성 소스 경로를 사용한다. `doctor`에서 import/compile 오류를 확인할 수 있다.

### Skill 설치

사용하는 에이전트가 읽는 skill 디렉터리를 지정한다.

```sh
jman skill install --target /path/to/agent/skills/jman
```

설치된 문서가 다르면 덮어쓰지 않는다. daemon 관리를 skill에 넣을 필요는 없다.

### 상주 서비스

flake의 `homeManagerModules.default`를 import하고 `services.jman.enable = true;`로 설정한다. `services.jman.maxSessions`의 기본값은 2다. 직접 실행할 때는 `jman daemon --max-sessions 2`를 사용한다.

session마다 JVM 최대 heap은 1536 MiB이며, 15분 idle session은 종료한다. 같은 workspace 요청은 직렬화하고 서로 다른 session은 병렬로 처리한다. socket과 캐시는 `JMAN_SOCKET`, `JMAN_CACHE_HOME`으로 격리할 수 있다.

## 벤치마크

```sh
# 실제 독립 저장소들과 로컬 Maven repository 생성
nix run .#bench -- fixture ./local/example

# 원래 코드가 실패하고 기준 수정이 통과하는지 독립 실행 검사
nix run .#bench -- validate --output ./local/oracle-check

# 실제 모델을 사용하는 paired A/B 실행
OPENAI_API_KEY=... nix run .#bench -- run \
  --model YOUR_MODEL --output ./local/experiment \
  --repetitions 5 --cache dependency-warm

nix run .#bench -- report ./local/experiment
```

OpenAI-compatible `/chat/completions` endpoint는 `--base-url`로 바꿀 수 있다. 다른 agent harness는 `--adapter-command`로 연결한다. 실행별 raw events, 수정 diff, 독립 평가 결과, JSON/Markdown report를 남긴다. `--prices rates.json`으로 input/output/cacheRead의 백만 토큰당 가격을 제공하면 추정 비용과 성공당 비용도 계산한다.

현재 실험 suite의 에이전트 과제는 내부 라이브러리 오인 사례다. 별도의 통합 fixture는 Lombok, MapStruct, QueryDSL/JPA, Spring context와 AOP self-invocation을 검증한다. **합성 adapter smoke test는 AI 성능이나 토큰 절감의 증거가 아니다. 실제 모델의 경제성 수치는 아직 측정되지 않았다.**

## 개발과 검증

```sh
nix develop
go test -race ./...
go build -o local/bin/jman ./cmd/jman
python3 tests/integration.py local/bin/jman
python3 tests/bench_test.py local/bin/jman

# 고정된 오프라인 의존성으로 패키지와 통합 검사
nix flake check -L
```

## 분석 범위

- 기본 의미는 compile-time binding이다. references는 현재 import된 workspace의 정적 Java 참조다.
- 인접 저장소의 HEAD를 JAR의 소스로 대체하지 않는다. 실제 Gradle composite substitution은 따로 따라간다.
- sources JAR의 대응은 `coordinate-only` 등으로 표시한다. source/binary의 동일 빌드 provenance를 검증했다고 주장하지 않는다.
- Spring의 실제 bean 선택, proxy 실행 경로, reflection과 동적 JPA query를 일반 Java references가 모두 표현하지는 않는다.
- compile/import 문제가 있으면 `partial`과 diagnostics를 반환한다. 종료 코드 3은 결과가 불완전함을 뜻한다.
- 현재 benchmark shell adapter는 신뢰하는 에이전트를 disposable 환경에서 실행하는 용도다. adversarial sandbox는 제공하지 않는다.

구현 현황과 남은 확장 범위는 [지원 범위](docs/support.md)를 참고한다.

## 설계 문서

- [요구사항과 에이전트 인터페이스](docs/requirements.md)
- [아키텍처와 구현 순서](docs/architecture.md)
- [벤치마크와 경제성 검증](docs/benchmark.md)

핵심 원칙: **빌드가 해석한 의존성을 기준으로 답하고, 출처·분석 범위·불완전성을 함께 전달한다.**

설계 문서의 SHOULD와 후속 단계는 제품의 확장 방향이다. 현재 지원 여부는 위 사용법과 지원 범위 문서를 기준으로 한다.
