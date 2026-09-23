# jman — JDTLS Manager

Java 개발자를 돕는 AI agent가 **현재 호출 지점의 실제 정의와 참조**를 적은 탐색 비용으로 확인하도록 돕는 도구.

주요 대상은 Gradle multi-project / multi-repository 환경과 Spring, Lombok, JPA, QueryDSL, MapStruct를 사용하는 프로젝트다. 특히 현재 classpath의 내부 라이브러리와 저장소에 남아 있는 오래된 동명 코드를 구별하는 것을 우선한다.

## 현재 단계

요구사항과 설계 초안. 아래 명령은 제안 인터페이스이며, 아직 구현된 명령이 아니다. 성능·비용 절감 수치도 아직 측정되지 않았다.

```sh
jman definition app/src/main/java/example/OrderService.java:42 --symbol normalize
jman references app/src/main/java/example/OrderService.java:42 --symbol normalize
jman doctor
```

## 설계 문서

- [요구사항과 에이전트 인터페이스](docs/requirements.md)
- [아키텍처와 구현 순서](docs/architecture.md)
- [벤치마크와 경제성 검증](docs/benchmark.md)

핵심 원칙: **빌드가 해석한 의존성을 기준으로 답하고, 출처·분석 범위·불완전성을 함께 전달한다.**

패키징은 `flake.nix` / `flake.lock` 기반으로 구현한다. 구체적인 패키지와 검증 계약은 아키텍처 문서에 정의한다.
