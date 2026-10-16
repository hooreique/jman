# Navigation optimization measurement

## Public evidence scope

Non-public workspace measurements, diagnostics, and environment details are
omitted. This report provides no public cold/warm p50/p95 baseline and must not
be used to claim the latency success criteria.

## Follow-up

1. Wait for generated-source/build activity to become idle before beginning a
   warm sample set, then capture cold, first, and three warm samples separately.
2. Investigate which watched paths are dirty after import and distinguish
   generated output that is part of the Gradle model from unrelated activity.
3. Do not use this run to claim the latency success criteria.
