# Benchmark design

The benchmark measures both navigation and whether an agent completes a task correctly. Runner behavior and adapter schemas are defined in [support](support.md); observed results are listed in [validation](validation.md) and `reports/`.

## Hypotheses

1. jman reduces wrong conclusions caused by stale same-name source.
2. It can reduce tokens, tool work, time, or cost without reducing task success.
3. Initial import costs may make jman slower for a single short task and pay off only across repeated work.

## Fixtures

The default fixture creates fresh `commerce` and `internal-text` Git repositories plus a local Maven repository containing `shared-text:2.4.1`. `commerce` contains a stale same-FQN source. The `stack` fixture validates Lombok, MapStruct, QueryDSL/JPA, and Spring AOP integration; it is not currently a default agent task.

```text
fixtures/
  commerce/       Gradle application and stale source
  internal-text/  actual library source
  stack/          generated-code and Spring fixture

<experiment>/<repetition>-<arm>/
  workspace/             agent workspace
  evaluation-workspace/  fresh workspace for allowed edits only
  cache/                 isolated Gradle and jman cache
  events.jsonl
  patch.diff
  result.json
```

The local Maven repository models artifact resolution without requiring a live Nexus server. Authentication, HTTP failures, and repository policy are separate future scenarios.

## Scenarios and oracles

| Scenario | Failure mode | Independent oracle |
|---|---|---|
| Split internal library | Stale source and selected JAR share an FQN | Gradle-selected digest and runtime test |
| Version conflict | Declared version differs from selected version | Gradle resolution and behavior |
| Main/test split | Source-set classpaths differ | Per-source-set compile/test oracle |
| Lombok | Generated getter, builder, constructor | Gradle compile and behavior |
| MapStruct | Interface differs from generated mapper | Processor output and mapping test |
| QueryDSL/JPA | Generated Q type and source input | Generated artifact and integration test |
| Composite build | Project substitutes a module | Gradle component and behavior |
| Missing sources | No source JAR or mismatched source | Binary behavior and source-origin label |
| Spring AOP | Static call differs from proxy runtime | Spring context test |

The current agent task covers the split-library case. Do not generalize its result to every row.

## Arms and controls

- **Baseline:** normal file, search, shell, Gradle, and source access; no jman command or skill.
- **jman:** the same environment plus packaged jman and its short skill.
- **General LSP control:** planned, not implemented.

Use the same model, prompt, starting revision, dependencies, time budget, token budget, and sampling configuration for paired arms. Randomize execution order. Give each run a fresh agent session, repository, daemon, and cache. Do not hide ordinary Gradle/JAR tooling from baseline.

## Evaluation

Correctness comes from a fixture-defined oracle, never from a jman response.

1. Verify selected component, binary, signature, and source kind against Gradle and bytecode evidence.
2. Apply only allowed edits to a fresh evaluation workspace and run an independent compile/runtime test.
3. Check requested explanation facts separately from behavioral success.

Evaluator code and hidden data must be outside the agent workspace. Failed and timed-out runs remain in the report.

## Metrics

Record task success, incorrect dependency selection, disallowed edits, wall time, tool calls, input/output/cache-read/reasoning usage, and an explicit-price estimate when rates are supplied. Preserve provider usage; unavailable usage remains unavailable.

Report all attempts and successful attempts separately. `cost per success` is total measured cost divided by successful runs; it is undefined with no successes. Do not convert CPU or memory into currency without a stated model.

The runner reports arm success, median time, complete token totals, paired time deltas, and a simple bootstrap interval when applicable. It samples daemon-tree RSS and CPU, but may miss detached Gradle daemons and short-lived processes.

## Cold and warm modes

Report these separately:

1. Dependency cache cold and JDTLS cold.
2. Dependency cache warm and JDTLS cold.
3. Dependency cache warm and JDTLS warm.

Record preparation time even when excluding it from warm-query comparisons. Do not confuse a warm service with an agent's remembered conversation.

## Run

```sh
jman-bench fixture ./local/example
jman-bench validate --output ./local/oracle-check
jman-bench run --model YOUR_MODEL --output ./local/experiment \
  --arms baseline,jman --repetitions 5 --cache dependency-warm
jman-bench report ./local/experiment
```

Custom suites provide template, project, repositories, prompt, allowed edits, and evaluator argv with `--suite suite.json`. See [support](support.md) for the complete schema and adapter contract.

## Interpreting results

Start with pilot runs, then fix the repetition count and primary metric before a broader experiment. Report improvements, regressions, failures, timeouts, cold-start cost, and missing measurements. A small task suite is evidence about that suite, not all Java work.
