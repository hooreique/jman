{
  description = "jman: build-aware Java navigation for AI agents";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/ef34387ddd751e1ab8857adf4676492d32eb24ec";
  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };
      jdk = pkgs.jdk21;
      jdtls = pkgs.jdt-language-server;
      extension = pkgs.stdenvNoCC.mkDerivation {
        pname = "jman-jdt-extension";
        version = "0.1.0";
        src = ./extension;
        nativeBuildInputs = [ jdk ];
        buildPhase = ''
          mkdir classes
          javac -cp '${jdtls}/share/java/jdtls/plugins/*' -d classes src/dev/jman/*.java
          jar cfm jman-jdt.jar META-INF/MANIFEST.MF -C classes . plugin.xml
        '';
        installPhase = ''mkdir -p $out/share/java; cp jman-jdt.jar $out/share/java/'';
      };
      jman = pkgs.buildGoModule {
        pname = "jman";
        version = "0.1.0";
        src = self;
        vendorHash = null;
        nativeBuildInputs = [ pkgs.makeWrapper ];
        postInstall = ''
          mkdir -p $out/share/jman
          cp -r skills gradle $out/share/jman/
          wrapProgram $out/bin/jman \
            --set-default JMAN_JDTLS ${jdtls}/bin/jdtls \
            --set-default JMAN_JAVA_HOME ${jdk} \
            --set-default JMAN_EXTENSION ${extension}/share/java/jman-jdt.jar \
            --set-default JMAN_GRADLE_HOME ${pkgs.gradle}/libexec/gradle \
            --set-default JMAN_GRADLE ${pkgs.gradle}/bin/gradle \
            --set-default JMAN_GRADLE_MODEL $out/share/jman/gradle/model.gradle \
            --set-default JMAN_SKILL $out/share/jman/skills/jman/SKILL.md
        '';
      };
    in {
      packages.${system} = {
        inherit jman extension;
        default = jman;
        jman-bench = pkgs.writeShellApplication {
          name = "jman-bench";
          runtimeInputs = [ pkgs.python3 jman pkgs.git jdk pkgs.gradle ];
          text = ''exec python3 ${self}/bench/jman_bench.py "$@"'';
        };
      };
      apps.${system} = {
        default = { type = "app"; program = "${jman}/bin/jman"; };
        bench = { type = "app"; program = "${self.packages.${system}.jman-bench}/bin/jman-bench"; };
      };
      devShells.${system}.default = pkgs.mkShell {
        packages = [ pkgs.go jdk pkgs.python3 pkgs.gradle pkgs.git jdtls ];
        JMAN_JDTLS = "${jdtls}/bin/jdtls";
        JMAN_JAVA_HOME = "${jdk}";
        JMAN_EXTENSION = "${extension}/share/java/jman-jdt.jar";
        JMAN_GRADLE_HOME = "${pkgs.gradle}/libexec/gradle";
        JMAN_GRADLE = "${pkgs.gradle}/bin/gradle";
        JMAN_GRADLE_MODEL = "${self}/gradle/model.gradle";
        JAVA_HOME = "${jdk}";
      };
      checks.${system}.unit = jman;
      homeManagerModules.default = { config, lib, ... }: {
        options.services.jman = {
          enable = lib.mkEnableOption "jman Java language service";
          package = lib.mkOption { type = lib.types.package; default = self.packages.${system}.jman; };
          maxSessions = lib.mkOption { type = lib.types.ints.positive; default = 2; };
        };
        config = lib.mkIf config.services.jman.enable {
          home.packages = [ config.services.jman.package ];
          systemd.user.services.jman = {
            Unit.Description = "jman Java language service";
            Service = {
              ExecStart = "${config.services.jman.package}/bin/jman daemon --max-sessions ${toString config.services.jman.maxSessions}";
              Restart = "on-failure";
            };
            Install.WantedBy = [ "default.target" ];
          };
        };
      };
    };
}
