"""Disk-warm/JVM-cold resume regression, including edits while stopped."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


def main():
    binary = str(Path(sys.argv[1]).resolve())
    with tempfile.TemporaryDirectory(prefix="jman-restart-") as tmp:
        root = Path(tmp)
        project = root / "project"
        source = project / "src/main/java/example"
        source.mkdir(parents=True)
        (project / "settings.gradle").write_text("rootProject.name = 'restart-regression'\n")
        (project / "build.gradle").write_text("plugins { id 'java' }\n")
        (project / ".jman.json").write_text('{"offline":true,"jdtlsMaxHeapMiB":768}')
        api = source / "Api.java"
        caller = source / "Caller.java"
        api.write_text('package example; public class Api { public String value() { return "before"; } }\n')
        caller_text = 'package example; class Caller { String call() { return new Api().value(); } }\n'
        caller.write_text(caller_text)
        env = dict(os.environ, JMAN_SOCKET=str(root / "service.sock"),
                   JMAN_CACHE_HOME=str(root / "cache"), GRADLE_USER_HOME=str(root / "gradle"))

        def query(command, *args):
            started = time.monotonic()
            p = subprocess.run([binary, command, *args, "--json", "--timings", "--timeout", "180s"],
                               cwd=project, env=env, capture_output=True, text=True, timeout=190)
            assert p.returncode in (0, 3), (command, p.returncode, p.stdout, p.stderr)
            r = json.loads(p.stdout)
            print(command, round(time.monotonic() - started, 3), r.get("timings"), flush=True)
            return r

        location = "src/main/java/example/Caller.java:1"
        try:
            first = query("definition", location, "--symbol", "value")
            assert "before" in first["results"][0]["snippet"], first
            prepared = query("prepare")
            old_pid = prepared["results"][0]["detail"]["service"]["pid"]
            query("stop")
            checkpoints = list((root / "cache/workspaces").glob("*/checkpoint.json"))
            assert len(checkpoints) == 1, checkpoints
            # No prepare prewarming: measure a real navigation request first.
            resumed = query("definition", location, "--symbol", "value")
            for field in ("snapshot", "results", "status", "coverage"):
                assert resumed.get(field) == first.get(field), (field, first, resumed)
            prepared = query("prepare")
            assert prepared["context"]["resumed"], prepared
            assert prepared["results"][0]["detail"]["service"]["pid"] != old_pid
            logs = list((root / "cache/workspaces").glob("*/data/.metadata/.log"))
            assert logs, "missing JDTLS logs"
            for log in logs:
                last = log.read_text(errors="replace").rsplit("!SESSION", 1)[-1]
                assert "workspace exited with unsaved changes" not in last, last[:2000]
            print("PASS unchanged resume, new JVM, identical navigation, clean shutdown", flush=True)

            refreshed = query("refresh")
            assert not refreshed["context"]["resumed"], refreshed
            assert refreshed["snapshot"] == first["snapshot"], refreshed
            print("PASS explicit refresh bypasses resume shortcut", flush=True)

            query("stop")
            api.write_text(api.read_text().replace('"before"', '"after"'))
            extra = source / "Extra.java"
            extra.write_text(caller_text.replace("class Caller", "class Extra"))
            edited = query("definition", location, "--symbol", "value")
            assert "after" in edited["results"][0]["snippet"], edited
            assert edited["snapshot"] != first["snapshot"]
            refs = query("references", location, "--symbol", "value")
            assert any(r["path"].endswith("Extra.java") for r in refs["results"]), refs
            print("PASS edit and add while stopped", flush=True)

            query("stop")
            extra.unlink()
            refs = query("references", location, "--symbol", "value")
            assert not any(r["path"].endswith("Extra.java") for r in refs["results"]), refs
            assert any(r["path"].endswith("Caller.java") for r in refs["results"]), refs
            print("PASS delete while stopped", flush=True)

            query("stop")
            # A consumed/missing checkpoint simulates an unclean previous run.
            for checkpoint in (root / "cache/workspaces").glob("*/checkpoint.json"):
                checkpoint.unlink()
            recovered = query("definition", location, "--symbol", "value")
            assert "after" in recovered["results"][0]["snippet"], recovered
            assert not query("prepare")["context"]["resumed"]
            print("PASS missing checkpoint takes conservative recovery path", flush=True)
        finally:
            query("stop")


if __name__ == "__main__":
    main()
