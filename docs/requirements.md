# 요구사항과 에이전트 인터페이스

상태: 0.1 구현 뒤에도 유지하는 제품 요구사항이다. 이 문서의 MUST/SHOULD는 설계 목표와 수용 기준이며, 모두 구현됐다는 선언은 아니다. 실제 제공 기능·알려진 경계는 [지원 범위](support.md), 실행한 검사는 [검증 기록](validation.md)을 기준으로 한다.

## 1. 제품 목표

에이전트가 다음 질문에 적은 도구 호출과 짧은 응답으로 정확히 답하게 한다.

1. 이 호출은 어떤 타입의 어떤 오버로드에 연결되는가?
2. 그 정의는 현재 소스인가, 생성 코드인가, 어떤 버전의 의존성인가?
3. 실제 구현을 읽을 수 있는가? 읽고 있는 것이 원본인가, 디컴파일 결과인가?
4. 이 심볼을 바꾸면 현재 분석 범위에서 어디가 영향을 받는가?
5. 이 답은 어떤 빌드·파일 상태를 기준으로 했으며 어디까지 확실한가?

제품의 성과는 LSP 기능 수보다 잘못된 수정 감소와 **정답을 얻는 데 쓰는 전체 비용**으로 평가한다.

## 2. 인터페이스 선택

**기본은 일반적인 CLI + 짧은 skill 문서**로 한다. 에이전트가 이미 쓰는 shell, 파일 경로, 줄 번호, 코드 읽기 방식을 그대로 활용한다.

- 공개 명령은 `jman` 하나를 중심으로 구성한다. daemon/worker 구분은 일반 사용에 노출하지 않는다.
- 기본 stdout은 간결한 텍스트, `--json`은 버전이 있는 구조화 응답이다. TTY 유무로 형식을 바꾸지 않는다.
- 로그와 진행 상황은 stderr에 쓴다. ANSI 색상은 비대화형 기본 출력에 넣지 않는다.
- 실행 위치에서 프로젝트를 추론한다. 모호하면 후보를 보여 주고 `--project`로 지정하게 한다.
- 첫 요청이 daemon과 필요한 JDTLS를 시작한다. 매 세션 초기화 명령을 학습할 필요가 없어야 한다.
- MCP는 후속 어댑터 후보다. 같은 질의 엔진을 호출하며 CLI와 의미를 공유한다. 도입 효과는 별도로 측정한다.

자연어 질의 해석기를 도구 내부에 넣지 않는다. 코드 이해를 하는 LLM과 의미 탐색 엔진의 역할을 명확하게 유지한다.

### 2.1 핵심 명령

| 명령 | 목적 | 우선순위 |
|---|---|---|
| `definition LOCATION` | 실제 선언, 출처, 짧은 코드 조각 | MUST |
| `references LOCATION` | 해당 심볼의 참조와 검색 범위 | MUST |
| `implementations LOCATION` | 정적 타입 기반 구현 후보 | MUST |
| `hover LOCATION` | 해석된 타입, 시그니처, 짧은 문서 | MUST |
| `read TARGET` | 외부/생성 소스를 포함한 범위 읽기 | MUST |
| `status` / `doctor` | 준비 상태 / 원인과 복구 방법 | MUST |
| `prepare` | 미리 import하고 준비 상태 확인 | MUST |
| `refresh` | 의존성·빌드 모델 재해석 | MUST |
| `symbols QUERY` | 위치를 모를 때 후보 탐색 | SHOULD — 미구현 |
| `callers` / `callees` | 정적 호출 관계 | SHOULD — 미구현 |
| `deps` | 선택 version·variant·artifact 상세 조회 | 구현됨 |

`symbols` 결과는 발견 후보이지 호출 바인딩의 증거가 아니다.

### 2.2 위치 입력: 열 번호 계산을 강요하지 않는다

```sh
jman definition app/src/main/java/example/OrderService.java:42 --symbol normalize
jman definition app/src/main/java/example/OrderService.java:42:27
jman references app/src/main/java/example/OrderService.java:42 --symbol normalize --limit 20
```

- 줄과 열은 1부터 시작한다. 공개 열 단위는 Unicode code point이고 내부 LSP 인코딩으로 변환한다.
- `--symbol`은 해당 줄의 Java 식별자 토큰을 선택한다. 이름으로 전체 저장소를 검색하는 옵션이 아니다.
- 같은 줄에 같은 식별자가 여러 번 있으면 열 위치와 주변 코드를 반환한다. `--occurrence N` 또는 정확한 열로 선택한다.
- 선택자가 없고 대상이 모호하면 임의의 심볼을 선택하지 않는다.
- 절대 경로, 프로젝트 상대 경로, 공백 포함 경로를 지원한다. 파일과 위치를 분리하는 `--file`, `--line`, `--column`도 제공한다.
- 디스크에 저장된 파일을 기본 분석 대상으로 삼는다. 외부 에디터의 미저장 buffer 지원은 별도 범위다.

