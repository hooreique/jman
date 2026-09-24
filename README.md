# jman

**Build-aware Java symbol navigation for AI agents and developers.**

jman resolves a symbol from the caller's JDT binding and Gradle-selected classpath. In multi-project and composite builds, it distinguishes the code being called from a nearby, stale source file with the same name.

## Install

The Nix flake supports `aarch64-darwin`, `aarch64-linux`, and `x86_64-linux`.

```sh
nix build .#jman
./result/bin/jman --help

# Run without installing from a Gradle project.
nix run . -- definition src/main/java/example/OrderService.java:42 --symbol normalize

# Or run the published version directly, without installing.
nix run github:hooreique/jman -- definition src/main/java/example/OrderService.java:42 --symbol normalize
```

The first request starts a local daemon and a JDTLS session for the Gradle build.

## Home Manager

Import the flake's `homeManagerModules.default` module in a Home Manager
configuration. Make `jman` available as `pkgs.jman` first; this example uses the
provided pinned overlay. The module installs it and starts its daemon as a
LaunchAgent on Darwin or a systemd user service on Linux.

```nix
# flake.nix
{
  inputs = {
    # ...
    jman.url = "github:hooreique/jman";
  };

  outputs = { nixpkgs, home-manager, jman, ... }: {
    # Apple Silicon macOS
    homeConfigurations."alice-darwin" = home-manager.lib.homeManagerConfiguration {
      pkgs = import nixpkgs {
        system = "aarch64-darwin";
        overlays = [ jman.overlays.pinned ];
      };
      modules = [
        # ...
        jman.homeManagerModules.default
        {
          services.jman.enable = true;
        }
      ];
    };

    # x86_64 Linux
    homeConfigurations."alice-linux" = home-manager.lib.homeManagerConfiguration {
      pkgs = import nixpkgs {
        system = "x86_64-linux";
        overlays = [ jman.overlays.pinned ];
      };
      modules = [
        # ...
        jman.homeManagerModules.default
        {
          services.jman.enable = true;
        }
      ];
    };
  };
}
```

Add your normal Home Manager configuration at `# ...`, then activate the matching
configuration with `home-manager switch --flake .#alice-darwin` or
`home-manager switch --flake .#alice-linux`.

## Use

Run commands from a Gradle build root or a descendant.

```sh
# Resolve the actual declaration and its provenance.
jman definition src/main/java/example/OrderService.java:42 --symbol normalize

# Search the imported workspace.
jman references src/main/java/example/OrderService.java:42 --symbol normalize
jman implementations src/main/java/example/OrderMapper.java:12 --symbol toDto

# Inspect type information, dependencies, and project health.
jman hover src/main/java/example/OrderService.java:42 --symbol normalize
jman deps src/main/java/example/OrderService.java
jman doctor
```

Definitions include the resolved signature, source set, selected artifact and version, binary SHA-256, source origin, and a short excerpt.

```sh
# Full provenance or structured output.
jman definition src/main/java/example/OrderService.java:42 --symbol normalize --explain
jman definition src/main/java/example/OrderService.java:42 --symbol normalize --json

# Read a returned source path.
jman read /path/from/definition/Source.java --lines 20:60
```

Lines and columns are 1-based; columns use Unicode code points. When a symbol occurs more than once on a line, pass `--occurrence N` or an exact column.

## Configure a project

jman discovers a Gradle build root from settings or build files. Use `--project /path/to/build` when discovery is not appropriate. Optional settings belong in `<build-root>/.jman.json`.

```json
{
  "offline": false,
  "javaHome": "/path/to/jdk21",
  "gradleJavaHome": "/path/to/jdk17-or-21",
  "generateTasks": [":app:compileJava"]
}
```

`jman prepare --generate` runs only the configured `generateTasks` before importing. Run `jman refresh` after changing build or dependency inputs.

## Install the agent skill

```sh
jman skill install --target /path/to/agent/skills/jman
```

jman does not overwrite a different installed skill.

## Limits

- Results describe compile-time bindings. References cover only the workspace imported by JDTLS.
- A nearby repository is never substituted for a dependency JAR merely because names match. Gradle composite substitution is followed when the build selects it.
- Static navigation cannot fully determine Spring runtime bean/proxy selection, reflection, or dynamic JPA queries.
- Import or build failures return `partial` results with diagnostics.

See [support](docs/support.md) for the complete support boundary, exit codes, and benchmark contracts.

## More

- [Contributing and documentation maintenance](CONTRIBUTING.md)
- [Validation record and benchmark trials](docs/validation.md)
- [MIT License](LICENSE)
