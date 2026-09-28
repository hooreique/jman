"""Sample isolated jman and Gradle resources; RSS is not unique physical memory."""
import datetime
import json
import math
import os
from pathlib import Path
import re
import socket as sockets
import subprocess
import sys
import threading
import time


def daemon_pid(socket):
    if socket is None:
        return None
    try:
        with sockets.socket(sockets.AF_UNIX) as client:
            client.settimeout(.2)
            client.connect(str(socket))
            client.sendall(b"GET /health HTTP/1.0\r\nHost: jman\r\n\r\n")
            chunks = []
            while data := client.recv(4096):
                chunks.append(data)
        return int(json.loads(b"".join(chunks).split(b"\r\n\r\n", 1)[1])["pid"])
    except (OSError, ValueError, KeyError, IndexError, TypeError):
        return None


def kind(command, root=False):
    if root:
        return "jman"
    if "org.gradle.launcher.daemon.bootstrap.GradleDaemon" in command:
        return "gradle"
    if "org.eclipse.equinox.launcher" in command or "org.eclipse.jdt.ls" in command or "jdtls" in command:
        return "jdtls"
    if "GradleWorkerMain" in command:
        return "gradle-worker"
    if "org.gradle.launcher.GradleMain" in command or "org.gradle.wrapper.GradleWrapperMain" in command:
        return "gradle-client"
    return "other"


class ProcessReader:
    def __init__(self, platform=None, proc=Path("/proc")):
        self.platform = platform or sys.platform
        self.proc = Path(proc)
        self.backend = "linux-proc" if self.platform.startswith("linux") else "darwin-ps" if self.platform == "darwin" else "unsupported"
        self.reason = None

    def snapshot(self):
        if self.backend == "unsupported":
            self.reason = f"Process sampling is unavailable on {self.platform}."
            return None, {}
        if self.backend == "darwin-ps":
            try:
                return self.darwin(), self.darwin_host()
            except (OSError, ValueError, subprocess.SubprocessError) as error:
                self.reason = f"macOS process sampling unavailable: {error}"
                return None, {}
        try:
            stat = (self.proc / "stat").read_text()
            boot = int(next(line.split()[1] for line in stat.splitlines() if line.startswith("btime ")))
            ticks = os.sysconf("SC_CLK_TCK")
            page = os.sysconf("SC_PAGE_SIZE")
            processes = {}
            for entry in self.proc.iterdir():
                if not entry.name.isdigit():
                    continue
                try:
                    fields = (entry / "stat").read_text().rsplit(")", 1)[1].split()
                    if fields[0] == "Z":
                        continue
                    processes[int(entry.name)] = {
                        "pid": int(entry.name), "ppid": int(fields[1]), "started": fields[19],
                        "startEpoch": boot + int(fields[19]) / ticks,
                        "rssBytes": max(0, int(fields[21])) * page,
                        "cpuSeconds": (int(fields[11]) + int(fields[12])) / ticks,
                        "command": (entry / "cmdline").read_bytes().replace(b"\0", b" ").decode(errors="replace"),
                    }
                except (OSError, ValueError, IndexError):
                    continue
            if not processes:
                raise ValueError("proc contains no readable processes")
            return processes, self.host()
        except (OSError, ValueError, StopIteration) as error:
            self.reason = f"Linux process sampling unavailable: {error}"
            return None, {}

    def darwin(self):
        result = subprocess.run(["/bin/ps", "-axo", "pid=,ppid=,rss=,lstart=,time=,command="],
                                capture_output=True, text=True, check=True, timeout=2,
                                env=dict(os.environ, LC_ALL="C"))
        processes = {}
        for line in result.stdout.splitlines():
            fields = line.split(None, 9)
            if len(fields) != 10:
                continue
            try:
                started = " ".join(fields[3:8])
                days, clock = fields[8].split("-", 1) if "-" in fields[8] else ("0", fields[8])
                cpu = 0.0
                for part in clock.split(":"):
                    cpu = cpu * 60 + float(part)
                processes[int(fields[0])] = {
                    "pid": int(fields[0]), "ppid": int(fields[1]), "started": started,
                    "startEpoch": datetime.datetime.strptime(started, "%a %b %d %H:%M:%S %Y").timestamp(),
                    "rssBytes": int(fields[2]) * 1024, "cpuSeconds": int(days) * 86400 + cpu,
                    "command": fields[9],
                }
            except ValueError:
                continue
        if not processes:
            raise ValueError("ps returned no readable processes")
        return processes

    def darwin_host(self):
        host = {"availableBytes": None, "memoryPressure": None}
        try:
            result = subprocess.run(["/usr/sbin/sysctl", "-n", "hw.memsize", "vm.swapusage"],
                                    capture_output=True, text=True, check=True, timeout=2,
                                    env=dict(os.environ, LC_ALL="C"))
            lines = result.stdout.splitlines()
            host["totalBytes"] = int(lines[0])
            match = re.search(r"used\s*=\s*([\d.]+)([KMG])", lines[1])
            if match:
                host["swapUsedBytes"] = int(float(match[1]) * 1024 ** ("KMG".index(match[2]) + 1))
        except (OSError, ValueError, IndexError, subprocess.SubprocessError):
            pass
        return host

    def host(self):
        host = {}
        try:
            info = {parts[0].rstrip(":"): int(parts[1]) * 1024
                    for line in (self.proc / "meminfo").read_text().splitlines()
                    if len(parts := line.split()) >= 2}
            host.update(totalBytes=info.get("MemTotal"), availableBytes=info.get("MemAvailable"),
                        swapUsedBytes=info["SwapTotal"] - info["SwapFree"])
        except (OSError, KeyError, ValueError):
            pass
        try:
            host["memoryPressure"] = {
                parts[0]: {key: float(value) for item in parts[1:] for key, value in [item.split("=")]}
                for line in (self.proc / "pressure/memory").read_text().splitlines()
                if (parts := line.split())}
        except (OSError, ValueError):
            host["memoryPressure"] = None
        try:
            stats = dict(line.split() for line in (self.proc / "vmstat").read_text().splitlines())
            host["swapInPages"] = int(stats["pswpin"])
            host["swapOutPages"] = int(stats["pswpout"])
        except (OSError, KeyError, ValueError):
            pass
        return host

    def pss(self, process):
        if self.backend != "linux-proc":
            return None
        try:
            directory = self.proc / str(process["pid"])
            value = next(int(line.split()[1]) * 1024 for line in (directory / "smaps_rollup").read_text().splitlines()
                         if line.startswith("Pss:"))
            started = (directory / "stat").read_text().rsplit(")", 1)[1].split()[19]
            return value if started == process["started"] else None
        except (OSError, ValueError, IndexError, StopIteration):
            return None


