{
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
    "--idle-timeout"
    cfg.idleTimeout
  ];
in
{
  options.services.jman = {
    enable = lib.mkEnableOption "jman Java language service";
    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.jman;
    };
    maxSessions = lib.mkOption {
      type = lib.types.ints.positive;
      default = 5;
    };
    idleTimeout = lib.mkOption {
      type = lib.types.str;
      default = "15m";
      description = "Idle workspace lifetime as a Go duration, at least 1s.";
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
