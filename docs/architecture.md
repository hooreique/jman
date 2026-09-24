# Architecture

This document describes the 0.1 design and its intended direction. See [support](support.md) for shipped behavior and [validation](validation.md) for executed checks.

```text
developer or agent
       |
    jman CLI
       | local Unix socket
    daemon
       +-- workspace session A -- JDTLS JVM + jman Java extension
       +-- workspace session B -- JDTLS JVM + jman Java extension
       +-- source and artifact cache

jman-bench -> agent adapter -> fresh workspace -> independent evaluator -> report
```

## Components

| Component | Language | Responsibility |
|---|---|---|
| CLI, daemon, sessions, skill installer | Go | Process lifecycle, socket API, requests, caches |
| JDTLS extension | Java | Binding, Java element, classpath, and generated-member provenance |
| Gradle init script | Gradle/Groovy | Selected component, variant, artifact, and source-set model |
| Benchmark runner | Python | Isolated trials, adapters, evaluator, reporting |
| Fixtures | Java/Gradle | Real Gradle and annotation-processing behavior |
| Packaging | Nix | Fixed compatible JDK, JDTLS, extension, and fixtures |

The Go layer does not reimplement Java semantics. The benchmark runner is not required to operate jman.

## Workspace sessions

A session represents a Gradle build root and the projects it declares in one worktree.

- One multi-project build imports as one session.
- Independent build roots get separate sessions.
- Composite builds follow Gradle declarations; neighboring repositories are not imported speculatively.
- Separate worktrees never share mutable JDTLS data.
- JDK, JDTLS/extension, Gradle configuration, build inputs, and selected artifacts participate in invalidation.

JDTLS runtime JDK, Gradle daemon JDK, and project toolchain JDK are distinct. The packaged runtime uses a fixed Java 21-compatible JDTLS configuration.

## Query path

1. The CLI maps a location to a build root and sends a deadline-bound request.
2. The daemon serializes work that mutates a session and creates JDTLS once.
3. The session synchronizes files and Gradle/JDTLS state.
4. JDTLS resolves definition, references, implementations, or hover.
5. The extension reads the selected Java element, classpath entry, binary path, and generated-member details.
6. The Gradle model maps a selected binary to its component, variant, and caller source set.
7. jman returns a bounded result with source provenance, coverage, and freshness state.

Location alone is insufficient for provenance. The caller binding and selected classpath are required. Missing model data must remain unknown or partial.

## Source resolution

Resolution proceeds from selected JDT element to binary, attached source, verified mapping, then decompiled source or signature. A source attachment is not automatically proof that it was built with the selected binary.

External source is written to a read-only content-addressed cache. Workspace indexes remain session-local. Gradle handles private repository credentials; jman does not create another credential store or write credentials to reports.

## Lifecycle and recovery

Sessions move through `starting`, `importing`, `indexing`, `ready`, `degraded`, `failed`, and `stopped`. An LSP initialize response alone does not mean ready.

The daemon uses bounded queues, request deadlines, cancellation, idle eviction, and restart backoff. Queries may run concurrently where safe; import, refresh, and model replacement are serialized. A cancelled request must not cancel shared import work needed by another request.

`status` is a compact summary. `doctor --json` exposes JDK, Gradle, Lombok, processor, and classpath diagnostics.

## Packaging

The flake exposes `jman`, `jman-bench`, the JDTLS extension, apps, a development shell, checks, overlays, and a Home Manager module. `overlays.default` rebuilds with the caller's nixpkgs; `overlays.pinned` exposes this flake's fixed packages.

Packages are evaluated for `aarch64-darwin`, `aarch64-linux`, and `x86_64-linux`. Real integration validation currently runs on `x86_64-linux`; the other systems receive flake evaluation. Home Manager installs a systemd user service on Linux and a launchd agent on Darwin.

Nix fixes the JDTLS runtime and fixture closure. Real user dependency resolution still runs through the user's Gradle environment, and mutable workspace data stays outside the Nix store.

## Roadmap

The vertical slice and core navigation are implemented. Future work includes broader agent trials, cross-repository references, Spring relationship analysis, and an MCP adapter. These are not current support commitments.

## Upstream references

- [JDTLS README](https://github.com/eclipse-jdtls/eclipse.jdt.ls/blob/main/README.md)
- [JDTLS plugin.xml](https://github.com/eclipse-jdtls/eclipse.jdt.ls/blob/main/org.eclipse.jdt.ls.core/plugin.xml)
- [vscode-java settings](https://github.com/redhat-developer/vscode-java/blob/master/package.json)
