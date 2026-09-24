# jman

**빌드가 실제로 선택한 Java 심볼을 AI agent와 개발자에게 보여 주는 JDTLS manager.**

`jman`은 호출 위치의 JDT binding과 Gradle의 선택 classpath를 함께 사용한다. 그래서 Gradle multi-project / composite build / 여러 repository 환경에서, 저장소에 남아 있는 오래된 동명 소스가 아니라 **실제로 호출되는 artifact 또는 project source**를 찾는다.

주요 대상은 Spring, Lombok, JPA, QueryDSL, MapStruct를 사용하는 Java/Gradle 프로젝트다.

> 현재 릴리스: **0.1.0** — 지원 기능과 한계는 [지원 범위](docs/support.md)를 기준으로 한다.

## 빠른 시작

Nix flake는 `aarch64-darwin`, `aarch64-linux`, `x86_64-linux`를 제공한다. JDK 21, JDTLS, Lombok agent, Java extension, Gradle을 호환되는 조합으로 고정한다.

```sh
# 빌드 후 실행
nix build .#jman
./result/bin/jman --help

# 설치 없이 현재 프로젝트에서 실행
nix run . -- definition src/main/java/example/OrderService.java:42 --symbol normalize
```

프로젝트 root 또는 그 하위에서 실행한다. 첫 요청이 local daemon과 해당 Gradle build의 JDTLS session을 시작한다.

```sh
jman definition src/main/java/example/OrderService.java:42 --symbol normalize
jman references src/main/java/example/OrderService.java:42 --symbol normalize
jman implementations src/main/java/example/OrderMapper.java:12 --symbol toDto
jman hover src/main/java/example/OrderService.java:42 --symbol normalize
jman deps src/main/java/example/OrderService.java
```

정의 결과에는 해석된 signature, source set, 선택 artifact/version, binary SHA-256, source origin, 짧은 excerpt가 포함된다.

```sh
# 사람이 읽는 상세 provenance
jman definition src/main/java/example/OrderService.java:42 --symbol normalize --explain

# schemaVersion 1 구조화 출력
jman definition src/main/java/example/OrderService.java:42 --symbol normalize --json

# 반환된 source 경로를 더 읽기
jman read /path/from/definition/Source.java --lines 20:60
```

위치의 줄·열은 1부터 시작하며 열은 Unicode code point 기준이다. 같은 줄에 같은 식별자가 반복되면 `--occurrence N` 또는 정확한 열을 지정한다. jman은 위치 없이 이름만 검색해 정의를 추측하지 않는다.

## 명령과 운영

| 명령 | 역할 |
|---|---|
| `definition`, `references`, `implementations`, `hover` | JDT binding 기반 탐색 |
| `deps` | 호출 파일 source set의 Gradle 선택 dependency context |
| `read PATH --lines START:END` | workspace, generated, cached external source 읽기 |
| `prepare [--generate]` | import/index warming; 설정된 generation task는 `--generate`에서만 실행 |
| `refresh` | build/dependency 변경 뒤 재import |
| `doctor` | import, classpath, processor diagnostics |
| `status`, `stop` | local daemon session 확인·종료 |
| `skill install --target DIR` | 배포 skill 설치 |

공통 옵션은 `--project PATH`, `--symbol NAME`, `--occurrence N`, `--limit N`, `--max-bytes N`, `--cursor TOKEN`, `--timeout 120s`, `--json`, `--explain`이다. 전체 옵션은 `jman --help`가 authoritative하다.

기본 daemon은 최대 2개 JDTLS session을 유지하며 15분 idle session을 종료한다. session의 JVM heap 상한은 1536 MiB다. socket/cache를 격리하려면 `JMAN_SOCKET`, `JMAN_CACHE_HOME`을 사용한다.

Home Manager에서는 `homeManagerModules.default`를 import한 뒤 다음처럼 설정할 수 있다.

```nix
services.jman = {
  enable = true;
  maxSessions = 2;
};
```

다른 flake에서 `overlays.default`는 caller의 nixpkgs로 `pkgs.jman`과 `pkgs.jman-bench`를 빌드하고, `overlays.pinned`는 이 flake가 고정한 package를 노출한다.

## 프로젝트 설정

build root는 Gradle settings/build 파일에서 자동 발견한다. 모호하거나 별도 root를 사용할 때 `--project /path/to/build`를 지정한다. 선택 설정은 build root의 `.jman.json`에 둔다.

