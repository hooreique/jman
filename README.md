# jman

**Gradle이 실제로 선택한 Java 심볼을 찾는 JDTLS manager.**

`jman`은 호출 위치의 JDT binding과 Gradle classpath를 함께 사용한다. 따라서 multi-project, composite build, 여러 repository 환경에서 인접한 오래된 동명 소스가 아니라 실제 artifact 또는 project source를 찾는다.

## 설치

Nix flake는 `aarch64-darwin`, `aarch64-linux`, `x86_64-linux`를 제공한다.

```sh
# 빌드 후 실행
nix build .#jman
./result/bin/jman --help

# 설치 없이 현재 프로젝트에서 실행
nix run . -- definition src/main/java/example/OrderService.java:42 --symbol normalize
```

첫 요청은 local daemon과 해당 Gradle build의 JDTLS session을 시작한다.

## 기본 사용법

프로젝트 root 또는 그 하위에서 실행한다.

```sh
# 실제 정의와 artifact provenance
jman definition src/main/java/example/OrderService.java:42 --symbol normalize

# 현재 import된 workspace의 정적 참조/구현 후보
jman references src/main/java/example/OrderService.java:42 --symbol normalize
jman implementations src/main/java/example/OrderMapper.java:12 --symbol toDto

# type/documentation, 선택된 dependency, workspace 상태
jman hover src/main/java/example/OrderService.java:42 --symbol normalize
jman deps src/main/java/example/OrderService.java
jman doctor
```

결과에는 해석된 signature, source set, 선택 artifact/version, binary SHA-256, source origin, 짧은 excerpt가 포함된다.

```sh
# 상세 provenance 또는 schemaVersion 1 JSON
jman definition src/main/java/example/OrderService.java:42 --symbol normalize --explain
jman definition src/main/java/example/OrderService.java:42 --symbol normalize --json

# 반환된 source 경로 읽기
jman read /path/from/definition/Source.java --lines 20:60
```

줄·열은 1부터 시작하며 열은 Unicode code point 기준이다. 같은 줄에 같은 식별자가 반복되면 `--occurrence N` 또는 정확한 열을 지정한다.

## 프로젝트 설정

build root는 Gradle settings/build 파일에서 자동 발견한다. 필요하면 `--project /path/to/build`를 지정한다. 선택 설정은 build root의 `.jman.json`에 둔다.

```json
{
  "offline": false,
  "javaHome": "/path/to/jdk21",
  "gradleJavaHome": "/path/to/jdk17-or-21",
  "generateTasks": [":app:compileJava"]
}
```

`jman prepare --generate`는 `generateTasks`만 실행한 뒤 import한다. build/dependency 변경 뒤에는 `jman refresh`를 사용한다.

## Agent skill

```sh
jman skill install --target /path/to/agent/skills/jman
```

기존 skill 내용이 다르면 덮어쓰지 않는다.

## 중요한 한계

- 기본 의미는 compile-time binding이며 references는 현재 import된 workspace의 정적 Java 참조다.
- dependency JAR의 source로 인접 repository HEAD를 자동 대체하지 않는다. Gradle composite substitution은 실제 build 관계를 따른다.
- Spring runtime bean/proxy 선택, reflection, 동적 JPA query는 일반 Java references만으로 완전하게 결정되지 않는다.
- import/build 문제가 있으면 `partial` 결과와 diagnostics를 반환한다.

현재 지원 범위·종료 코드·benchmark suite/adapter 계약은 [docs/support.md](docs/support.md)를 참고한다.

## 더 보기

- [기여 및 문서 유지 가이드](CONTRIBUTING.md)
- [검증 기록과 실제 benchmark trial](docs/validation.md)
- [MIT License](LICENSE)
