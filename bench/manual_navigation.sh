#!/usr/bin/env bash
# Run three warm navigation samples and write raw timings plus JDTLS memory data.
# Set the three commands to complete, quoted jman invocations before running.
set -euo pipefail

: "${JMAN_BENCH_DEFINITION:?set a complete definition command}"
: "${JMAN_BENCH_REFERENCES:?set a complete references command}"
: "${JMAN_BENCH_IMPLEMENTATIONS:?set a complete implementations command}"

output=${1:-"./local/navigation-benchmark-$(date +%Y%m%d-%H%M%S)"}
mkdir -p "$output"

run_samples() {
  local name command samples
  name=$1
  command=$2
  samples="$output/$name.jsonl"
  : >"$samples"
  for _ in 1 2 3; do
    python3 - "$command" "$samples" <<'PY'
import json, subprocess, sys, time
command, destination = sys.argv[1:]
started = time.monotonic()
run = subprocess.run(command, shell=True, text=True, capture_output=True)
elapsed_ms = round((time.monotonic() - started) * 1000)
with open(destination, "a") as f:
    f.write(json.dumps({"command": command, "elapsedMs": elapsed_ms,
                        "exitCode": run.returncode, "stdout": run.stdout,
                        "stderr": run.stderr}, ensure_ascii=False) + "\n")
PY
  done
}

run_samples definition "$JMAN_BENCH_DEFINITION"
run_samples references "$JMAN_BENCH_REFERENCES"
run_samples implementations "$JMAN_BENCH_IMPLEMENTATIONS"

jman status --json >"$output/status.json" || true
if command -v jcmd >/dev/null; then
  python3 - "$output/status.json" <<'PY' >"$output/jdtls-heap.txt" || true
import json, subprocess, sys
for item in json.load(open(sys.argv[1])).get("results", []):
    pid = item.get("detail", {}).get("pid", 0)
    if pid:
        print(subprocess.check_output(["jcmd", str(pid), "GC.heap_info"], text=True))
PY
fi
ps -axo pid=,rss=,command= | grep '[j]dt.ls' >"$output/jdtls-rss.txt" || true
echo "wrote $output"
