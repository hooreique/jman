import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "bench"))
from resource_bench import copy_gradle_seed, load_manifest, main, percentile, response_result, signal_observed


FAKE = r'''
import collections
import http.client
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
import signal
import socket
import sys
import threading
import time

args = sys.argv[1:]
path = os.environ.get("JMAN_SOCKET")
if args == ["--version"]:
    print("fake-jman 1")
    sys.exit(0)
if args[0] == "daemon":
    maximum = int(args[args.index("--max-sessions") + 1])
    idle = float(args[args.index("--idle-timeout") + 1].rstrip("s"))
    sessions = collections.OrderedDict()
    sequence = 100000
    class Server(HTTPServer):
        address_family = socket.AF_UNIX
        def server_bind(self):
            socket.socket.bind(self.socket, self.server_address)
            self.server_name = "localhost"
            self.server_port = 0
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_GET(self):
            self.answer({"pid": os.getpid()})
        def answer(self, value):
            encoded = json.dumps(value).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)
        def do_POST(self):
            global sequence
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            command = request["command"]
            now = time.monotonic()
            for project in list(sessions):
                if now - sessions[project]["used"] > idle:
                    del sessions[project]
            response = {"status": "ok", "results": []}
            if command == "stop":
                threading.Timer(.03, lambda: os.kill(os.getpid(), signal.SIGTERM)).start()
            elif command == "status":
                response["results"] = [{"detail": dict(value, root=project)} for project, value in sessions.items()]
            else:
                project = request["project"]
                if project not in sessions:
                    if len(sessions) >= maximum:
                        sessions.popitem(last=False)
                    sequence = sequence + 1 if not os.environ.get("JMAN_FAKE_DUPLICATE_PID") else 100000
                    sessions[project] = {"pid": sequence, "state": "ready"}
                sessions[project]["used"] = now
                sessions.move_to_end(project)
                if command == "definition":
                    response["results"] = [{"path": project + "/src/Target.java"}]
                else:
                    response["results"] = [{"name": "workspace"}]
            self.answer(response)
            if command == "prepare" and os.environ.get("JMAN_FAKE_REPLACE_DAEMON"):
                if os.fork():
                    os._exit(0)
                os.environ.pop("JMAN_FAKE_REPLACE_DAEMON")
    server = Server(path, Handler)
    server.serve_forever()
else:
    connection = http.client.HTTPConnection("localhost")
    connection.sock = socket.socket(socket.AF_UNIX)
    connection.sock.connect(path)
    project = args[args.index("--project") + 1] if "--project" in args else None
    connection.request("POST", "/query", json.dumps({"command": args[0], "project": project}))
    print(connection.getresponse().read().decode())
    if os.environ.get("JMAN_FAKE_REPLACE_DAEMON"):
        time.sleep(.1)
'''


