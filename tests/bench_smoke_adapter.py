"""Deterministic runner plumbing check. This is NOT an AI agent or cost evidence."""
import json
import os
from pathlib import Path
import subprocess

config = json.loads(Path(os.environ["JMAN_BENCH_INPUT"]).read_text())
is_jman = "Available additional tool: jman" in config["prompt"]
result = subprocess.run(["jman", "--version"], capture_output=True, text=True)
assert (result.returncode == 0) == is_jman, result
source = Path(config["project"]) / "app/src/main/java/example/AccessService.java"
source.write_text(source.read_text().replace('.equals("admin")', '.equals("ADMIN")'))
Path(config["output"]).write_text(json.dumps({
    "reason": "completed", "usage": None,
    "answer": "Smoke reference fix; com.acme:shared-text:2.4.1 uppercases the value.",
    "synthetic": True,
}))