def gradle_daemons(home, processes):
    """Requires an experiment-specific Gradle home. Reject stale PID logs."""
    if home is None:
        return set()
    found = set()
    for log in (Path(home) / "daemon").glob("*/daemon-*.out.log"):
        match = re.fullmatch(r"daemon-(\d+)\.out\.log", log.name)
        if not match:
            continue
        pid = int(match[1])
        process = processes.get(pid)
        if not process or kind(process["command"]) != "gradle":
            continue
        try:
            if log.stat().st_mtime >= process["startEpoch"]:
                found.add(pid)
        except OSError:
            pass
    return found


def new_stats():
    return {"samples": 0, "availableSamples": 0, "pssSamples": 0, "peakRSSBytes": None,
            "peakPSSBytes": None, "lastRSSBytes": None, "lastPSSBytes": None, "_rssSum": 0}


def update(stats, record):
    stats["samples"] += 1
    for metric in ("RSS", "PSS"):
        value = record[metric.lower() + "Bytes"]
        stats[f"last{metric}Bytes"] = value
        if value is not None:
            key = f"peak{metric}Bytes"
            stats[key] = max(stats[key] or 0, value)
    if record["rssBytes"] is not None:
        stats["availableSamples"] += 1
        stats["_rssSum"] += record["rssBytes"]
    if record["pssBytes"] is not None:
        stats["pssSamples"] += 1


def finish_stats(stats):
    return {**{key: value for key, value in stats.items() if not key.startswith("_")},
            "meanRSSBytes": stats["_rssSum"] / stats["availableSamples"] if stats["availableSamples"] else None}


