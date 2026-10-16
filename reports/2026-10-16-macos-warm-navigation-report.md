# macOS warm navigation stabilization

## Method

- Host: aarch64-darwin; Nix-pinned Java 21, Gradle 8.14.4, JDTLS 1.60.0.
- One isolated daemon/session, fresh Gradle and JDTLS caches, 1536 MiB maximum heap.
- Controlled synthetic Gradle workspace: nine projects, 6,003 Java files.
  Eight `lib0`–`lib7` projects each contain 750 independent `ValueN` classes;
  `app` depends on `lib0`, which also contains an `Api` interface and `Impl`.
  `app.Caller.call(Api)` invokes `Api.apply("value")`.
- No remote dependencies; Gradle offline mode. No model/API calls or agent arms.
- Cold `prepare`, then three consecutive one-second-spaced prepares reporting
  `status: ok`, `cacheHit: true`, and `dirty: false` before first navigation.
  Three further prepares separate first navigation from warm sampling.
- Twenty interleaved repetitions each of definition, references, and
  implementations on `app/src/main/java/app/Caller.java:1 --symbol apply`,
  with `--json --timings --timeout 300s`.
- Every warm response must succeed, contain results, and report `scan: 0` and
  `sync: 0`. Returned paths were checked against `Api.java`, `Caller.java`, and
  `Impl.java`, respectively. A separate Java compile/runtime oracle exercises
  `new Caller().call(new lib0.Impl())` and passed its returned-value assertion.
- Wall time includes CLI startup and IPC. p50 is the median; p95 is nearest rank.

## Observed results

| Command | Warm samples | p50 ms | p95 ms | Maximum ms |
|---|---:|---:|---:|---:|
| definition | 20 | 22.99 | 27.32 | 28.62 |
| references | 20 | 29.05 | 34.81 | 42.34 |
| implementations | 20 | 43.82 | 57.12 | 57.31 |

All 60 warm requests returned `ok`, skipped scan/sync, and retained the same
snapshot. Cold prepare took 34.07 seconds. First navigation took 515.14 ms,
53.80 ms, and 139.25 ms, respectively; these are excluded from warm percentiles.

## Retention and limits

Raw events, responses, fixture, caches, logs, summary, and the generator/runner
are retained locally and are not committed. Local artifact locations are omitted.
The measured executable SHA-256 is
`be8b99e1f10b795a292eeab6df875be79d4ce03061fa43aa3e1ad98e3fca313d`;
the generator/runner SHA-256 is
`75817e3fef86f5f357096426fd76c5e0b40ee0a90104170e8489f7b8dfba1daf`.

This is one synthetic-workspace trial with many files but a small queried symbol
graph. It is not a before/after comparison, an agent accuracy/cost trial, a Linux
measurement, or evidence of representative workspace performance. The host
also ran background Nix/test validation; this is not an
idle-host measurement. Real-workspace post-import activity and diagnostic behavior still
need operator-specific measurements before claiming representative latency.
