# Contributing to jman

jman changes must keep implementation, user contract, and evidence aligned. Its core promise is simple: resolve the symbol selected by the build, not a plausible file with the same name.

Contributions are provided under the [MIT License](LICENSE).

## Start here

```sh
nix develop
go test -race ./...
go vet ./...

go build -o local/bin/jman ./cmd/jman
python3 tests/integration.py local/bin/jman
python3 tests/bench_test.py local/bin/jman
nix flake check -L
```

`nix flake check -L` runs Go tests, benchmark-runner tests, and real JDTLS/Gradle integration tests. Fixture dependencies are fixed by Nix and integration tests use Gradle offline mode.

## Documentation ownership

Keep documents short, single-purpose, and non-overlapping. Before adding text, decide which document owns the fact. Link to that document instead of copying the fact elsewhere.

| Document | Owns | Does not own |
|---|---|---|
| `README.md` | Installation, first use, minimal configuration, essential limits | Development workflow, internals, benchmark method |
| `CONTRIBUTING.md` | Maintenance rules, document boundaries, test and PR expectations | User tutorial or product claims |
| `docs/support.md` | Shipped behavior, exit codes, explicit limits, suite/adapter contracts | Future plans |
| `docs/validation.md` | Tests actually run and observed trials | Product requirements or broad performance claims |
| `docs/requirements.md` | Product goals and acceptance criteria | The sole statement of shipped behavior |
| `docs/architecture.md` | Design and implementation structure | Getting-started instructions |
| `docs/benchmark.md` | Benchmark hypotheses, fixtures, oracles, and method | The only record of observed results |
| `skills/jman/SKILL.md` | Minimal agent instructions and safety rules | Internal implementation detail |
| `reports/` | A specific experiment's method, results, and limits | General product claims |

If documents disagree, `docs/support.md` defines current support and `docs/validation.md` defines what was actually measured.

### Keep maintenance sustainable

- Keep one detailed explanation for each fact; use links elsewhere.
- Keep README limited to getting started. Move maintenance detail here or to `docs/`.
- Update documentation only where a behavior, contract, or evidence change requires it. Avoid unrelated rewrites.
- Do not publish unmeasured accuracy, cost, or performance claims.
- Update code, help text, schemas, and tests before documenting machine-checkable behavior.
- Add a new document only when no existing owner fits; otherwise improve the existing document.

## Synchronize changes

Review these items with the implementation change.

| Change | Also review |
|---|---|
| CLI command, option, exit code, JSON field | CLI help, tests, `README.md`, `docs/support.md`, and the skill if needed |
| JDT binding, provenance, source origin | Extension/integration tests, `docs/support.md`, `docs/architecture.md` |
| Gradle model, source set, composite behavior | `gradle/model.gradle`, integration fixtures, `docs/support.md`, `docs/benchmark.md` |
| Lombok, MapStruct, QueryDSL, Spring behavior | `fixtures/stack`, integration tests, `docs/validation.md`, support boundary |
| Nix package, runtime version, platform | `flake.nix`, `nix/packages/`, lock file, README, validation record |
| Benchmark runner, usage/cost calculation, evaluator | `bench/`, runner tests, `docs/support.md`, `docs/benchmark.md` |
| Accuracy, performance, or cost claim | A report in `reports/`, `docs/validation.md`, and any README summary |

`jman --help` is authoritative for option spelling.

## Code and test rules

- Run `gofmt -w cmd internal` for Go changes; keep `go test -race ./...` and `go vet ./...` clean.
- Resolve symbols from the caller binding and selected classpath, never from filename, FQN, repository name, or a nearby checkout.
- Keep binary provenance separate from source provenance. A matching source JAR coordinate is not proof of an identical source build.
- Preserve canonical paths, deadlines, cancellation, bounded responses, isolated sessions, and snapshot-bound cursors.
- Do not commit credentials, private source, caches, generated output, IDE metadata, build output, API logs, or temporary trial data.
- Pin hashes for external test artifacts. Tests must not require Maven Central or a private Nexus service.
- Treat Spring runtime selection, reflection, and dynamic queries as runtime behavior unless a test proves a narrower static claim.

Prefer the narrowest test that proves the change:

1. Go unit tests for protocol, parsing, state, pagination, or formatting.
2. Real JDTLS integration tests for bindings, Gradle classpaths, generated sources, or composite builds.
3. An independent compile/runtime oracle for behavior and agent-task claims.

`fixtures/commerce` tests dependency provenance. `fixtures/stack` tests Lombok, MapStruct, QueryDSL/JPA, and Spring AOP. Generated output must be produced from tracked fixture inputs.

## Benchmark claims

An accuracy, token, time, or cost claim needs:

1. A committed report with model, harness, prompt, arms, repetitions, cache state, evaluator, and limitations.
2. The retention location for raw events and a statement of what is not committed.
3. Independent evaluation of changed code; jman output is not the oracle.
4. A clear distinction between observed results, calculated prices, and synthetic tests.

Missing usage is missing; never replace it with zero or estimate it from text length.

## Pull request checklist

- [ ] Scope is focused and unrelated work was preserved.
- [ ] Code is formatted and relevant tests pass.
- [ ] `nix flake check -L` passes, or its limitation is explained.
- [ ] The documents named in the synchronization table were reviewed.
- [ ] Documentation has one owner per fact and no unnecessary duplication.
- [ ] New behavior has an appropriate unit or real JDTLS/Gradle test.
- [ ] The diff contains no generated files, caches, credentials, or evaluator secrets.
- [ ] Benchmark statements link to evidence and state their limits.

## Security reports

Do not put credentials, private source, internal artifact coordinates, or tokens in public issues, fixtures, reports, or logs. Use the repository host's private security-report channel when available; otherwise ask a maintainer for a private contact path.
