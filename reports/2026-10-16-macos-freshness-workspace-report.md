# macOS freshness regression checks

## Correctness checks

The real Gradle/JDTLS integration regression replaces a file-dependency JAR
outside the workspace without calling `refresh`. It verifies changed binary
digest, snapshot, decompiled definition, cached-source path, and JDTLS PID.
The subsequent `refresh` produces identical results and snapshot. A Java edit
under the Gradle-declared `reports/coverage` root likewise matches `refresh`.

Go regressions cover same-size external rewrites with restored mtime, build
changes inside the sync response (including model re-export), selected test
variant outputs, excluded producer dependencies, full/incremental snapshots,
and native watching of excluded and initially missing declared source roots.

## Public evidence scope

This report retains only repository regression evidence. Non-public workspace
measurements, diagnostics, environment details, and local artifact locations
are omitted. These regressions do not establish representative workspace
latency, whole-workspace accuracy, arbitrary variant compatibility, or Linux
behavior.
