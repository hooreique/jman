{
  description = "jman: build-aware Java navigation for AI agents";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/ef34387ddd751e1ab8857adf4676492d32eb24ec";

  outputs = { self, nixpkgs }:
    let
      forAllSys =
        perSys:
        nixpkgs.lib.genAttrs [ "aarch64-darwin" "aarch64-linux" "x86_64-linux" ] (
          system: perSys nixpkgs.legacyPackages.${system}
        );

      packageSet = pkgs:
        let
          lombokAgent = pkgs.callPackage ./nix/packages/lombok-agent/package.nix { };
          extension = pkgs.callPackage ./nix/packages/extension/package.nix { };
          jman = pkgs.callPackage ./nix/packages/jman/package.nix {
            inherit extension lombokAgent;
          };
          bench = pkgs.callPackage ./nix/packages/bench/package.nix {
            inherit jman;
          };
        in {
          inherit extension jman lombokAgent;
          jman-bench = bench;
          default = jman;
        };
    in {
      packages = forAllSys packageSet;

      apps = forAllSys (pkgs:
        let packages = packageSet pkgs;
        in {
          default = {
            type = "app";
            program = "${packages.jman}/bin/jman";
          };
          bench = {
            type = "app";
            program = "${packages.jman-bench}/bin/jman-bench";
          };
        });

      devShells = forAllSys (pkgs:
        let
          packages = packageSet pkgs;
          fixtureDeps = import ./nix/java-deps.nix { inherit pkgs; };
          jdk = pkgs.jdk21;
          jdtls = pkgs.jdt-language-server;
        in {
          default = pkgs.mkShell {
            packages = [ pkgs.go jdk pkgs.python3 pkgs.gradle pkgs.git jdtls packages.jman ];
            JMAN_JDTLS = "${jdtls}/bin/jdtls";
            JMAN_JDTLS_HOME = "${jdtls}/share/java/jdtls";
            JMAN_LOMBOK_AGENT = "${packages.lombokAgent}";
            JMAN_JAVA_HOME = "${jdk}";
            JMAN_EXTENSION = "${packages.extension}/share/java/jman-jdt.jar";
            JMAN_GRADLE_HOME = "${pkgs.gradle}/libexec/gradle";
            JMAN_GRADLE = "${pkgs.gradle}/bin/gradle";
            JMAN_GRADLE_MODEL = "${self}/gradle/model.gradle";
            JMAN_FIXTURE_DEPS = "${fixtureDeps}";
            JAVA_HOME = "${jdk}";
          };
        });

      checks = forAllSys (pkgs:
        let
          packages = packageSet pkgs;
          jdk = pkgs.jdk21;
          common = {
            nativeBuildInputs = [ pkgs.python3 pkgs.git pkgs.bash jdk pkgs.gradle packages.jman ];
          };
        in {
          unit = packages.jman;
          benchmark = pkgs.runCommand "jman-benchmark-check" common ''
            export HOME=$TMPDIR/home
            mkdir -p "$HOME"
            python3 ${self}/tests/bench_test.py ${packages.jman}/bin/jman
            touch $out
          '';
          integration = pkgs.runCommand "jman-jdtls-integration" (common // {
            JMAN_FIXTURE_DEPS = "${import ./nix/java-deps.nix { inherit pkgs; }}";
          }) ''
            export HOME=$TMPDIR/home
            mkdir -p "$HOME"
            python3 ${self}/tests/integration.py ${packages.jman}/bin/jman
            touch $out
          '';
        });

      overlays = {
        default = final: prev:
          let
            lombokAgent = final.callPackage ./nix/packages/lombok-agent/package.nix { };
            extension = final.callPackage ./nix/packages/extension/package.nix { };
            jman = final.callPackage ./nix/packages/jman/package.nix {
              inherit extension lombokAgent;
            };
          in {
            inherit jman lombokAgent;
            jman-jdt-extension = extension;
            jman-bench = final.callPackage ./nix/packages/bench/package.nix { inherit jman; };
          };

        pinned = final: prev:
          let packages = self.packages.${final.stdenv.hostPlatform.system};
          in {
            jman = packages.jman;
            jman-jdt-extension = packages.extension;
            jman-bench = packages.jman-bench;
            jman-lombok-agent = packages.lombokAgent;
          };
      };

      homeManagerModules.default = { config, lib, pkgs, ... }:
        import ./nix/modules/home-manager.nix {
          inherit config lib pkgs;
          jman = self.packages.${pkgs.stdenv.hostPlatform.system}.jman;
        };
    };
}
