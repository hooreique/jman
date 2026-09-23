import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "bench"))
from jman_bench import Events, live_agent, report
from fixture import evaluate, materialize


class BenchmarkTests(unittest.TestCase):
    def test_nonterminating_solution_fails_evaluation(self):
        with tempfile.TemporaryDirectory() as tmp:
            fixture = materialize(Path(tmp) / "fixture")
            source = Path(fixture["project"]) / "app/src/main/java/example/AccessService.java"
            source.write_text(source.read_text().replace("System.out.println(new AccessService().isAdmin(args[0]));", "while (true) {}"))
            result = evaluate(fixture["project"], fixture["binary"])
            self.assertFalse(result["success"])
            self.assertEqual(result["reason"], "runtime-timeout")

    def test_tool_loop_uses_provider_usage_without_double_counting(self):
        from types import SimpleNamespace
        calls = []

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                calls.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                if len(calls) == 1:
                    message = {"role": "assistant", "content": None, "tool_calls": [{"id": "call-1", "type": "function", "function": {"name": "shell", "arguments": json.dumps({"command": "test -z \"$JMAN_TEST_API_KEY\" && echo inspected"})}}]}
                else:
                    message = {"role": "assistant", "content": "Done"}
                response = {"choices": [{"message": message}], "usage": {"prompt_tokens": 100, "completion_tokens": 20, "prompt_tokens_details": {"cached_tokens": 30}, "completion_tokens_details": {"reasoning_tokens": 5}}}
                encoded = json.dumps(response).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(encoded)))
                self.end_headers()
                self.wfile.write(encoded)

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        previous = os.environ.get("JMAN_TEST_API_KEY")
        os.environ["JMAN_TEST_API_KEY"] = "synthetic-secret"
        try:
            with tempfile.TemporaryDirectory() as tmp:
                args = SimpleNamespace(api_key_env="JMAN_TEST_API_KEY", base_url=f"http://127.0.0.1:{server.server_port}", model="synthetic-test", max_turns=3, max_tokens=1000)
                events = Events(Path(tmp) / "events.jsonl")
                try:
                    result = live_agent(args, tmp, dict(os.environ), "Inspect the project", events, time.monotonic()+10)
                finally:
                    events.close()
                self.assertEqual(result["usage"], {"input": 200, "output": 40, "cacheRead": 60, "reasoning": 10})
                self.assertEqual(result["reason"], "completed")
                self.assertIn("inspected", calls[1]["messages"][-1]["content"])
                self.assertNotIn("synthetic-secret", (Path(tmp) / "events.jsonl").read_text())
        finally:
            if previous is None:
                os.environ.pop("JMAN_TEST_API_KEY", None)
            else:
                os.environ["JMAN_TEST_API_KEY"] = previous
            server.shutdown()
            server.server_close()
            thread.join()

    def test_custom_project_and_evaluator(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            project = root / "template/app"
            project.mkdir(parents=True)
            (project / "Value.java").write_text("class Value { int value = 1; }")
            (root / "evaluate.py").write_text("from pathlib import Path\nassert 'value = 2' in Path('Value.java').read_text()\n")
            (root / "adapter.py").write_text("import json, os\nfrom pathlib import Path\nc=json.loads(Path(os.environ['JMAN_BENCH_INPUT']).read_text())\np=Path(c['project'])/'Value.java'\np.write_text(p.read_text().replace('value = 1','value = 2'))\nPath(c['output']).write_text(json.dumps({'reason':'completed','usage':None}))\n")
            suite = root / "suite.json"
            suite.write_text(json.dumps({"template": "template", "project": "app", "prompt": "Set value to 2", "allowedEdits": ["Value.java"], "evaluate": [sys.executable, "{suite}/evaluate.py"]}))
            output = root / "run"
            result = subprocess.run([sys.executable, str(ROOT / "bench/jman_bench.py"), "run", "--suite", str(suite), "--output", str(output), "--model", "synthetic-smoke-not-a-model", "--arms", "baseline", "--adapter-command", f"{sys.executable} {root / 'adapter.py'}", "--repetitions", "1", "--cache", "cold", "--jman", sys.argv[1] if len(sys.argv) > 1 else "jman"], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            report = json.loads((output / "report.json").read_text())
            self.assertEqual(report["arms"]["baseline"]["successes"], 1, result.stdout)

    def test_missing_usage_not_counted_as_zero(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for n, usage in enumerate([None, {"input": 100, "output": 50}]):
                folder = root / f"{n:03d}-baseline"
                folder.mkdir()
                (folder / "result.json").write_text(json.dumps({"metadata": {"arm": "baseline", "repetition": n}, "agent": {"usage": usage}, "evaluation": {"success": n == 1}, "seconds": n+1}))
            result = report(root)
            self.assertEqual(result["arms"]["baseline"]["usageCoverage"], .5)
            self.assertIsNone(result["arms"]["baseline"]["inputTokens"])
            self.assertEqual(result["arms"]["baseline"]["successRate"], .5)

    def test_runner_both_arms(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "experiment"
            result = subprocess.run([sys.executable, str(ROOT / "bench/jman_bench.py"), "run", "--output", str(output), "--model", "synthetic-smoke-not-a-model", "--adapter-command", f"{sys.executable} {ROOT / 'tests/bench_smoke_adapter.py'}", "--repetitions", "1", "--cache", "cold", "--jman", sys.argv[1] if len(sys.argv) > 1 and not sys.argv[1].startswith("-") else "jman"], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            report = json.loads((output / "report.json").read_text())
            for arm in ("baseline", "jman"):
                self.assertEqual(report["arms"][arm]["successes"], 1, result.stdout)
                self.assertEqual(report["arms"][arm]["usageCoverage"], 0)


if __name__ == "__main__":
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(BenchmarkTests)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    sys.exit(not result.wasSuccessful())