class ResourceBenchmarkTests(unittest.TestCase):
    def repository(self, directory):
        source = directory / "source"
        source.mkdir()
        (source / "settings.gradle").write_text("rootProject.name = 'fake'\n")
        (source / "src").mkdir()
        (source / "src/Target.java").write_text("class Target { int value = 1; }\n")
        wrapper = source / "gradlew"
        wrapper.write_text("#!/bin/sh\nexit 0\n")
        wrapper.chmod(0o755)
        for argv in (["git", "init", "-q"], ["git", "add", "."],
                     ["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"]):
            subprocess.run(argv, cwd=source, check=True, capture_output=True)
        executable = directory / "fake-jman"
        executable.write_text(f"#!{sys.executable}\n" + FAKE)
        executable.chmod(0o755)
        return source, executable

    def arguments(self, source, executable, output, manifest):
        return ["--repository", str(source), "--ref", "HEAD", "--output", str(output),
                "--jman", str(executable), "--queries", str(manifest), "--sessions", "1,3",
                "--repetitions", "1", "--query-rounds", "1", "--refresh-rounds", "1",
                "--steady-seconds", "0", "--timeout", "5", "--sample-period", ".05"]

    def test_real_worktrees_queries_refresh_eviction_and_source_unchanged(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, executable = self.repository(root)
            manifest = root / "queries.json"
            manifest.write_text(json.dumps({"queries": [{"name": "definition", "args": ["definition", "src/Target.java:1"],
                                                       "expect": {"minResults": 1, "resultPathSuffix": "/src/Target.java"}}],
                                            "edits": [{"path": "src/Target.java", "find": "value = 1", "replace": "value = 2"}]}))
            output = root / "output"
            with patch.dict(os.environ, {"JMAN_GRADLE": str(source / "gradlew")}):
                code = main(self.arguments(source, executable, output, manifest) + ["--lifecycle", "--jdtls-max-heap-mib", "512"])
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual(code, 0, summary)
            self.assertTrue(summary["complete"])
            for run in summary["runs"]:
                self.assertTrue(run["success"])
                self.assertIn("first-queries", run["phases"])
                self.assertIn("refresh-1", run["phases"])
                self.assertIn("eviction", run["phases"])
                self.assertEqual(len(run["sessionChecks"][0]["sessions"]), run["metadata"]["sessions"])
                for project in run["metadata"]["projects"]:
                    self.assertTrue((Path(project) / ".git").is_file())
                    self.assertEqual(json.loads((Path(project) / ".jman.json").read_text())["jdtlsMaxHeapMiB"], 512)
            self.assertEqual(subprocess.check_output(["git", "status", "--porcelain"], cwd=source), b"")
            self.assertEqual(len(subprocess.check_output(["git", "worktree", "list", "--porcelain"], cwd=source).split(b"worktree ")), 2)

    def test_failed_correctness_is_nonzero_and_recorded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, executable = self.repository(root)
            manifest = root / "queries.json"
            manifest.write_text(json.dumps({"queries": [{"args": ["definition", "src/Target.java:1"],
                                                        "expect": {"resultPathSuffix": "/Wrong.java"}}]}))
            output = root / "output"
            arguments = self.arguments(source, executable, output, manifest)
            arguments[arguments.index("1,3")] = "1"
            with patch.dict(os.environ, {"JMAN_GRADLE": str(source / "gradlew")}):
                self.assertEqual(main(arguments), 1)
            run = json.loads((output / "summary.json").read_text())["runs"][0]
            self.assertFalse(run["success"])
            self.assertEqual(run["phases"]["first-queries"]["failures"], 1)
            self.assertTrue(run["errors"])
            self.assertTrue(all(cleanup["returncode"] == 0 for cleanup in run["gradleCleanup"]))

    def test_seed_copies_dependencies_config_without_daemon_or_locks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, target = root / "seed", root / "target"
            source.mkdir()
            target.mkdir()
            (source / "caches").mkdir()
            (source / "caches/artifact.jar").write_text("jar")
            (source / "caches/state.lock").write_text("lock")
            (source / "daemon").mkdir()
            (source / "daemon/registry.bin").write_text("registry")
            (source / "gradle.properties").write_text("org.gradle.java.installations.auto-download=false\n")
            copy_gradle_seed(source, target)
            self.assertTrue((target / "caches/artifact.jar").is_file())
            self.assertTrue((target / "gradle.properties").is_file())
            self.assertFalse((target / "caches/state.lock").exists())
            self.assertFalse((target / "daemon").exists())

    def test_duplicate_session_pid_fails_observed_concurrency_check(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, executable = self.repository(root)
            manifest = root / "queries.json"
            manifest.write_text(json.dumps({"queries": [{"args": ["prepare"]}]}))
            output = root / "output"
            arguments = self.arguments(source, executable, output, manifest)
            arguments[arguments.index("1,3")] = "3"
            with patch.dict(os.environ, {"JMAN_GRADLE": str(source / "gradlew"), "JMAN_FAKE_DUPLICATE_PID": "1"}):
                self.assertEqual(main(arguments), 1)
            run = json.loads((output / "summary.json").read_text())["runs"][0]
            self.assertFalse(run["sessionChecks"][0]["success"])
            self.assertNotIn("warm-queries", run["phases"])

    def test_replacement_daemon_fails_trial_and_private_socket_is_stopped(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, executable = self.repository(root)
            manifest = root / "queries.json"
            manifest.write_text(json.dumps({"queries": [{"args": ["prepare"]}]}))
            output = root / "output"
            arguments = self.arguments(source, executable, output, manifest)
            arguments[arguments.index("1,3")] = "1"
            with patch.dict(os.environ, {"JMAN_GRADLE": str(source / "gradlew"), "JMAN_FAKE_REPLACE_DAEMON": "1"}):
                self.assertEqual(main(arguments), 1)
            run = json.loads((output / "summary.json").read_text())["runs"][0]
            self.assertFalse(run["success"])
            events = [json.loads(line) for line in (output / "001-1-sessions/events.jsonl").read_text().splitlines()]
            command_events = [event for event in events if event["type"] == "command"]
            self.assertTrue(any(event["result"].get("daemonIdentityError") for event in command_events))
            self.assertTrue(any(event["name"] == "stop" and event["result"]["success"] for event in command_events))
            self.assertEqual(run["residualProcesses"], [])

    def test_cleanup_refuses_reused_pid(self):
        class Reader:
            def snapshot(self):
                return {123: {"started": "replacement"}}, {}
        with patch("resource_bench.os.pidfd_open", return_value=10, create=True), \
                patch("resource_bench.os.close"), patch("resource_bench.os.kill") as kill, \
                patch("resource_bench.signal.pidfd_send_signal", create=True) as pidfd_signal:
            self.assertFalse(signal_observed(Reader(), {"pid": 123, "started": "original"}, 15))
            kill.assert_not_called()
            pidfd_signal.assert_not_called()

    def test_failure_and_timeout_never_count_as_valid_responses(self):
        for code, timeout, status in [(4, False, "ok"), (0, True, "ok"), (0, False, "partial")]:
            result = response_result({"returncode": code, "timeout": timeout, "stdout": json.dumps({"status": status})})
            self.assertFalse(result["success"])
        self.assertEqual(percentile([1, 2, 3], 95), 3)
        self.assertIsNone(percentile([], 50))

    def test_manifest_rejects_shell_or_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "queries.json"
            for manifest in ({"queries": [{"args": "jman prepare; echo bad"}]},
                             {"queries": [{"args": ["stop"]}]},
                             {"queries": [{"args": ["prepare", "--project=/outside"]}]},
                             {"queries": [{"args": ["prepare"]}], "edits": [{"path": "../outside", "find": "a", "replace": "b"}]}):
                path.write_text(json.dumps(manifest))
                with self.assertRaises(ValueError):
                    load_manifest(path)


if __name__ == "__main__":
    unittest.main()
