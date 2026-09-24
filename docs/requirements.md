# Product requirements and agent interface

This document records product goals and acceptance criteria. It is not a statement that every goal has shipped. See [support](support.md) for current behavior and [validation](validation.md) for executed checks.

## Goal

Help an agent answer, with little search and a small response:

1. Which overload does this call bind to?
2. Is its declaration workspace, generated, dependency, or decompiled code?
3. Which binary and source evidence support that answer?
4. Where is the static impact within the analyzed workspace?
5. Which build and file state produced the result?

Success means fewer incorrect edits and lower total cost to a correct answer, not merely more LSP features.

## Interface principles

- Provide one familiar CLI, `jman`, plus a short agent skill.
- Use concise text by default and a versioned JSON response for `--json`.
- Discover a build root from the current path; require `--project` when it is ambiguous.
- Start the daemon and JDTLS lazily. Do not make users learn session setup commands.
- Keep natural-language reasoning in the agent. jman resolves code semantics.
- Treat an MCP adapter as a future interface over the same query engine, not a separate semantic implementation.

## Commands

| Command | Requirement |
|---|---|
| `definition LOCATION` | Resolve the selected declaration with provenance and an excerpt |
| `references LOCATION` | Find static references in the imported workspace and state coverage |
| `implementations LOCATION` | Find static type-hierarchy candidates and state runtime limits |
| `hover LOCATION` | Show resolved type, signature, and short documentation |
| `read PATH` | Read workspace, generated, or cached external source |
| `prepare`, `refresh`, `status`, `doctor` | Manage and diagnose a workspace |
| `deps LOCATION` | Show selected component, variant, and artifact context |
| `symbols`, `callers`, `callees` | Future goals; not shipped |

## Location and response contract

Locations use 1-based lines and Unicode code-point columns. `--symbol` selects an identifier on the given line; it is not a repository-wide name search. Repeated identifiers require `--occurrence N` or an exact column. Disk files are the default input; unsaved editor buffers are outside current scope.

A successful definition should identify the signature, origin (`workspace-source`, `generated-source`, `dependency-source`, `decompiled`, or `binary-only`), selected artifact when known, caller Gradle context, location, excerpt, and warnings that affect trust.

JSON responses use `schemaVersion`, `status`, `query`, `context`, `snapshot`, `results`, `coverage`, `warnings`, and `nextCursor`. Errors use `code`, `message`, `retryable`, and `nextAction`. Results are bounded by `--limit` and `--max-bytes`; cursors are valid only for the same snapshot.

## Correctness requirements

### Binding and provenance

- Select declarations from the caller compilation unit's binding, not from imports, FQN text, filenames, or nearby repositories.
- Record the selected classpath binary and digest when a binary is involved.
- Keep binary provenance and source provenance separate. Use `verified` only with independent proof; coordinate matching alone is `coordinate-only` or `unverified`.
- When source is unavailable, label decompiled output correctly; never present decompiled line numbers as original source locations.
- Do not replace a dependency with a neighboring checkout unless Gradle selects a composite/substituted project or a verified mapping exists.

### Gradle and generated code

- Use Gradle/Buildship resolution rather than a second dependency resolver. Respect wrapper, multi-project builds, composites, constraints, lockfiles, source sets, variants, and local JARs.
- Report disagreement between JDT and Gradle classpaths as degraded or partial.
- Resolve Lombok-generated members to their owner when no source body exists.
- Recognize MapStruct implementations and QueryDSL types in generated source roots.
- Run generation only through explicit `prepare --generate` tasks; ordinary navigation must not run a full build.

### Freshness and scope

- Track source and build revisions in each snapshot. Reflect edits, file changes, branch changes, and build-input changes.
- Do not share mutable JDTLS state between separate Git worktrees.
- State that references cover the imported workspace, not unknown downstream consumers.
- State that implementation candidates do not determine Spring runtime bean/proxy selection.

## Skill rules

The distributed skill should tell an agent to resolve definitions and references before reasoning about implementation or impact; check origin, context, and coverage; read only the needed source; follow `nextAction` or `doctor` on incomplete results; and modify owner source or generator inputs rather than caches or generated output. It should not duplicate operations documentation or internal JDTLS detail.