```json
{
  "offline": false,
  "javaHome": "/path/to/jdk21",
  "gradleJavaHome": "/path/to/jdk17-or-21",
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

- `javaHome`: JDTLS 실행 JDK. package 기본값은 JDK 21이다.
- `gradleJavaHome`: Gradle 실행 JDK이며 project toolchain과는 별개다.
- `gradleHome`: wrapper가 없을 때 사용할 Gradle 설치 root다.
- `generateTasks`: `jman prepare --generate`가 실행할 명시적 task 목록이다.
- `settings`: JDTLS에 전달하는 중첩 Java 설정이다.

Lombok 생성 getter/builder는 생성 멤버로 표시하고 소유 타입/필드 쪽으로 안내한다. MapStruct와 QueryDSL은 Gradle annotation processing이 만든 source를 탐색한다.

## Agent skill

agent가 읽는 skill directory에 배포 skill을 설치한다. 기존 내용이 다르면 덮어쓰지 않는다.

```sh
jman skill install --target /path/to/agent/skills/jman
```

## 정확성 경계

- 기본 의미는 compile-time binding이며, references는 현재 JDTLS가 import한 workspace의 정적 Java 참조다.
- 인접 repository의 HEAD를 dependency JAR source로 자동 대체하지 않는다. Gradle composite substitution은 실제 build 관계를 따라간다.
- sources JAR이 binary와 같은 build임을 증명하지 못할 때 source match를 `coordinate-only` 또는 `unverified`로 표시한다.
- Spring runtime bean/proxy 선택, reflection, 동적 JPA query는 일반 Java references로 완전하게 결정되지 않는다.
- import/build 문제가 있으면 `partial` 결과와 diagnostics를 반환한다. exit code `3`은 불완전하거나 아직 준비되지 않은 결과다.

더 자세한 제공 범위, 종료 코드, benchmark suite/adapter 계약은 [지원 범위](docs/support.md)를 참고한다.

## 벤치마크

`jman-bench`는 baseline과 jman arm을 fresh repository/session/cache에서 paired 실행하고, 종료 후 별도의 evaluator에서 허용된 수정만 적용해 판정한다.

```sh
# bundled internal-library fixture와 local Maven repository 생성
nix run .#bench -- fixture ./local/example

# fixture의 실패 상태와 reference fix 검증
nix run .#bench -- validate --output ./local/oracle-check

# OpenAI-compatible endpoint를 통한 실제 agent trial
OPENAI_API_KEY=... nix run .#bench -- run \
  --model YOUR_MODEL \
  --output ./local/experiment \
  --repetitions 5 \
  --cache dependency-warm

nix run .#bench -- report ./local/experiment
```

`--base-url`과 `--api-key-env`로 OpenAI-compatible provider를 바꾸거나, `--adapter-command`로 별도 agent harness를 연결할 수 있다. 사용자 template, prompt, 허용 edit glob, 독립 evaluator는 `--suite suite.json`으로 제공한다. 형식은 [suite와 adapter 계약](docs/support.md#사용자-프로젝트와-문제)을 참고한다.

실제 모델로 수행한 제한된 3회 paired origin-navigation trial은 **baseline 3/3, jman 3/3 성공**이었고, 합계 non-cache token은 21.4%, wall time은 28.5% 낮았다. 단일 문제·작은 표본이므로 일반 성능 주장으로 해석해서는 안 된다. 방법과 원본 보존 한계는 [trial report](reports/2026-09-24-opencode-jdtls-origin-navigation-report.md)에 기록했다.

## 개발·기여

```sh
nix develop
go test -race ./...
go vet ./...
python3 tests/integration.py local/bin/jman
python3 tests/bench_test.py local/bin/jman
nix flake check -L
```

기여 시 코드, CLI/help, 지원 범위, 검증 기록, benchmark claim을 함께 유지해야 한다. 구체적인 변경 절차와 PR checklist는 [CONTRIBUTING.md](CONTRIBUTING.md)를 참고한다.

## 문서와 라이선스

- [지원 범위](docs/support.md): 현재 제공 기능·한계·suite/adapter 계약
- [검증 기록](docs/validation.md): 재현 가능한 검증과 실제 trial 상태
- [요구사항](docs/requirements.md), [아키텍처](docs/architecture.md), [벤치마크 설계](docs/benchmark.md): 목표와 설계 결정
- [기여 가이드](CONTRIBUTING.md)
- [MIT License](LICENSE)

핵심 원칙: **빌드가 해석한 의존성을 기준으로 답하고, 출처·분석 범위·불완전성을 함께 전달한다.**