### 2.3 기본 응답은 작은 증거 묶음

예시 출력이며 실제 값은 아니다.

```text
com.acme.text.TextUtil.normalize(String): String
origin: dependency com.acme:shared-text:2.4.1 [sources-jar]
context: :app / main / compileClasspath
binding: resolved; source-match: coordinate-only
source: /home/user/.cache/jman/sources/<digest>/com/acme/text/TextUtil.java:31
31 public static String normalize(String value) {
32     return value.strip().toLowerCase(Locale.ROOT);
33 }
```

기본 응답에 MUST로 포함할 의미:

- 타입과 오버로드를 구별하는 시그니처.
- `workspace-source`, `dependency-source`, `generated-source`, `decompiled`, `binary-only` 출처 종류.
- 의존성이면 확인된 좌표와 선택 버전. 확인할 수 없는 좌표는 추측하지 않는다.
- 호출자의 Gradle project / source set / 해석된 classpath 문맥.
- 정의 위치와 기본 최대 20줄의 관련 코드. 큰 메서드는 잘림을 표시한다.
- 해석 실패, 소스 대응 미검증, 불완전한 결과 등 답의 사용에 영향을 주는 상태.

동명 후보 전체와 전체 dependency graph는 기본 응답에 넣지 않는다. `--explain`으로 바인딩 근거, binary digest, variant, source 대응 근거와 비교 후보를 요청한다.

외부 소스는 읽기 전용 캐시 파일로 제공해 기존 파일 읽기 도구로도 볼 수 있게 한다. 현재 `read TARGET --lines 31:70`은 반환된 경로를 받는다. snapshot에 귀속되는 별도 source handle과 만료 계약은 아직 구현하지 않았다.

### 2.4 구조화 응답과 실패 계약

JSON의 공통 필드는 `schemaVersion`, `status`, `query`, `context`, `snapshot`, `results`, `coverage`, `warnings`, `nextCursor`다. 오류에는 `error.code`, `error.message`, `error.retryable`, `error.nextAction`을 사용한다.

- `status`: `ok`, `partial`, `not-found`, `ambiguous`, `not-ready`, `error`.
- 종료 코드: 0은 완료된 질의(`ok`, `not-found`), 2는 입력 오류/모호함, 3은 준비 미완료/partial, 4는 실행 실패.
- `not-found`는 해당 범위에서 준비된 분석이 끝나고도 결과가 없는 경우다. import 실패를 빈 결과로 바꾸지 않는다.
- `--timeout`은 기동·대기열·동기화·질의를 포함한 전체 deadline이다. timeout은 `not-ready` 또는 실행 중 timeout 오류로 구분한다.
- 기본 결과 수와 코드 크기는 제한한다. `--limit`, `--max-bytes`, cursor로 확장한다. 토큰 수는 모델별이므로 byte 제한을 토큰 보장으로 표현하지 않는다.
- cursor는 snapshot에 묶는다. 파일/빌드가 바뀌면 새 질의를 요구하며 다른 상태의 페이지를 섞지 않는다.
- 검색 범위의 불완전성과 출력 페이지 잘림은 별개다. `coverage`와 `nextCursor`로 구분한다.

## 3. 정확성 요구사항

### R1. 호출자 기준 바인딩

MUST: import 문자열, FQN, 디렉터리 유사도가 아닌 호출자 compilation unit의 binding을 기준으로 정의를 선택한다. 동일 이름, 오버로드, 상속, test/main source set 차이를 다룬다.

수용 사례: 저장소에 과거의 `TextUtil`이 남아 있지만 Gradle이 내부 JAR의 클래스를 선택하는 경우, 응답과 코드 조각은 실제 선택된 JAR에 대응해야 한다. 과거 코드를 정답으로 반환하면 실패다.

### R2. binary와 source의 출처를 구분

MUST: 실제 classpath entry와 binary digest를 추적한다. source JAR 좌표 일치만으로 binary와 정확히 동일한 빌드의 소스라고 단정하지 않는다. source 대응 근거를 `verified`, `coordinate-only`, `unverified`, `missing`으로 구별한다. 현재 구현은 `coordinate-only`, `unverified`, `missing`만 생성하며, 검증된 source provenance registry는 아직 없다.

`verified`는 신뢰할 수 있는 빌드 provenance 등으로 binary와 source 쌍이 확인된 경우에만 사용한다. JAR의 `pom.properties`는 보조 자료이며 shaded JAR의 선택 버전 근거로 단독 사용하지 않는다.

