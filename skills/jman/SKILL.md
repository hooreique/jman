---
name: jman
description: Resolve Java definitions and references using the caller's actual build classpath, including dependency and generated code.
---

Before reasoning about a Java call's implementation or edit impact, use:

```sh
jman definition path/to/File.java:42 --symbol methodName
jman references path/to/File.java:42 --symbol methodName
jman implementations path/to/File.java:42 --symbol methodName
```

Locations use 1-based lines and Unicode columns. Use `--occurrence N` if a name appears more than once on a line. `--project PATH` selects a build root when needed.

Check `origin`, `context`, and `coverage`: a same-named local file may not be the selected dependency. References cover the current imported workspace, not every downstream repository. Implementation candidates do not prove Spring runtime bean selection.

Definitions include short source excerpts. Read more with `jman read PATH --lines 10:50`; external sources are read-only and may be decompiled. Change the owning source or generator input, not cached/generated files.

For incomplete results, follow `nextAction` or run `jman doctor`. `jman prepare` warms the workspace; `jman refresh` reimports after build changes. Use `--json` for machine-readable output. Do not substitute a guessed definition after an unresolved binding.
