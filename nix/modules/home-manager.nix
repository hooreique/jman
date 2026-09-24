{ self }:
{ config, lib, pkgs, ... }:
let
  package = self.packages.${pkgs.stdenv.hostPlatform.system}.jman;
in {
  options.services.jman = {
    enable = lib.mkEnableOption "jman Java language service";
    package = lib.mkOption {
      type = lib.types.package;
      default = package;
    };
    maxSessions = lib.mkOption {
      type = lib.types.ints.positive;
      default = 2;
    };
  };

  config = lib.mkIf config.services.jman.enable (
    { home.packages = [ config.services.jman.package ]; }
    // lib.optionalAttrs pkgs.stdenv.isLinux {
      systemd.user.services.jman = {
        Unit.Description = "jman Java language service";
        Service = {
          ExecStart = "${config.services.jman.package}/bin/jman daemon --max-sessions ${toString config.services.jman.maxSessions}";
          Restart = "on-failure";
        };
        Install.WantedBy = [ "default.target" ];
      };
    }
  );
}