MUST: source가 없으면 디컴파일임을 표시하고, 디컴파일도 불가능하면 시그니처와 binary 위치를 반환한다. 디컴파일 줄 번호를 원본 파일 줄 번호로 표시하지 않는다.

MUST: 옆 저장소의 HEAD를 의존성 소스로 자동 대체하지 않는다. Gradle composite build / dependency substitution이 실제 선택했거나 명시적인 provenance가 있을 때 연결한다. 단순 사용자 경로 매핑은 검증된 대응과 구별한다.

SNAPSHOT이나 동일 좌표 재발행을 고려해 캐시 키에 binary/source 내용 digest를 포함한다.

### R3. Gradle 모델 충실도

MUST: wrapper, multi-project, composite build, version catalog, dependency constraints, lockfile, source set, 선택 variant 및 로컬 JAR을 고려한다. 모든 것을 별도 Gradle 파서로 재구현하지 않고 Gradle/Buildship 해석 결과를 사용한다.

JDT 모델과 실제 Gradle classpath가 맞지 않으면 degraded 상태와 차이를 알린다. compile-time binding과 runtime classpath 선택은 다른 질문이며 기본 탐색은 전자를 답한다.

### R4. Lombok / annotation processing

MUST: Lombok getter/setter, builder, 생성 constructor의 사용을 fixture로 검증한다. 생성 멤버가 실제 소스 위치를 갖지 않으면 소유 타입·필드·annotation으로 연결하되 이를 생성 멤버의 본문이라고 표현하지 않는다.

MUST: MapStruct 구현과 QueryDSL Q 타입의 generated source root를 인식한다. 생성 전/실패/오래된 생성물 상태를 구분한다. 필요한 Gradle task와 처리기 상태를 알려 준다.

`prepare --generate`는 명시적으로 설정된 생성 task를 실행한다. 일반 탐색마다 전체 Gradle build를 실행하지 않는다. JDTLS import/자동 빌드가 수행하는 annotation processing도 상태와 비용 기록에 포함한다.

### R5. 다중 저장소와 references의 범위

MUST: 기본 references는 현재 build workspace에서 인덱싱된 소스 범위로 한정하고 그 범위를 응답에 명시한다. 알려지지 않은 downstream 저장소까지 검색했다고 주장하지 않는다.

SHOULD: 등록된 workspace 집합에서 cross-repository references를 모을 수 있다. 대상 라이브러리의 binary identity와 각 consumer의 선택 버전을 비교한다. 같은 FQN의 다른 버전 사용은 버전 간 후보로 분리한다.

### R6. 상태 변화와 동시 실행

MUST: 파일 수정, 추가/삭제, branch 전환, build script/lockfile 변경을 반영한다. 결과에는 파일 내용 및 build model revision을 추적할 snapshot이 있어야 한다.

MUST: 감시 이벤트만 신뢰하지 않고 질의 대상 파일을 재확인한다. 질의 도중 상태가 바뀌면 재시도하거나 partial/stale로 알린다. 특히 references의 전체 범위 최신성은 대상 파일 하나의 hash만으로 보장하지 않는다.

MUST: 별도 Git worktree는 같은 branch/commit이어도 인덱스와 JDTLS data directory를 공유하지 않는다.

### R7. Spring의 정적 분석 경계

MUST: `implementations`는 타입 계층 후보이며 실제 주입 bean 선택이라고 표시하지 않는다. Spring AOP proxy, 조건부 bean, reflection, JPA derived query는 일반 Java references만으로 완전하게 표현되지 않음을 해당 질의에서 명시한다.

SHOULD: 후속 Spring 확장은 `@Qualifier`, `@Primary`, profile, pointcut 같은 근거를 별도 관계로 제공한다. 프레임워크 추론과 compiler binding을 응답에서 구별한다.

## 4. Skill의 역할

짧은 공통 skill에 다음만 가르친다.

1. 호출 구현이나 수정 영향 범위를 판단할 때 `definition` / `references`를 호출한다.
2. 파일·줄과 이미 읽은 식별자를 전달한다.
3. 결과의 origin과 coverage를 확인하고 필요한 부분만 추가로 읽는다.
4. 모호함/준비 실패가 있으면 반환된 next action이나 `doctor`를 사용한다.
5. 외부/생성 소스는 출처를 확인하고 수정 대상은 실제 소유 저장소·생성 입력에서 찾는다.

운영 설치 설명, 긴 JSON schema, JDTLS 내부 용어를 매번 읽는 skill에 넣지 않는다. skill 자체의 토큰도 벤치마크 비용에 포함한다.
