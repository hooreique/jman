#!/usr/bin/env python3
"""Deterministic, model-free measurements of one daemon and real Git worktrees."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import math
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time

from resources import ResourceSampler


def dump(path, value):
    Path(path).write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n")


def command(argv, cwd, env, timeout):
    start = time.monotonic()
    process = subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, text=True, start_new_session=True)
    timed_out = False
    try:
        stdout, stderr = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        timed_out = True
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate()
    return {"argv": [str(a) for a in argv], "returncode": process.returncode,
            "stdout": stdout, "stderr": stderr, "timeout": timed_out,
            "seconds": time.monotonic() - start}


def checked(argv, cwd, env, timeout):
    result = command(argv, cwd, env, timeout)
    if result["returncode"]:
        raise RuntimeError(f"{argv[0]} failed: {result['stderr'][-4000:]}")
    return result["stdout"].strip()


def percentile(values, percent):
    """Nearest-rank percentiles; include the maximum for small p95 samples."""
    if not values:
        return None
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * percent / 100) - 1)]


def latency(results):
    values = [r["seconds"] for r in results]
    return {"count": len(values), "p50Seconds": percentile(values, 50),
            "p95Seconds": percentile(values, 95), "maxSeconds": max(values, default=None)}


def health(socket_path):
    try:
        with socket.socket(socket.AF_UNIX) as client:
            client.settimeout(.5)
            client.connect(str(socket_path))
            client.sendall(b"GET /health HTTP/1.0\r\nHost: jman\r\n\r\n")
            chunks = []
            while data := client.recv(65536):
                chunks.append(data)
        return json.loads(b"".join(chunks).split(b"\r\n\r\n", 1)[1])
    except (OSError, ValueError, IndexError):
        return None


def relative_path(value):
    path = Path(value)
    if path.is_absolute() or ".." in path.parts:
        raise ValueError(f"expected a path inside the worktree: {value}")
    return path


def load_manifest(path):
    if not path:
        return {"queries": [{"name": "warm-prepare", "args": ["prepare"]}], "edits": []}
    manifest = json.loads(Path(path).read_text())
    allowed = {"definition", "references", "implementations", "hover", "deps", "doctor", "prepare"}
    if not isinstance(manifest, dict) or not isinstance(manifest.get("queries"), list) or not manifest["queries"]:
        raise ValueError("query manifest requires a nonempty queries array")
    for query in manifest["queries"]:
        if not isinstance(query, dict):
            raise ValueError("queries must contain objects with args arrays")
        args = query.get("args")
        if not isinstance(args, list) or not args or not all(isinstance(a, str) for a in args):
            raise ValueError("queries[].args must be a nonempty string array")
        if args[0] not in allowed or any(a.split("=", 1)[0] in {"--project", "--timeout"} for a in args):
            raise ValueError("queries must use navigation/prepare/doctor commands without project/timeout overrides")
        expect = query.get("expect", {})
        if not isinstance(expect, dict):
            raise ValueError("query expect must be an object")
        if expect.get("status", "ok") not in {"ok", "not-found"}:
            raise ValueError("expected status must be ok or not-found; import failures are never successes")
        if "resultPathSuffix" in expect and (not isinstance(expect["resultPathSuffix"], str) or not expect["resultPathSuffix"]):
            raise ValueError("expect.resultPathSuffix must be a nonempty string")
        if not isinstance(expect.get("minResults", 0), int) or expect.get("minResults", 0) < 0:
            raise ValueError("expect.minResults must be a nonnegative integer")
    if not isinstance(manifest.get("edits", []), list):
        raise ValueError("edits must be an array")
    edit_paths = set()
    for edit in manifest.get("edits", []):
        if not isinstance(edit, dict) or not isinstance(edit.get("path"), str):
            raise ValueError("edits must contain objects with path strings")
        path = relative_path(edit["path"])
        if path in edit_paths:
            raise ValueError("use at most one replacement per edit path")
        edit_paths.add(path)
        if not isinstance(edit.get("find"), str) or not edit["find"] or not isinstance(edit.get("replace"), str):
            raise ValueError("edits require path, nonempty find, and replace strings")
        if edit["find"] == edit["replace"]:
            raise ValueError("edits must change file contents")
    return manifest


class Events:
    def __init__(self, path):
        self.file = Path(path).open("x")
        self.lock = threading.Lock()
        self.start = time.monotonic()

    def emit(self, kind, **fields):
        with self.lock:
            self.file.write(json.dumps({"type": kind, "elapsedSeconds": time.monotonic() - self.start,
                                        **fields}, ensure_ascii=False) + "\n")
            self.file.flush()

    def close(self):
        self.file.close()


def response_result(result, expect=None):
    expect = expect or {}
    try:
        response = json.loads(result["stdout"])
    except (ValueError, TypeError):
        response = {}
    result["response"] = response
    result["success"] = (result["returncode"] == 0 and not result["timeout"]
                         and response.get("status") == expect.get("status", "ok")
                         and len(response.get("results", [])) >= expect.get("minResults", 0)
                         and ("resultPathSuffix" not in expect or any(
                             r.get("path", "").endswith(expect["resultPathSuffix"])
                             for r in response.get("results", []))))
    return result


def session_roots(response):
    return {r["detail"]["root"]: r["detail"] for r in response.get("results", [])
            if isinstance(r.get("detail"), dict) and "root" in r["detail"]}


def copy_gradle_seed(source, target):
    """Copy dependencies/distributions only; never inherit daemon identities or locks."""
    source, target = Path(source), Path(target)
    if not source.is_dir():
        raise ValueError(f"Gradle seed is not a directory: {source}")
    if (source / "gradle.properties").is_file():
        shutil.copy2(source / "gradle.properties", target / "gradle.properties")
    for name in ("caches", "wrapper", "jdks"):
        if (source / name).exists():
            shutil.copytree(source / name, target / name,
                            ignore=shutil.ignore_patterns("*.lock", "*.lck", "gc.properties"))


def make_worktree(repository, root, index, commit, project, env, timeout):
    destination = root / f"worktree-{index:02d}"
    checked(["git", "worktree", "add", "--detach", str(destination), commit], repository, env, timeout)
    selected = (destination / relative_path(project)).resolve()
    if not selected.is_relative_to(destination.resolve()) or not selected.is_dir():
        raise ValueError(f"project directory does not exist inside worktree: {project}")
    return selected


def configure_project(project, max_heap):
    if max_heap is None:
        return
    path = project / ".jman.json"
    config = json.loads(path.read_text()) if path.is_file() else {}
    config["jdtlsMaxHeapMiB"] = max_heap
    dump(path, config)


def signal_observed(reader, identity, sig):
    """Signal only a currently matching process, using a pinned Linux PID handle."""
    descriptor = None
    try:
        if hasattr(os, "pidfd_open") and hasattr(signal, "pidfd_send_signal"):
            descriptor = os.pidfd_open(identity["pid"])
        processes, _ = reader.snapshot()
        current = processes.get(identity["pid"]) if processes is not None else None
        if not current or current["started"] != identity["started"]:
            return False
        if descriptor is not None:
            signal.pidfd_send_signal(descriptor, sig)
        else:
            os.kill(identity["pid"], sig)
        return True
    except ProcessLookupError:
        return False
    finally:
        if descriptor is not None:
            os.close(descriptor)


def run_trial(args, folder, repository, commit, count, repetition, manifest):
    folder.mkdir()
    env = dict(os.environ)
    cache = folder / "cache"
    cache.mkdir()
    gradle_home = cache / "gradle"
    gradle_home.mkdir()
    if args.gradle_home_seed:
        copy_gradle_seed(args.gradle_home_seed, gradle_home)
    # Keep the AF_UNIX pathname short even when reports are in deep repositories.
    socket_dir = Path(tempfile.mkdtemp(prefix="jresource-"))
    socket_path = socket_dir / "jman.sock"
    env.update(JMAN_SOCKET=str(socket_path), JMAN_CACHE_HOME=str(cache / "jman"),
               GRADLE_USER_HOME=str(gradle_home))
    events = Events(folder / "events.jsonl")
    sampler = None
    daemon = None
    daemon_log = None
    projects = []
    result = {"success": False, "metadata": {"sessions": count, "repetition": repetition,
              "commit": commit, "startup": args.startup,
              "cache": "dependency-seeded, new JDTLS workspace" if args.gradle_home_seed else "empty Gradle and JDTLS caches",
              "osPageCache": "uncontrolled", "jdtlsRuntime": "preinstalled; inherited JMAN_JDTLS/JMAN_JDTLS_HOME",
              "daemonIdleTimeoutSeconds": args.idle_timeout, "navigationQueries": bool(args.queries),
              "jdtlsMaxHeapMiBOverride": args.jdtls_max_heap_mib},
              "phases": {}, "sessionChecks": [], "errors": []}

    def phase(name):
        events.emit("phase", name=name)
        if sampler:
            sampler.phase(name)

    def invoke(project, arguments, name, expect=None, timeout=None):
        argv = [args.jman, *arguments, "--json", "--timeout", f"{args.timeout}s"]
        if project is not None:
            argv += ["--project", str(project)]
        before = health(socket_path) if arguments[0] != "stop" else None
        if arguments[0] != "stop" and (not daemon or daemon.poll() is not None
                                       or not before or before.get("pid") != daemon.pid):
            value = {"argv": argv, "returncode": -1, "timeout": False, "seconds": 0,
                     "stdout": "", "stderr": "owned daemon disappeared or changed identity",
                     "response": {}, "success": False, "daemonHealth": before}
            events.emit("command", name=name, project=str(project) if project else None, result=value)
            return value
        value = response_result(command(argv, project or folder, env, timeout or args.timeout + 5), expect)
        if arguments[0] != "stop":
            after = health(socket_path)
            value["daemonHealth"] = after
            if daemon.poll() is not None or not after or after.get("pid") != daemon.pid:
                value["success"] = False
                value["daemonIdentityError"] = "owned daemon disappeared or changed identity during command"
        events.emit("command", name=name, project=str(project) if project else None, result=value)
        return value

    def verify_sessions(expected, name, require_ready=True):
        observed = invoke(None, ["status"], name)
        sessions = session_roots(observed["response"])
        success = (observed["success"] and set(sessions) == {str(p) for p in expected}
                   and (not require_ready or all(s.get("state") == "ready" for s in sessions.values()))
                   and all(isinstance(s.get("pid"), int) and s["pid"] > 0 for s in sessions.values())
                   and len({s.get("pid") for s in sessions.values()}) == len(sessions))
        check = {"name": name, "expected": [str(p) for p in expected], "sessions": sessions, "success": success}
        result["sessionChecks"].append(check)
        events.emit("session_check", **check)
        if not success:
            raise RuntimeError(f"{name}: unexpected session count/roots/state; see events.jsonl")
        return sessions

    def phase_commands(name, operations, parallel=True):
        phase(name)
        started = time.monotonic()
        if parallel:
            with ThreadPoolExecutor(max_workers=count) as pool:
                values = list(pool.map(lambda op: invoke(*op), operations))
        else:
            values = [invoke(*op) for op in operations]
        result["phases"][name] = {"wallSeconds": time.monotonic() - started,
                                  "latency": latency(values), "commands": len(values),
                                  "failures": sum(not value["success"] for value in values)}
        if any(not value["success"] for value in values):
            raise RuntimeError(f"{name}: command failed, timed out, or returned unexpected JSON; see events.jsonl")
        return values

    def navigation(name, rounds=None):
        # One worker per worktree prevents artificial same-session gate contention.
        phase(name)
        started = time.monotonic()
        def for_project(project):
            values = []
            for round_number in range(args.query_rounds if rounds is None else rounds):
                for query in manifest["queries"]:
                    values.append(invoke(project, query["args"], query.get("name", query["args"][0]), query.get("expect")))
            return values
        with ThreadPoolExecutor(max_workers=count) as pool:
            batches = list(pool.map(for_project, projects))
        values = [value for batch in batches for value in batch]
        result["phases"][name] = {"wallSeconds": time.monotonic() - started, "latency": latency(values),
                                  "commands": len(values), "failures": sum(not value["success"] for value in values)}
        if any(not value["success"] for value in values):
            raise RuntimeError(f"{name}: a query failed correctness checks; see events.jsonl")

    try:
        projects = [make_worktree(repository, folder, n, commit, args.project, env, args.timeout)
                    for n in range(count)]
        for project in projects:
            configure_project(project, args.jdtls_max_heap_mib)
        result["metadata"]["projects"] = [str(p) for p in projects]
        daemon_log = (folder / "daemon.log").open("w")
        argv = [args.jman, "daemon", "--max-sessions", str(count), "--idle-timeout", f"{args.idle_timeout}s"]
        daemon = subprocess.Popen(argv, cwd=folder, env=env, stdout=daemon_log, stderr=subprocess.STDOUT,
                                  start_new_session=True)
        events.emit("daemon_start", argv=argv, pid=daemon.pid)
        sampler = ResourceSampler(socket=socket_path, gradle_home=gradle_home, root_pid=daemon.pid,
                                  period=args.sample_period, output=folder / "resources.jsonl")
        phase("daemon-start")
        deadline = time.monotonic() + min(args.timeout, 30)
        while not health(socket_path):
            if daemon.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("daemon did not start; see daemon.log")
            time.sleep(.05)
        phase_commands("cold-prepare", [(p, ["prepare"], "prepare") for p in projects], args.startup == "concurrent")
        verify_sessions(projects, "after-prepare")
        navigation("first-queries", rounds=1)
        verify_sessions(projects, "after-first-queries")
        phase("steady")
        time.sleep(args.steady_seconds)
        verify_sessions(projects, "after-steady")
        navigation("warm-queries")
        verify_sessions(projects, "after-queries")
        edits = manifest.get("edits", [])
        originals = {}
        for project in projects:
            for edit in edits:
                path = (project / relative_path(edit["path"])).resolve()
                if not path.is_relative_to(project) or not path.is_file():
                    raise ValueError(f"edit path is outside project or absent: {edit['path']}")
                original = path.read_text()
                if original.count(edit["find"]) != 1:
                    raise ValueError(f"edit find must match exactly once: {edit['path']}")
                originals[path] = (original, original.replace(edit["find"], edit["replace"], 1))
        for iteration in range(args.refresh_rounds if edits else 0):
            for path, versions in originals.items():
                path.write_text(versions[(iteration + 1) % 2])
            phase_commands(f"refresh-{iteration + 1}", [(p, ["refresh"], "refresh") for p in projects])
            navigation(f"queries-after-refresh-{iteration + 1}")
            verify_sessions(projects, f"after-refresh-{iteration + 1}")
        if args.lifecycle:
            extra = make_worktree(repository, folder, count, commit, args.project, env, args.timeout)
            configure_project(extra, args.jdtls_max_heap_mib)
            phase_commands("eviction", [(extra, ["prepare"], "eviction-prepare")])
            status = invoke(None, ["status"], "after-eviction")
            sessions = session_roots(status["response"])
            removed = [p for p in projects if str(p) not in sessions]
            if not status["success"] or len(sessions) != count or str(extra) not in sessions or len(removed) != 1:
                raise RuntimeError("eviction did not replace exactly one session")
            result["eviction"] = {"removed": str(removed[0]), "added": str(extra), "sessions": sessions}
            phase_commands("reopen", [(removed[0], ["prepare"], "reopen-prepare")])
            reopened = invoke(None, ["status"], "after-reopen")
            current = session_roots(reopened["response"])
            if not reopened["success"] or len(current) != count or str(removed[0]) not in current:
                raise RuntimeError("evicted session did not reopen within the configured limit")
            result["reopen"] = {"project": str(removed[0]), "sessions": current}
        if args.idle_seconds:
            phase("idle")
            time.sleep(args.idle_seconds)
            status = invoke(None, ["status"], "after-idle")
            sessions = session_roots(status["response"])
            sweep_seconds = 1 if args.idle_timeout < 60 else 60
            result["idle"] = {"seconds": args.idle_seconds, "remainingSessions": sessions,
                              "sweepIntervalSeconds": sweep_seconds,
                              "evictionExpected": args.idle_seconds >= args.idle_timeout + sweep_seconds}
            if not status["success"] or result["idle"]["evictionExpected"] and sessions:
                raise RuntimeError("idle eviction did not remove the expected sessions")
        result["success"] = True
    except Exception as error:
        result["errors"].append(f"{type(error).__name__}: {error}")
        events.emit("run_error", error=result["errors"][-1])
    finally:
        phase("stop")
        responder = health(socket_path)
        if responder:
            stopped = invoke(None, ["stop"], "stop", timeout=10)
            if not stopped["success"]:
                result["errors"].append("daemon stop command failed")
                result["success"] = False
        if daemon and daemon.poll() is None:
            try:
                daemon.wait(timeout=15)
            except (OSError, subprocess.TimeoutExpired):
                daemon.terminate()
                try:
                    daemon.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    daemon.kill()
                    daemon.wait()
                result["errors"].append("daemon required forced termination")
                result["success"] = False
        # The CLI can race a daemon failure and auto-start a replacement. The
        # private socket is still ours, but never accept that replacement as data.
        deadline = time.monotonic() + 10
        while health(socket_path) and time.monotonic() < deadline:
            time.sleep(.05)
        if health(socket_path):
            result["errors"].append("daemon still responds on the isolated socket after stop")
            result["success"] = False
        if daemon:
            result["daemonExitCode"] = daemon.poll()
            events.emit("daemon_exit", pid=daemon.pid, returncode=daemon.returncode)
        # Observe resources after jman exits, before stopping this run's Gradle daemons.
        if sampler:
            phase("after-stop")
            time.sleep(args.steady_seconds)
        phase("gradle-cleanup")
        if projects:
            wrapper = projects[0] / "gradlew"
            gradle_commands = [str(wrapper)] if wrapper.is_file() else []
            configured = env.get("JMAN_GRADLE") or shutil.which("gradle", path=env.get("PATH"))
            if configured and configured not in gradle_commands:
                gradle_commands.append(configured)
            cleanup_results = []
            for gradle in gradle_commands:
                try:
                    cleanup = command([gradle, "--stop"], projects[0], env, min(args.timeout, 60))
                    events.emit("gradle_stop", result=cleanup)
                    cleanup_results.append({"argv": cleanup["argv"], "returncode": cleanup["returncode"], "timeout": cleanup["timeout"]})
                    if cleanup["returncode"]:
                        result["success"] = False
                        result["errors"].append("isolated Gradle cleanup failed")
                except OSError as error:
                    result["success"] = False
                    result["errors"].append(f"isolated Gradle cleanup: {error}")
            result["gradleCleanup"] = cleanup_results
        if sampler:
            phase("after-cleanup")
            deadline = time.monotonic() + 5
            remaining = sampler.tracked_alive()
            while remaining and time.monotonic() < deadline:
                time.sleep(.1)
                remaining = sampler.tracked_alive()
            result["residualProcesses"] = remaining
            if remaining:
                result["success"] = False
                result["errors"].append("observed processes survived normal cleanup; identity-checked fallback required")
                for sig in (signal.SIGTERM, signal.SIGKILL):
                    for identity in remaining:
                        try:
                            signal_observed(sampler.reader, identity, sig)
                        except OSError as error:
                            events.emit("cleanup_error", pid=identity["pid"], error=str(error))
                    deadline = time.monotonic() + 2
                    while remaining and time.monotonic() < deadline:
                        time.sleep(.1)
                        remaining = sampler.tracked_alive()
                    if not remaining:
                        break
                result["residualProcessesAfterFallback"] = remaining
            result["resources"] = sampler.stop()
        if daemon_log:
            daemon_log.close()
        events.close()
        shutil.rmtree(socket_dir)
        dump(folder / "result.json", result)
    return result


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--repository", required=True, help="existing local Git repository (never modified)")
    p.add_argument("--ref", required=True, help="revision resolved and recorded as an immutable commit")
    p.add_argument("--project", default=".", help="Gradle build path relative to repository root")
    p.add_argument("--output", required=True, help="new directory for worktrees, caches, raw events and summaries")
    p.add_argument("--sessions", default="1,3,5")
    p.add_argument("--repetitions", type=int, default=3)
    p.add_argument("--jman", default="jman")
    p.add_argument("--timeout", type=float, default=300)
    p.add_argument("--startup", choices=["concurrent", "sequential"], default="concurrent")
    p.add_argument("--queries", help="JSON manifest of query argv arrays and optional source edits")
    p.add_argument("--query-rounds", type=int, default=3)
    p.add_argument("--refresh-rounds", type=int, default=3)
    p.add_argument("--steady-seconds", type=float, default=3)
    p.add_argument("--idle-seconds", type=float, default=0, help="optional idle observation; allow timeout plus sweep interval")
    p.add_argument("--idle-timeout", type=float, default=900, help="daemon idle timeout in seconds")
    p.add_argument("--lifecycle", action="store_true", help="add one worktree to test eviction and reopening")
    p.add_argument("--gradle-home-seed", "--dependency-cache", help="copy dependency/distribution caches from this home into each isolated run")
    p.add_argument("--sample-period", type=float, default=.2)
    p.add_argument("--jdtls-max-heap-mib", type=int, help="override .jman.json only in disposable worktrees")
    return p


def main(argv=None):
    p = parser()
    args = p.parse_args(argv)
    try:
        counts = [int(value) for value in args.sessions.split(",")]
        if not counts or any(n < 1 for n in counts) or len(set(counts)) != len(counts):
            raise ValueError("sessions must be distinct positive integers separated by commas")
        if args.repetitions < 1 or args.query_rounds < 1 or args.refresh_rounds < 0 or args.timeout <= 0:
            raise ValueError("repetitions/query-rounds/timeout must be positive; refresh-rounds nonnegative")
        if not all(math.isfinite(value) for value in (args.timeout, args.sample_period, args.steady_seconds,
                                                     args.idle_seconds, args.idle_timeout)):
            raise ValueError("durations and sampling period must be finite")
        if args.sample_period <= 0 or args.steady_seconds < 0 or args.idle_seconds < 0 or args.idle_timeout < 1:
            raise ValueError("sample period must be positive, observation times nonnegative, idle timeout >=1s")
        if args.jdtls_max_heap_mib is not None and args.jdtls_max_heap_mib < 128:
            raise ValueError("JDTLS max heap must be at least 128 MiB")
        relative_path(args.project)
        manifest = load_manifest(args.queries)
        executable = shutil.which(args.jman)
        if not executable:
            raise ValueError(f"jman executable not found: {args.jman}")
        args.jman = str(Path(executable).resolve())
        source = Path(args.repository).resolve()
        if not source.is_dir():
            raise ValueError("repository must be an existing local Git repository")
        env = dict(os.environ)
        commit = checked(["git", "rev-parse", "--verify", args.ref + "^{commit}"], source, env, args.timeout)
        root = Path(args.output).resolve()
        root.mkdir(parents=True, exist_ok=False)
        repository = root / "repository"
        checked(["git", "clone", "--no-hardlinks", "--no-checkout", str(source), str(repository)], root, env, args.timeout)
        # Source commits outside the clone's default refs are fetched explicitly.
        checked(["git", "fetch", "--no-tags", str(source), commit], repository, env, args.timeout)
        metadata = {"schemaVersion": 1, "repository": str(source), "requestedRef": args.ref,
                    "commit": commit, "arguments": vars(args), "queryManifest": manifest,
                    "jmanVersion": checked([args.jman, "--version"], root, env, 10),
                    "jmanExecutableSHA256": hashlib.sha256(Path(args.jman).read_bytes()).hexdigest(),
                    "gitVersion": checked(["git", "--version"], root, env, 10),
                    "javaVersion": command(["java", "-version"], root, env, 10) if shutil.which("java") else None,
                    "startedAtEpochSeconds": time.time(), "pythonVersion": sys.version,
                    "runtimeEnvironment": {name: env.get(name) for name in ("JMAN_JAVA_HOME", "JAVA_HOME", "JMAN_JDTLS_HOME", "JMAN_JDTLS", "JMAN_EXTENSION")}}
        dump(root / "metadata.json", metadata)
        runs = []
        for repetition in range(1, args.repetitions + 1):
            for count in counts:
                folder = root / f"{repetition:03d}-{count}-sessions"
                result = run_trial(args, folder, repository, commit, count, repetition, manifest)
                runs.append(result)
                print(f"{folder.name}: {'ok' if result['success'] else 'FAILED'}", flush=True)
                summary = {"schemaVersion": 1, "commit": commit, "complete": len(runs) == len(counts) * args.repetitions,
                           "success": all(r["success"] for r in runs), "runs": runs}
                dump(root / "summary.json", summary)
        return 0 if all(r["success"] for r in runs) else 1
    except (OSError, ValueError, RuntimeError) as error:
        print(f"resource benchmark: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
