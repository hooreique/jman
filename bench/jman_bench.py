#!/usr/bin/env python3
"""Reproducible paired agent trials with raw usage, independent evaluation and reporting.

Uses only the Python standard library. Live agents use an OpenAI-compatible chat
completions endpoint; command adapters can integrate other harnesses without
pretending character counts are provider token usage.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import random
import shutil
import signal
import statistics
import subprocess
import sys
import time
import urllib.request

from fixture import SOURCE, evaluate, materialize, run


def dump(path, value):
    Path(path).write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


class Events:
    def __init__(self, path):
        self.file = Path(path).open("x")
        self.start = time.monotonic()

    def emit(self, kind, **fields):
        self.file.write(json.dumps({"type": kind, "elapsed": time.monotonic() - self.start, **fields}, ensure_ascii=False) + "\n")
        self.file.flush()

    def close(self):
        self.file.close()


def command(argv, cwd, env, timeout):
    start = time.monotonic()
    process = subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    timed_out = False
    try:
        stdout, stderr = process.communicate(timeout=max(0.1, timeout))
    except subprocess.TimeoutExpired:
        timed_out = True
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate()
    return {"returncode": process.returncode, "stdout": stdout, "stderr": stderr,
            "timeout": timed_out, "seconds": time.monotonic() - start}


def shell_tool(script, project, env, deadline, events):
    events.emit("tool_start", tool="shell", arguments={"command": script})
    result = command(["bash", "-lc", script], project, env, min(120, deadline - time.monotonic()))
    # Record full output in raw events; bound model context equally in both arms.
    events.emit("tool_end", tool="shell", result=result, outputBytes=len((result["stdout"] + result["stderr"]).encode()))
    return json.dumps({**result, "stdout": result["stdout"][:24000], "stderr": result["stderr"][:8000],
                       "truncated": len(result["stdout"]) > 24000 or len(result["stderr"]) > 8000})


def live_agent(args, project, env, prompt, events, deadline):
    key = os.environ.get(args.api_key_env)
    if not key:
        raise RuntimeError(f"{args.api_key_env} is unset; supply credentials or use --adapter-command")
    messages = [{"role": "system", "content": "You are a Java coding agent. Solve the user's task in the current workspace. Use shell to inspect, edit and test files. Do not read outside the supplied fixture repositories or tooling. Finish with a concise explanation including dependency identity."}, {"role": "user", "content": prompt}]
    tools = [{"type": "function", "function": {"name": "shell", "description": "Run a shell command in the Java project", "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"], "additionalProperties": False}}}]
    total = {"input": 0, "output": 0, "cacheRead": 0, "reasoning": 0}
    for turn in range(args.max_turns):
        if time.monotonic() >= deadline:
            return {"reason": "timeout", "usage": total}
        body = {"model": args.model, "messages": messages, "tools": tools}
        events.emit("model_request", turn=turn, request=body)
        request = urllib.request.Request(args.base_url.rstrip("/") + "/chat/completions", data=json.dumps(body).encode(), headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=max(1, deadline - time.monotonic())) as response:
            data = json.load(response)
        events.emit("model_response", turn=turn, response=data)
        usage = data.get("usage")
        if usage is None:
            return {"reason": "usage-unavailable", "usage": None}
        total["input"] += usage.get("prompt_tokens", 0)
        total["output"] += usage.get("completion_tokens", 0)
        total["cacheRead"] += usage.get("prompt_tokens_details", {}).get("cached_tokens", 0)
        total["reasoning"] += usage.get("completion_tokens_details", {}).get("reasoning_tokens", 0)
        message = data["choices"][0]["message"]
        messages.append(message)
        if not message.get("tool_calls"):
            return {"reason": "completed", "usage": total, "answer": message.get("content", "")}
        for call in message["tool_calls"]:
            try:
                arguments = json.loads(call["function"]["arguments"])
                if call["function"]["name"] != "shell":
                    raise ValueError("unknown tool")
                result = shell_tool(arguments["command"], project, env, deadline, events)
            except (KeyError, ValueError) as error:
                result = json.dumps({"error": str(error)})
            messages.append({"role": "tool", "tool_call_id": call["id"], "content": result})
        if args.max_tokens and total["input"] + total["output"] >= args.max_tokens:
            return {"reason": "token-budget", "usage": total}
    return {"reason": "turn-budget", "usage": total}


def adapter_agent(args, project, env, prompt, events, deadline, folder):
    """External harness receives input JSON on disk and emits a result JSON file.

    This is an adapter protocol, not an assertion that arbitrary harnesses report
    complete usage. The raw event path and usage provenance must be supplied.
    """
    config = {"schemaVersion": 1, "project": str(project), "prompt": prompt,
              "deadlineSeconds": max(0, deadline - time.monotonic()), "maxTokens": args.max_tokens,
              "model": args.model, "output": str(folder / "adapter-result.json")}
    dump(folder / "adapter-input.json", config)
    child = dict(env, JMAN_BENCH_INPUT=str(folder / "adapter-input.json"))
    result = command(["bash", "-lc", args.adapter_command], project, child, deadline - time.monotonic())
    events.emit("adapter_execution", result=result)
    if result["returncode"] != 0:
        return {"reason": "adapter-failed", "usage": None}
    result = json.loads((folder / "adapter-result.json").read_text())
    if "reason" not in result:
        raise ValueError("adapter result requires reason")
    return result


def terminate_daemon(socket):
    # Only stop the daemon with the isolated socket created for this run.
    import socket as sockets
    try:
        client = sockets.socket(sockets.AF_UNIX)
        client.settimeout(2)
        client.connect(str(socket))
        client.sendall(b"GET /health HTTP/1.0\r\nHost: jman\r\n\r\n")
        chunks = []
        while data := client.recv(4096):
            chunks.append(data)
        client.close()
        metadata = json.loads(b"".join(chunks).split(b"\r\n\r\n", 1)[1])
        os.kill(metadata["pid"], signal.SIGTERM)
    except (OSError, ValueError, KeyError):
        pass


def trial(args, root, repetition, arm):
    folder = root / f"{repetition:03d}-{arm}"
    folder.mkdir()
    metadata = materialize(folder / "workspace")
    project = Path(metadata["project"])
    env = dict(os.environ)
    cache = folder / "cache"
    cache.mkdir()
    # Short sockets avoid AF_UNIX's ~108-byte path limit.
    socket = Path(os.environ.get("TMPDIR", "/tmp")) / ("jbench-" + hashlib.sha256(str(folder).encode()).hexdigest()[:16] + ".sock")
    env.update(JMAN_SOCKET=str(socket), JMAN_CACHE_HOME=str(cache / "jman"), GRADLE_USER_HOME=str(cache / "gradle"))
    prompt = (project / "task.md").read_text()
    skill = SOURCE.parent / "skills/jman/SKILL.md"
    if arm == "jman":
        prompt += "\n\nAvailable additional tool: jman. Instructions:\n" + skill.read_text()
    events = Events(folder / "events.jsonl")
    prepare_seconds = 0
    try:
        if args.cache == "dependency-warm" or args.cache == "jdtls-warm":
            prepared = command(["gradle", "--offline", ":app:classes"], project, env, args.timeout)
            events.emit("dependency_prepare", result=prepared)
            if prepared["returncode"]:
                raise RuntimeError("dependency preparation failed")
        if args.cache == "jdtls-warm" and arm == "jman":
            prepared = command([args.jman, "prepare", "--timeout", f"{args.timeout}s", "--json"], project, env, args.timeout + 5)
            prepare_seconds = prepared["seconds"]
            events.emit("jdtls_prepare", result=prepared)
            if prepared["returncode"]:
                raise RuntimeError("JDTLS preparation failed")
        metadata.update(arm=arm, repetition=repetition, model=args.model, cache=args.cache,
                        skillHash=hashlib.sha256(skill.read_bytes()).hexdigest() if arm == "jman" else None,
                        adapter="command" if args.adapter_command else "chat-completions",
                        started=time.time(), fixtureCommit=run(["git", "rev-parse", "HEAD"], project).stdout.strip(),
                        jmanVersion=command([args.jman, "--version"], project, env, 10)["stdout"].strip())
        events.emit("run_start", metadata=metadata)
        start = time.monotonic()
        deadline = start + args.timeout
        if args.adapter_command:
            result = adapter_agent(args, project, env, prompt, events, deadline, folder)
        else:
            result = live_agent(args, project, env, prompt, events, deadline)
        seconds = time.monotonic() - start
        patch = run(["git", "diff", "--binary"], project).stdout
        (folder / "patch.diff").write_text(patch)
        # Reconstruct the clean fixture for evaluation; only apply application edits.
        clean = materialize(folder / "evaluation-workspace")
        clean_project = Path(clean["project"])
        allowed = ["app/src/main/java/example/AccessService.java"]
        changed = run(["git", "diff", "--name-only"], project).stdout.splitlines()
        for relative in allowed:
            target = clean_project / relative
            candidate = project / relative
            if candidate.is_file() and not candidate.is_symlink():
                target.write_bytes(candidate.read_bytes())
        verdict = evaluate(clean_project, clean["binary"])
        verdict["allowedEdits"] = all(p in allowed for p in changed)
        verdict["success"] = verdict["success"] and verdict["allowedEdits"]
        events.emit("evaluation", result=verdict)
        output = {"metadata": metadata, "agent": result, "evaluation": verdict, "seconds": seconds, "prepareSeconds": prepare_seconds}
    except Exception as error:
        events.emit("run_error", error=f"{type(error).__name__}: {error}")
        output = {"metadata": {"arm": arm, "repetition": repetition, "model": args.model, "cache": args.cache},
                  "agent": {"reason": "error", "usage": None}, "evaluation": {"success": False}, "error": str(error)}
    finally:
        terminate_daemon(socket)
        events.close()
    dump(folder / "result.json", output)
    return output


def report(root):
    results = [json.loads(p.read_text()) for p in sorted(root.glob("*-*/result.json"))]
    arms = {}
    for arm in sorted({r["metadata"]["arm"] for r in results}):
        runs = [r for r in results if r["metadata"]["arm"] == arm]
        complete_usage = [r["agent"]["usage"] for r in runs if r["agent"].get("usage") is not None]
        success = sum(r["evaluation"]["success"] for r in runs)
        arms[arm] = {"runs": len(runs), "successes": success, "successRate": success / len(runs),
                     "medianSeconds": statistics.median(r["seconds"] for r in runs if "seconds" in r) if any("seconds" in r for r in runs) else None,
                     "usageCoverage": len(complete_usage) / len(runs),
                     "inputTokens": sum(u.get("input", 0) for u in complete_usage) if len(complete_usage) == len(runs) else None,
                     "outputTokens": sum(u.get("output", 0) for u in complete_usage) if len(complete_usage) == len(runs) else None}
    output = {"schemaVersion": 1, "arms": arms, "runs": len(results),
              "limitations": ["One fixture family; exploratory results, not a population estimate.",
                              "Missing provider usage is unavailable, not estimated from characters.",
                              "Current shell adapter is for trusted agents on disposable environments; evaluator files are created only after the agent exits.",
                              "Peak RSS and provider pricing are not synthesized; retain raw events for subsequent accounting."]}
    dump(root / "report.json", output)
    lines = ["# jman benchmark", "", "| Arm | Success | Median seconds | Input tokens | Output tokens |", "|---|---:|---:|---:|---:|"]
    for arm, value in arms.items():
        lines.append(f"| {arm} | {value['successes']}/{value['runs']} | {value['medianSeconds']} | {value['inputTokens']} | {value['outputTokens']} |")
    lines += ["", "## Limitations", *["- " + v for v in output["limitations"]]]
    (root / "report.md").write_text("\n".join(lines) + "\n")
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    fixture = commands.add_parser("fixture")
    fixture.add_argument("destination", type=Path)
    validate = commands.add_parser("validate")
    validate.add_argument("--output", type=Path, required=True)
    runs = commands.add_parser("run")
    runs.add_argument("--output", type=Path, required=True)
    runs.add_argument("--model", required=True)
    runs.add_argument("--base-url", default="https://api.openai.com/v1")
    runs.add_argument("--api-key-env", default="OPENAI_API_KEY")
    runs.add_argument("--adapter-command")
    runs.add_argument("--arms", default="baseline,jman")
    runs.add_argument("--repetitions", type=int, default=5)
    runs.add_argument("--timeout", type=float, default=600)
    runs.add_argument("--max-turns", type=int, default=30)
    runs.add_argument("--max-tokens", type=int, default=200000)
    runs.add_argument("--seed", type=int, default=1729)
    runs.add_argument("--cache", choices=["cold", "dependency-warm", "jdtls-warm"], default="dependency-warm")
    runs.add_argument("--jman", default="jman")
    reports = commands.add_parser("report")
    reports.add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.command == "fixture":
        print(json.dumps(materialize(args.destination), indent=2))
    elif args.command == "validate":
        metadata = materialize(args.output)
        before = evaluate(metadata["project"], metadata["binary"])
        source = Path(metadata["project"]) / "app/src/main/java/example/AccessService.java"
        original = source.read_text()
        source.write_text(original.replace('.equals("admin")', '.equals("ADMIN")'))
        after = evaluate(metadata["project"], metadata["binary"])
        source.write_text(original)
        assert not before["success"] and after["success"], (before, after)
        print(json.dumps({"baseline": before, "referenceFix": after}, indent=2))
    elif args.command == "report":
        print(json.dumps(report(args.directory.resolve()), indent=2))
    else:
        if not args.adapter_command and not os.environ.get(args.api_key_env):
            parser.error(f"{args.api_key_env} is unset; live agent trials require credentials or --adapter-command")
        if args.repetitions < 1 or args.timeout <= 0:
            parser.error("repetitions and timeout must be positive")
        arms = args.arms.split(",")
        if len(set(arms)) != len(arms) or any(arm not in ("baseline", "jman") for arm in arms):
            parser.error("arms must be baseline,jman without duplicates")
        root = args.output.resolve()
        root.mkdir(parents=True, exist_ok=False)
        args.jman = shutil.which(args.jman) or args.jman
        dump(root / "experiment.json", {k: str(v) if isinstance(v, Path) else v for k, v in vars(args).items()})
        schedule = [(i, arm) for i in range(args.repetitions) for arm in arms]
        random.Random(args.seed).shuffle(schedule)
        for repetition, arm in schedule:
            result = trial(args, root, repetition, arm)
            print(f"{repetition:03d} {arm}: {result['agent']['reason']}, success={result['evaluation']['success']}", flush=True)
        print(json.dumps(report(root), indent=2))


if __name__ == "__main__":
    main()
