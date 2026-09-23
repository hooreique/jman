import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "bench"))
from jman_bench import report


class BenchmarkTests(unittest.TestCase):
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
