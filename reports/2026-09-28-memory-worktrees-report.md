# Memory usage across simultaneous worktrees

This resource study addresses [issue #1](https://github.com/hooreique/jman/issues/1).
It uses deterministic navigation, without a language model, API usage, or cost
claims. The method is implemented by `jman-bench resources`; see
[benchmark design](../docs/benchmark.md#resource-measurement).

## Workload and environment

- Upstream: [RxJava v3.1.12](https://github.com/ReactiveX/RxJava/tree/2e066505a104f3326bb31a960047b0c183b3d027),
  commit `2e066505a104f3326bb31a960047b0c183b3d027`.
- 1,884 tracked Java files: 857 main, 970 test, and 57 other; one Gradle
  project, three source-set contexts, 24 unique selected artifact paths in the
  exported model.
- Linux x86_64, NixOS kernel 6.18.50; AMD Ryzen 7 1800X, 8 cores/16 threads;
  31 GiB reported physical RAM, 15 GiB swap. Other applications and the OS page
  cache were not isolated.
- Nix lock from origin `8549668`; JDTLS 1.60.0, OpenJDK 21.0.12.1 runtime,
  RxJava's Gradle 8.14 wrapper, Eclipse Temurin 11.0.32 project toolchain.
  `BUILD_WITH_11=true`; experimental Gradle user homes use
  `org.gradle.workers.max=4` and an explicit toolchain path.
- Default JDTLS heap: initial 128 MiB, maximum 1,536 MiB. Each trial sets its
  session limit explicitly and verifies separate live JDTLS PIDs for all roots.
- Dependencies and Gradle distribution were preloaded, then copied to an
  isolated home per trial, without daemon state or locks. JDTLS workspaces and
  Git worktree build directories begin empty. This is dependency-warm,
  JDTLS-cold measurement; it does not claim an empty OS page cache.

`compileJava` and `compileTestJava` passed on the unmodified upstream project.
Its targeted `FlowableMapTest` ran 28 tests with one skipped, zero failures, and
zero errors.
However, the initial JDTLS import returned `partial` with 18 syntax diagnostics:
the Java 9 module descriptor was included in its Java 8 Eclipse source set.
The experiment applies the small, explicit
[Eclipse classpath adjustment](../bench/workloads/rxjava-jdtls.gradle).
It changes only Eclipse's source entries, leaving Gradle compilation and test
inputs unchanged. The adjusted import has complete coverage and no diagnostics.
This compatibility limitation remains separate from the memory changes.

The locally prepared commit is `079896035e13d2e27c13bc63e39877e5924c3854`,
tree `8fa702a55ee3ea9c0250de215b4d1456eaad473d`. The three
[workload queries](../bench/workloads/rxjava.json) check `Function.apply`'s
definition path, local mapper references, and hover; they all passed preflight.

## Reproduction

```sh
git clone https://github.com/ReactiveX/RxJava.git local/rxjava
git -C local/rxjava checkout 2e066505a104f3326bb31a960047b0c183b3d027
cat bench/workloads/rxjava-jdtls.gradle >> local/rxjava/build.gradle
git -C local/rxjava add build.gradle
git -C local/rxjava commit -m 'Exclude module descriptor from Java 8 Eclipse sources'
```

Provide a Temurin 11 installation, export `BUILD_WITH_11=true`, and prepare a
dedicated Gradle user home containing these `gradle.properties` entries:

```properties
org.gradle.java.installations.paths=/absolute/path/to/temurin-11
org.gradle.workers.max=4
```

Run `compileJava compileTestJava` and
`test --tests io.reactivex.rxjava3.internal.operators.flowable.FlowableMapTest`
using RxJava's wrapper and that home. A successful `jman prepare` preflight also
warms the source attachments. Stop its jman and Gradle daemons before copying
the seed. Within `nix develop`, run:

```sh
jman-bench resources --repository local/rxjava --ref HEAD \
  --output local/memory-concurrent --sessions 1,3,5 --repetitions 3 \
  --startup concurrent --queries bench/workloads/rxjava.json \
  --gradle-home-seed /absolute/path/to/seed --refresh-rounds 0 --timeout 600
```

Repeat with `--startup sequential` and a new output directory. Retain failed
attempts. Use separate trials with `--lifecycle` and source-edit refreshes so
their import work is not confused with warm-query measurements.

Each startup mode runs 1, 3, and 5 sessions, repeated three times in that order.
`concurrent` starts the prepare requests together; `sequential` prepares each
worktree in turn. Both modes then issue navigation concurrently across the
resident sessions, with one worker per worktree. The first-navigation phase
executes each of the three queries once; the warm phase repeats them three
times. Correctness checks require successful JSON, nonempty results, and the
expected definition/reference paths. Status checks require all requested
worktrees to be ready with distinct live JDTLS PIDs.

The requested sampling interval is 200 ms; actual intervals also include
collection time. Peaks are the largest sampled simultaneous process totals,
and can miss shorter bursts. "Steady" is a three-second observation after first
navigation, not a long-duration stability test. Each command gets a 600-second
deadline. Concurrent cold imports can exceed the CLI's default 120-second
deadline on this host; use an explicit longer `--timeout` for this workload.

## Main matrix

All **18 trials passed**, including 648 navigation commands: 162 first-query
commands and 486 warm-query commands. Every requested session was retained and
ready at the phase checks, and every trial exited with zero residual tracked
processes after cleanup.

Memory peaks below are maxima across the three trials in each row. Steady and
after-stop values are medians of each trial's sample mean for that phase, in
GiB (2^30 bytes). RSS and PSS maxima can occur in different samples. After-stop
memory belongs to Gradle; after explicit cleanup the tracked process count and
sampled RSS were zero in every completed trial.

| Startup | Sessions | Pass/attempts | Peak RSS / PSS (GiB) | Steady RSS (GiB) | After jman stop (GiB) |
|---|---:|---:|---:|---:|---:|
| concurrent | 1 | 3/3 | 2.92 / 2.88 | 2.54 | 0.72 |
| concurrent | 3 | 3/3 | 8.32 / 7.90 | 6.87 | 0.80 |
| concurrent | 5 | 3/3 | 12.18 / 11.31 | 11.54 | 0.82 |
| sequential | 1 | 3/3 | 3.05 / 3.01 | 2.56 | 0.73 |
| sequential | 3 | 3/3 | 6.97 / 6.56 | 6.49 | 0.79 |
| sequential | 5 | 3/3 | 11.84 / 11.13 | 11.49 | 0.80 |

Prepare and first-query times are median phase wall times. Warm percentiles
are medians of the three per-trial percentiles, not pooled percentiles; counts
give the total individual warm navigation commands across the three trials.

| Startup | Sessions | Cold prepare (s) | First queries (s) | Warm p50 / p95 (s) | Warm queries |
|---|---:|---:|---:|---:|---:|
| concurrent | 1 | 58.96 | 54.63 | 0.98 / 1.31 | 27 |
| concurrent | 3 | 169.49 | 153.63 | 1.06 / 2.52 | 81 |
| concurrent | 5 | 335.88 | 303.68 | 1.14 / 4.24 | 135 |
| sequential | 1 | 59.13 | 54.58 | 1.01 / 1.28 | 27 |
| sequential | 3 | 157.75 | 160.74 | 1.06 / 2.46 | 81 |
| sequential | 5 | 254.40 | 287.73 | 1.10 / 3.01 | 135 |

Sequential preparation had lower sampled peaks for three and five sessions on
this host, and the five-session prepare phase completed sooner. Five-session
steady residency remained about 11.5 GiB in both modes, and first navigation
still required substantial work. Preparing worktrees in sequence can moderate
import pressure in this workload; the active-session memory budget remains
necessary. These observations do not establish a general scheduling policy.

The following component breakdown uses the sample with the highest total RSS
in each group. The last column independently counts the maximum simultaneous
Gradle daemons during any sample, not the number of distinct PIDs over a run.

| Startup | Sessions | jman / JDTLS / Gradle / other at total RSS peak (GiB) | Maximum concurrent Gradle daemons |
|---|---:|---:|---:|
| concurrent | 1 | 0.017 / 2.224 / 0.684 / 0.000 | 1 |
| concurrent | 3 | 0.015 / 6.961 / 1.341 / 0.000 | 3 |
| concurrent | 5 | 0.024 / 9.848 / 2.304 / 0.000 | 5 |
| sequential | 1 | 0.017 / 2.299 / 0.738 / 0.000 | 1 |
| sequential | 3 | 0.020 / 6.110 / 0.836 / 0.000 | 3 |
| sequential | 5 | 0.028 / 10.940 / 0.874 / 0.000 | 5 |

Host-wide available memory stayed at or above 17.89 GiB during the matrix.
The concurrent trials observed zero swap; the sequential trials observed up to
1.48 MiB of host swap, with 379 pages swapped out and none swapped in during
the recorded trials. Host-wide swap and pressure counters cannot identify
which process caused that activity. They are retained in the published data;
the separate constrained trials below provide workload-specific no-swap
budget evidence.

## Constrained memory budgets

Additional Linux cgroup v2 trials constrain the entire runner scope with
systemd `MemoryMax` and `MemorySwapMax=0`. Each uses concurrent startup, the
default 1,536 MiB JDTLS heap, one repetition, and the same correctness and
cleanup checks. A wrapper reads `memory.max`, `memory.peak`, `memory.events`,
and `memory.swap.{max,peak,current}` after the runner finishes, before the scope
exits. The [published data](2026-09-28-memory-worktrees-results.json) preserves
those counters.

| Sessions | Scope limit (GiB) | Peak service RSS (GiB) | Peak cgroup charge (GiB) | Peak swap | OOM / OOM kills | Result |
|---:|---:|---:|---:|---:|---:|---|
| 1 | 4 | 3.03 | 3.21 | 0 | 0 / 0 | pass |
| 5 | 16 | 12.57 | 13.04 | 0 | 0 / 0 | pass |

Cgroup charge includes the profiler, CLI processes, and charged page cache;
it is not the same quantity as sampled service RSS/PSS. These successful caps
validate service budgets on the measured workload, not minimum physical RAM
for an entire computer. The one-session budget trial overlapped integration
checks, so its timings are excluded from performance comparisons.
Both scopes reported zero `memory.events` counters, including `max` and OOM,
and no residual tracked processes. Charged file cache can remain after service
processes exit, so zero tracked RSS does not imply zero cgroup charge.

The resulting [RAM planning guidance](../docs/support.md#ram-planning-guidance)
uses the verified one- and five-session service budgets, an explicitly
estimated intermediate allowance, and separate space for the OS and developer
applications. These results do not establish a universal minimum machine size.

## Refresh, eviction, idle, and heap controls

A separate one-session lifecycle trial used the default heap, a 30-second idle
timeout, three source-edit/explicit-refresh cycles, and one extra worktree. It
passed every navigation and session-state check, with a 2.82 GiB peak RSS and
2.77 GiB peak PSS. Explicit refresh restarts JDTLS; the three refresh commands
took 29.76, 30.09, and 29.92 seconds, followed by successful navigation checks.
Importing the extra worktree replaced exactly one session (49.77 seconds), and
reopening the evicted worktree respected the one-session limit (29.65 seconds).

After 32 seconds of inactivity, status reported zero sessions. The last idle
sample was 0.81 GiB for jman and retained Gradle state; explicit Gradle cleanup
left zero tracked processes and zero RSS. Requests lasting longer than the
configured idle timeout also completed successfully while active. This tests
the configurable timeout and busy-session protection, not a 15-minute wait or
long-running leak behavior.

One exploratory trial set `jdtlsMaxHeapMiB` to **1024**. It passed with peak
RSS/PSS of **2.62/2.58 GiB**, prepare/first-query phase times of 59.20/55.56
seconds, and warm p50/p95 of 1.00/1.31 seconds. Live JVM command lines confirmed
`-Xms128m -Xmx1024m`; the lifecycle trial similarly confirmed `-Xmx1536m`.
This single lower-heap trial establishes that the setting works for this
workload. It is not enough evidence to change the default or recommend that
heap for arbitrary projects.

## Pilot observation

The first one-session pilot succeeded with a sampled peak RSS of 2.80 GiB for
jman, JDTLS, and Gradle together. `prepare` took 86.15 seconds; the first
definition query took another 52.97 seconds and changed the observed JDTLS PID.
The import had generated Eclipse `.classpath` metadata, which participates in
the existing build fingerprint. Subsequent queries took 0.71–1.31 seconds.

Consequently the final runner records a separate `first-queries` phase before
steady residency and repeated `warm-queries`. First-query costs remain in the
results and total resource peaks. They are not silently excluded or reported
as steady-state query latency. The pilot was run while integration checks were
also active, so its timings are exploratory and excluded from the main matrix.

## Decisions about defaults

- Keep **1,536 MiB** as the default maximum JDTLS heap. This study covers one
  large project; it does not justify reducing headroom for larger dependency
  graphs or other build tools. The new project-level `jdtlsMaxHeapMiB` setting
  permits explicit tuning. Its value caps Java heap, not the JVM's total RSS.
- Keep the existing **five-session** ceiling for compatibility and concurrent
  worktree use. Sessions start lazily; the ceiling does not reserve five JVMs.
  It is a capacity setting, not a memory guarantee. Machines with
  smaller service budgets should explicitly select one or three sessions.
- Keep the **15-minute** idle timeout to preserve expensive imported state
  between queries. The new daemon/Home Manager setting allows shorter
  retention when memory matters more than reopening latency. Busy requests are
  protected; expiration includes the documented periodic-sweep delay.
- Do not add automatic memory-based admission or a portable RSS cap in this
  change. RSS, PSS, and cgroup accounting have different meanings, Gradle can
  outlive jman, and platform coverage here is insufficient for a reliable
  cross-platform policy. Explicit session limits and per-project heaps are
  inspectable through `status --json` and remain the primary controls.

## Evidence retention and limits

The [published trial summaries](2026-09-28-memory-worktrees-results.json) retain
per-trial memory, timings, correctness/cleanup outcomes, process attribution at
the simultaneous RSS peak, and SHA-256 hashes of the raw artifacts. Component
values at that peak add to the measured total; independent component maxima
must not be added to estimate a simultaneous peak.

Raw command events, process samples, caches, worktrees, and compiler/test logs
are retained under `local/resource-study/` in the implementation workspace.
These generated files and third-party sources are not committed. Each run's
metadata records the executable SHA-256, project commit, query manifest,
settings, and runtime paths. Published summaries describe sampled simultaneous
process totals; RSS may double-count shared pages and PSS is Linux-specific.
The short observation windows and three repetitions do not establish a memory
leak bound or a statistical performance guarantee.

This is one large source tree with three source sets, not a survey of
multi-module enterprise builds, different JDK/Gradle combinations, or annotation
processor workloads. All measured worktrees share one Gradle version and
experimental user home within each trial; homes are separate between trials.
Different versions or JVM options can require more
Gradle daemons. macOS and aarch64 runtime memory remain unmeasured here; portable
collector tests do not establish memory requirements on those systems.