class ResourceSampler:
    """Auto-starting sampler. phase(name), sample(), stop() return JSON-safe data."""
    def __init__(self, socket=None, gradle_home=None, period=.2, output=None, root_pid=None, *, reader=None, start=True):
        if not math.isfinite(period) or period <= 0:
            raise ValueError("sampling period must be positive")
        self.socket, self.gradle_home, self.root_pid = socket, gradle_home, root_pid
        self.root_identity = None
        self.period = period
        self.reader = reader or ProcessReader()
        self.started = time.monotonic()
        self.started_wall = time.time()
        self.current_phase = "initial"
        self.phase_started = self.started
        self.phases = {"initial": {**new_stats(), "seconds": 0.0}}
        self.stats = new_stats()
        self.processes = {}
        self.tracked = set()
        self.host = {"minAvailableBytes": None, "peakSwapUsedBytes": None, "first": {}, "last": {}}
        self.errors = set()
        self.done = threading.Event()
        self.lock = threading.Lock()
        self.file = Path(output).open("x") if output else None
        self.result = None
        self.thread = None
        if start:
            self.sample()
            self.thread = threading.Thread(target=self.collect, daemon=True)
            self.thread.start()

    def collect(self):
        while not self.done.wait(self.period):
            self.sample()

    def sample(self):
        with self.lock:
            if self.result is not None:
                raise RuntimeError("sampler is stopped")
            return self._sample()

    def _sample(self):
        now = time.monotonic()
        table, host = self.reader.snapshot()
        rows = []
        if table is None:
            self.errors.add(self.reader.reason or "Process table unavailable.")
        else:
            root = self.root_pid if self.root_pid is not None else daemon_pid(self.socket)
            selected = {pid for pid, value in table.items() if (pid, value["started"]) in self.tracked}
            if root in table:
                identity = (root, table[root]["started"])
                if self.root_pid is None or self.root_identity is None or self.root_identity == identity:
                    self.root_identity = identity
                    selected.add(root)
            selected |= gradle_daemons(self.gradle_home, table)
            while True:
                descendants = {pid for pid, value in table.items() if value["ppid"] in selected}
                if descendants <= selected:
                    break
                selected |= descendants
            for pid in sorted(selected):
                value = table[pid]
                identity = (pid, value["started"])
                self.tracked.add(identity)
                row = {key: value[key] for key in ("pid", "ppid", "started", "rssBytes", "cpuSeconds")}
                row.update(kind=kind(value["command"], identity == self.root_identity), pssBytes=self.reader.pss(value))
                previous = self.processes.get(identity)
                if previous is None:
                    previous = self.processes[identity] = {
                        "pid": pid, "started": value["started"], "kind": row["kind"],
                        "firstSeenSeconds": now - self.started,
                        "peakRSSBytes": 0, "peakPSSBytes": None,
                        "_cpuBaseline": value["cpuSeconds"] if value["startEpoch"] < self.started_wall else 0,
                    }
                previous.update(lastSeenSeconds=now - self.started,
                                cpuSeconds=max(0, value["cpuSeconds"] - previous["_cpuBaseline"]),
                                peakRSSBytes=max(previous["peakRSSBytes"], row["rssBytes"]))
                if previous["kind"] == "other" and row["kind"] != "other":
                    previous["kind"] = row["kind"]
                if row["pssBytes"] is not None:
                    previous["peakPSSBytes"] = max(previous["peakPSSBytes"] or 0, row["pssBytes"])
                row["kind"] = previous["kind"]
                rows.append(row)
        rss = sum(row["rssBytes"] for row in rows) if table is not None else None
        pss = sum(row["pssBytes"] for row in rows) if table is not None and self.reader.backend == "linux-proc" and all(row["pssBytes"] is not None for row in rows) else None
        record = {"type": "resource_sample", "elapsed": now - self.started, "phase": self.current_phase,
                  "rssBytes": rss, "pssBytes": pss, "processes": rows, "host": host}
        update(self.stats, record)
        update(self.phases[self.current_phase], record)
        if host:
            if not self.host["first"]:
                self.host["first"] = host
            self.host["last"] = host
            available = host.get("availableBytes")
            swap = host.get("swapUsedBytes")
            if available is not None:
                self.host["minAvailableBytes"] = min(self.host["minAvailableBytes"], available) if self.host["minAvailableBytes"] is not None else available
            if swap is not None:
                self.host["peakSwapUsedBytes"] = max(self.host["peakSwapUsedBytes"] or 0, swap)
        if self.file:
            self.file.write(json.dumps(record) + "\n")
            self.file.flush()
        return record

    def phase(self, name):
        with self.lock:
            if self.result is not None:
                raise RuntimeError("sampler is stopped")
            self._sample()
            now = time.monotonic()
            self.phases[self.current_phase]["seconds"] += now - self.phase_started
            self.current_phase = name
            self.phase_started = now
            self.phases.setdefault(name, {**new_stats(), "seconds": 0.0})

    def tracked_alive(self):
        """Current identities only; caller must recheck identity before signalling."""
        return self.sample()["processes"]

    def stop(self):
        self.done.set()
        if self.thread:
            self.thread.join()
        with self.lock:
            if self.result is not None:
                return self.result
            self._sample()
            self.phases[self.current_phase]["seconds"] += time.monotonic() - self.phase_started
            limitations = ["Sampled RSS sums shared resident pages more than once; it is not unique physical memory.",
                           "Short-lived processes between samples may be missed; process CPU is observed, not complete accounting.",
                           "Gradle attribution requires a dedicated GRADLE_USER_HOME; shared daemons are counted once, not assigned to worktrees."]
            if self.reader.backend == "unsupported":
                limitations.append("RSS/PSS and host memory metrics are unavailable on this backend.")
            elif self.reader.backend == "darwin-ps":
                limitations.append("macOS PSS, host available memory and memory pressure are unavailable; ps start identities have one-second precision.")
            elif self.stats["pssSamples"] != self.stats["availableSamples"]:
                limitations.append("PSS is unavailable in samples where any selected process has no readable smaps_rollup.")
            self.result = {"scope": "jman, observed descendants and daemons in the dedicated Gradle user home",
                           "backend": self.reader.backend, "periodSeconds": self.period, **finish_stats(self.stats),
                           "observedCPUSeconds": sum(value["cpuSeconds"] for value in self.processes.values()) if self.stats["availableSamples"] else None,
                           "phases": {name: finish_stats(value) for name, value in self.phases.items()},
                           "processes": [{key: item for key, item in value.items() if not key.startswith("_")} for value in self.processes.values()],
                           "host": self.host, "limitations": limitations + sorted(self.errors)}
            if self.file:
                self.file.close()
            return self.result
