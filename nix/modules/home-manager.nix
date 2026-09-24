{
  jman,
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.jman;
  daemonArguments = [
    "${cfg.package}/bin/jman"
    "daemon"
    "--max-sessions"
    (toString cfg.maxSessions)
  ];
in
{
  options.services.jman = {
    enable = lib.mkEnableOption "jman Java language service";
    package = lib.mkOption {
      type = lib.types.package;
      default = jman;
    };
    maxSessions = lib.mkOption {
      type = lib.types.ints.positive;
      default = 5;
    };
  };

  config = lib.mkIf cfg.enable (
    {
      home.packages = [ cfg.package ];
    }
    // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
      systemd.user.services.jman = {
        Unit.Description = "jman Java language service";
        Service = {
          ExecStart = lib.escapeShellArgs daemonArguments;
          Restart = "on-failure";
        };
        Install.WantedBy = [ "default.target" ];
      };
    }
    // lib.optionalAttrs pkgs.stdenv.hostPlatform.isDarwin {
      launchd.agents.jman = {
        enable = true;
        config = {
          ProgramArguments = daemonArguments;
          ProcessType = "Background";
          RunAtLoad = true;
          KeepAlive.SuccessfulExit = false;
        };
      };
    }
  );
}
