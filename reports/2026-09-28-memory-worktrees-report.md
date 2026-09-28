# Memory usage across simultaneous worktrees

Resource measurements for [issue #1](https://github.com/hooreique/jman/issues/1),
using the [deterministic benchmark](../docs/benchmark.md#resource-measurement).
Detailed timings, process breakdowns, and artifact hashes are in the
[trial data](2026-09-28-memory-worktrees-results.json).

## Workload and environment

- [RxJava v3.1.12](https://github.com/ReactiveX/RxJava/tree/2e066505a104f3326bb31a960047b0c183b3d027):
  1,884 tracked Java files, one Gradle project, three source-set contexts.
- Linux x86_64, NixOS kernel 6.18.50; Ryzen 7 1800X (8 cores/16 threads),
  31 GiB physical RAM, 15 GiB swap.
- Nix lock `8549668`; JDTLS 1.60.0, OpenJDK 21.0.12.1 runtime,
  Gradle wrapper 8.14, Temurin 11.0.32 project toolchain; four Gradle workers.
- JDTLS heap: initial 128 MiB, maximum 1,536 MiB. Dependencies were seeded;
  JDTLS workspaces and worktree build directories started empty.

Upstream `compileJava` and `compileTestJava` passed; `FlowableMapTest` ran 28
tests with one skipped and no failures. JDTLS initially reported 18 syntax
diagnostics because a Java 9 module descriptor entered its Java 8 source set.
The [classpath adjustment](../bench/workloads/rxjava-jdtls.gradle) excludes that
Eclipse source entry without changing Gradle compilation/test inputs. Adjusted
import and the [definition, references, and hover queries](../bench/workloads/rxjava.json)
passed preflight with complete coverage and no diagnostics.

## Reproduction

```sh
git clone https://github.com/ReactiveX/RxJava.git local/rxjava
git -C local/rxjava checkout 2e066505a104f3326bb31a960047b0c183b3d027
cat bench/workloads/rxjava-jdtls.gradle >> local/rxjava/build.gradle
git -C local/rxjava add build.gradle
git -C local/rxjava commit -m 'Exclude module descriptor from Java 8 Eclipse sources'
```

Export `BUILD_WITH_11=true` and prepare a dedicated Gradle home's `gradle.properties`:

```properties
org.gradle.java.installations.paths=/absolute/path/to/temurin-11
org.gradle.workers.max=4
```

Using that home and RxJava's wrapper, run `compileJava compileTestJava` and
`test --tests io.reactivex.rxjava3.internal.operators.flowable.FlowableMapTest`.
Run `jman prepare` to warm source attachments, then stop its jman and Gradle
daemons. Within `nix develop`:

```sh
jman-bench resources --repository local/rxjava --ref HEAD \
  --output local/memory-concurrent --sessions 1,3,5 --repetitions 3 \
  --startup concurrent --queries bench/workloads/rxjava.json \
  --gradle-home-seed /absolute/path/to/seed --refresh-rounds 0 --timeout 600
```

Repeat with `--startup sequential` and a new output directory. The measured
prepared tree was `8fa702a55ee3ea9c0250de215b4d1456eaad473d`.

## Main matrix

Each mode ran 1/3/5 sessions in order, repeated three times. Navigation ran
concurrently: three first queries per worktree, then three warm rounds.
The first query triggered reimport because generated `.classpath` metadata
changed the build fingerprint; that cost is included separately below.
Sampling used a 200 ms interval plus collection time and a three-second steady
window. All **18 trials and 648 navigation commands passed**, with the requested
sessions ready and distinct live JDTLS PIDs.

Peaks are maxima across three trials; steady and after-stop values are medians
of per-trial sample means. RSS/PSS peaks may occur at different times.
After-stop memory belongs to Gradle.

| Startup | Sessions | Peak RSS / PSS (GiB) | Steady RSS (GiB) | After jman stop (GiB) |
|---|---:|---:|---:|---:|
| concurrent | 1 | 2.92 / 2.88 | 2.54 | 0.72 |
| concurrent | 3 | 8.32 / 7.90 | 6.87 | 0.80 |
| concurrent | 5 | 12.18 / 11.31 | 11.54 | 0.82 |
| sequential | 1 | 3.05 / 3.01 | 2.56 | 0.73 |
| sequential | 3 | 6.97 / 6.56 | 6.49 | 0.79 |
| sequential | 5 | 11.84 / 11.13 | 11.49 | 0.80 |

Prepare/first-query times are median phase wall times. Warm p50/p95 values are
medians of per-trial percentiles, not pooled percentiles.

| Startup | Sessions | Cold prepare (s) | First queries (s) | Warm p50 / p95 (s) |
|---|---:|---:|---:|---:|
| concurrent | 1 | 58.96 | 54.63 | 0.98 / 1.31 |
| concurrent | 3 | 169.49 | 153.63 | 1.06 / 2.52 |
| concurrent | 5 | 335.88 | 303.68 | 1.14 / 4.24 |
| sequential | 1 | 59.13 | 54.58 | 1.01 / 1.28 |
| sequential | 3 | 157.75 | 160.74 | 1.06 / 2.46 |
| sequential | 5 | 254.40 | 287.73 | 1.10 / 3.01 |

## Budget and lifecycle checks

Two additional trials used concurrent startup and Linux cgroup v2 limits on
the runner scope (`MemoryMax`, `MemorySwapMax=0`), one repetition each:

| Sessions | Scope limit (GiB) | Peak service RSS (GiB) | Peak cgroup charge (GiB) | Result |
|---:|---:|---:|---:|---|
| 1 | 4 | 3.03 | 3.21 | pass |
| 5 | 16 | 12.57 | 13.04 | pass |

Both scopes recorded zero swap and zero `memory.events` counters, including OOM.
Cgroup charge includes the profiler, CLI, and page cache. These are service
budgets on a larger host, not minimum physical RAM requirements. The one-session
trial overlapped integration checks, so its timings are excluded from comparisons.

- A one-session lifecycle trial passed three source-edit/refresh cycles,
  eviction/reopening, and busy-request protection with a 30-second idle timeout.
  Status showed zero sessions after 32 idle seconds; peak RSS/PSS was 2.82/2.77 GiB.
- One exploratory 1024 MiB heap trial passed at 2.62/2.58 GiB peak RSS/PSS.
  Live JVM arguments confirmed the configured heap in both trials.

All **22 published trials** ended with zero residual tracked processes after
explicit Gradle cleanup.

## Default decision and limits

Keep the existing defaults: one workload and a single lower-heap trial do not
justify reducing headroom, session capacity, or retention of expensive imports.
Use the [resource controls and RAM guidance](../docs/support.md#memory-and-session-controls)
to tune those tradeoffs for smaller machines.

This study covers one project on one Linux x86_64 host. macOS, aarch64,
multi-module builds, and other toolchain combinations remain unmeasured.
The short windows and three repetitions do not establish a leak bound or
statistical performance guarantee. OS page caches and other applications were
uncontrolled. Sequential trials observed up to 1.48 MiB of host swap, which
cannot be attributed to jman; only the capped trials establish no-swap budgets.

Raw events, process samples, worktrees, and compiler/test logs remain under
`local/resource-study/` in the implementation workspace, outside Git. The
published trial data retains their hashes, executable identity, settings,
and correctness/cleanup outcomes.
