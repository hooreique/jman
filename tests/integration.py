"""Real Gradle + JDTLS integration tests. No language-model API calls."""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "bench"))
from fixture import evaluate, materialize


def main():
    binary = str(Path(sys.argv[1]).resolve()) if len(sys.argv) > 1 else "jman"
    with tempfile.TemporaryDirectory(prefix="jman-integration-") as tmp:
        root = Path(tmp)
        fixture = materialize(root / "fixture")
        project = Path(fixture["project"])
        socket = root / "service.sock"
        env = dict(os.environ, JMAN_SOCKET=str(socket), JMAN_CACHE_HOME=str(root / "cache"), GRADLE_USER_HOME=str(root / "gradle"))
        log = (root / "daemon.log").open("w")
        daemon = subprocess.Popen([binary, "daemon", "--max-sessions", "2"], env=env, stdout=log, stderr=log)
        try:
            until = time.monotonic() + 10
            while not socket.exists():
                if daemon.poll() is not None or time.monotonic() > until:
                    raise AssertionError("daemon startup failed")
                time.sleep(.05)

            def query(command, *args, expected=(0,), timeout=180):
                result = subprocess.run([binary, command, *args, "--json", "--timeout", f"{timeout}s"], cwd=project, env=env, capture_output=True, text=True, timeout=timeout+10)
                if result.returncode not in expected:
                    raise AssertionError(f"{command}: {result.returncode}\n{result.stdout}\n{result.stderr}")
                return json.loads(result.stdout)

            before = evaluate(project, fixture["binary"])
            assert not before["success"], "fixture is not initially broken"
            prepared = query("prepare", expected=(0, 3))
            assert not prepared.get("warnings"), prepared
            location = fixture["location"]
            found = query("definition", location, "--symbol", "normalize")
            definition = found["results"][0]
            assert definition["artifact"] == fixture["artifact"], found
            assert definition["binaryDigest"] == fixture["binaryDigest"], found
            assert "toUpperCase" in definition["snippet"] and "toLowerCase" not in definition["snippet"], found
            assert definition["origin"] == "dependency-source", found
            assert Path(definition["path"]).is_file()
            assert found["context"]["sourceSet"] == "main", found
            print("PASS actual binary binding beats same-FQN decoy", flush=True)

            refs = query("references", location, "--symbol", "normalize")
            assert any(v["path"].endswith("AccessService.java") for v in refs["results"]), refs
            assert all("legacy" not in v.get("path", "") for v in refs["results"]), refs
            print("PASS semantic references exclude obsolete declaration", flush=True)

            source = project / "app/src/main/java/example/AccessService.java"
            original = source.read_text()
            source.write_text(original.replace('.equals("admin")', '.equals("ADMIN")'))
            updated = query("definition", location, "--symbol", "normalize")
            assert updated["snapshot"] != found["snapshot"], updated
            assert evaluate(project, fixture["binary"])["success"]
            print("PASS disk edit synchronization and independent evaluator", flush=True)

            hover = query("hover", location, "--symbol", "normalize")
            assert hover["results"], hover
            ambiguous = query("definition", "app/src/main/java/example/AccessService.java:11", "--symbol", "println", expected=(0, 3))
            assert ambiguous["results"], ambiguous

            # Add a second call, proving refresh of files outside the opened document.
            extra = project / "app/src/main/java/example/Other.java"
            extra.write_text('package example; class Other { String x() { return com.acme.text.TextUtil.normalize("x"); } }\n')
            paged = query("references", location, "--symbol", "normalize", "--limit", "1")
            assert paged.get("nextCursor"), paged
            page2 = query("references", location, "--symbol", "normalize", "--limit", "1", "--cursor", paged["nextCursor"])
            assert page2["results"][0]["path"] != paged["results"][0]["path"], page2
            extra.unlink()
            stale = query("references", location, "--symbol", "normalize", "--cursor", paged["nextCursor"], expected=(4,))
            assert stale["error"]["code"] == "STALE_CURSOR", stale
            print("PASS add/delete synchronization and snapshot-bound pagination", flush=True)

            sources = Path(fixture["binary"]).with_name("shared-text-2.4.1-sources.jar")
            sources.unlink()
            query("refresh", expected=(0, 3))
            fallback = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            assert fallback["results"][0]["origin"] in ("decompiled", "binary-only"), fallback
            print("PASS missing sources are not represented as original source", flush=True)
        except BaseException:
            # Preserve actionable logs outside the automatically removed fixture.
            import shutil
            dest = Path(os.environ.get("JMAN_TEST_FAILURES", "local/integration-failure"))
            if dest.exists():
                shutil.rmtree(dest)
            shutil.copytree(root, dest, ignore=shutil.ignore_patterns("*.sock", "*.lock"))
            print(f"Failure data: {dest}", file=sys.stderr)
            raise
        finally:
            daemon.send_signal(signal.SIGTERM)
            try:
                daemon.wait(timeout=15)
            except subprocess.TimeoutExpired:
                daemon.kill()
                daemon.wait()
            log.close()


if __name__ == "__main__":
    main()
