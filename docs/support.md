# 지원 범위와 재현 방법

## 0.1 구현

| 영역 | 구현 |
|---|---|
| 사용자 인터페이스 | CLI, JSON schemaVersion 1, skill installer |
| 운영 | Unix socket daemon, lazy start, session 제한과 idle eviction, user service |
| 탐색 | definition, references, implementations, hover, source read, deps |
| 출처 | JDT codeSelect, 실제 classpath binary, digest, Gradle 선택 component/variant |
| 변경 반영 | 파일 내용 fingerprint, watched-file notifications, document lifecycle/index barrier |
| 페이지 | snapshot과 query에 귀속된 cursor; 변경 후 재사용 거부 |
| 생성 코드 | Lombok agent, 생성 멤버의 소유 위치, MapStruct/QueryDSL 생성 파일 |
| 패키징 | flake와 lock, Java/Go/Python toolchain, 고정된 fixture artifact closure |
| 실험 | 두 실험군, 새 저장소/session/cache, 반복 순서 randomization, 독립 compiler/runtime evaluator |
| 분석 | raw provider usage, missing-usage 구분, 성공률/시간/토큰/선택 가격표 비용, paired 반복 차이 |

프로젝트 크기에 따라 매 질의의 전체 입력 fingerprint 비용이 커질 수 있다. 현재는 mtime만 신뢰해서 잘못된 결과를 내는 것보다 내용 기반 확인을 우선한다. JVM 수 기본 2와 heap 상한은 프로세스 RSS 총량의 hard limit이 아니다.

## 정직하게 구분하는 경계

1. **binding과 source 대응:** binary 선택을 확인해도 sources JAR이 같은 빌드의 소스임을 증명한 것은 아니다.
2. **정적 분석과 런타임:** implementation 후보와 실제 Spring bean 선택은 다르다.
3. **프로젝트 오류와 빈 결과:** 빌드 모델 또는 binding 실패는 `partial`/오류다.
4. **범위와 페이지 잘림:** import된 workspace 밖의 consumer는 포함하지 않는다. 다음 페이지는 `nextCursor`다.
5. **fixture 검증과 경제성 검증:** compiler/runtime smoke test와 scripted adapter로 LLM의 비용 절감을 주장하지 않는다.

## 아직 제공하지 않는 확장

- MCP adapter, 자연어 질의, editor의 미저장 buffer.
- 독립적으로 등록한 모든 repository에 걸친 references 집계.
- Gradle artifact와 저장소 commit을 연결하는 검증된 source provenance registry.
- Spring 조건부 bean/profile/pointcut 분석과 runtime tracing.
- JPA 실행 SQL을 연결하는 분석.
- 임의의 사용자 annotation processor에 대한 호환성 보장.
- workspace 크기에 따른 동적 heap 조정, 전체 프로세스 RSS hard limit, cache garbage collection.
- benchmark의 adversarial sandbox, 여러 과제군의 확증적 통계, 실제 모델의 공개 결과 데이터.

`docs/requirements.md`의 최초 설계에는 이들 중 일부에 대한 더 넓은 목표가 포함되어 있다. 이 문서는 실제 제공하는 범위를 설명하며 그 목표의 달성을 대신 선언하지 않는다.

## 결과 코드

| 종료 코드 | 의미 |
|---|---|
| 0 | 완료 (`ok` 또는 준비된 분석의 `not-found`) |
| 2 | CLI 입력/위치 모호함 |
| 3 | `partial` 또는 `not-ready` |
| 4 | 프로토콜·실행·cursor 오류 |

`--max-bytes`는 소스 excerpt의 예산이다. JSON envelope, diagnostics, provenance까지 포함한 전체 응답 크기의 hard limit은 아니다. `--limit`은 페이지 결과 수를 제한한다.

## Benchmark adapter

### 사용자 프로젝트와 문제

`run --suite /path/to/suite.json`으로 기본 예제 대신 사용자 template과 문제를 입력한다.

```json
{
  "template": "./template",
  "project": "application",
  "repositories": ["application", "shared-library"],
  "prompt": "문제 설명과 수정 요구사항",
  "allowedEdits": ["src/main/java/*"],
  "prepare": ["./gradlew", "classes"],
  "evaluate": ["python3", "{suite}/evaluate.py"],
  "explanationTerms": ["expected-artifact", "expected-version"]
}
```

template은 manifest 기준 상대 경로다. project는 template 내부의 build/repository root이고 repositories에 포함되어야 한다. 원본 `.git`, `.gradle`, `build`는 복사하지 않고 새로운 독립 Git 저장소를 만든다. build output과 IDE metadata는 template의 `.gitignore`에서 제외한다.

평가기는 에이전트가 종료한 뒤 새 template에 허용된 변경만 복사한 환경에서 실행한다. 작업 디렉터리와 `JMAN_EVAL_PROJECT`가 평가할 프로젝트를 가리키며, 종료 코드 0을 성공으로 판정한다. `{suite}`는 manifest 디렉터리로 치환되어 평가 코드가 agent workspace 밖에 있게 한다. `allowedEdits`는 프로젝트 상대 경로의 glob이다.

### 외부 harness 연결

`--adapter-command`의 프로세스는 `JMAN_BENCH_INPUT` 경로의 JSON을 읽는다.

```json
{
  "schemaVersion": 1,
  "project": "/run/workspace/commerce",
  "prompt": "...",
  "deadlineSeconds": 600,
  "maxTokens": 200000,
  "model": "model-id",
  "output": "/run/adapter-result.json"
}
```

지정된 output에 결과를 쓴다. usage를 모르면 `null`을 사용한다.

```json
{
  "reason": "completed",
  "answer": "...",
  "usage": {"input": 12000, "output": 1500, "cacheRead": 2000, "reasoning": 500},
  "usageSource": "provider-or-harness-name"
}
```

input은 cacheRead를 포함하고 output은 reasoning을 포함하는 규약이다. provider의 다른 정의는 adapter에서 변환하고 raw event를 보존해야 한다. runner는 별도 agent의 내부 요청을 자동으로 관찰할 수 없으므로 adapter가 완전한 집계와 원본 기록을 책임진다.

가격 파일의 단위는 USD / 백만 토큰이다.

```json
{"input": 1.0, "output": 4.0, "cacheRead": 0.1}
```

이는 입력 예시이며 특정 모델의 현재 가격이 아니다. 모델 요청 실패, deadline과 불완전한 usage도 report에 남긴다. API key는 이벤트에 저장하지 않고 내장 agent의 shell 환경에서도 제거한다. 외부 adapter에는 모델 호출을 위해 인증 환경을 전달하므로 해당 harness가 tool 환경과 기록의 인증 정보 분리를 책임진다.
