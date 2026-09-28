import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "bench"))
from resources import ProcessReader, ResourceSampler, gradle_daemons

GRADLE = "java org.gradle.launcher.daemon.bootstrap.GradleDaemon 8.14.4"


def process(pid, parent=1, rss=100, started="first", command="jdtls", pss=50, cpu=1):
    return {"pid": pid, "ppid": parent, "started": started,
            "startEpoch": time.time() - 10, "rssBytes": rss, "pssBytes": pss,
            "cpuSeconds": cpu, "command": command}


class Reader:
    backend = "linux-proc"
    reason = None

    def __init__(self, table):
        self.table = table
        self.host = {"availableBytes": 5000, "swapUsedBytes": 0}

    def snapshot(self):
        return self.table, self.host

    def pss(self, process):
        return process["pssBytes"]


class ResourceTests(unittest.TestCase):
    def test_detached_gradle_is_counted_once_with_descendants(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            log = home / "daemon/8.14.4/daemon-30.out.log"
            log.parent.mkdir(parents=True)
            log.write_text("Gradle daemon")
            reader = Reader({10: process(10, rss=10, command="jman daemon"),
                             20: process(20, 10, rss=20),
                             30: process(30, 20, rss=30, command=GRADLE),
                             40: process(40, 30, rss=40, command="GradleWorkerMain"),
                             99: process(99, rss=9000)})
            sampler = ResourceSampler(root_pid=10, gradle_home=home, reader=reader, start=False)
            first = sampler.sample()
            self.assertEqual(first["rssBytes"], 100)
            reader.table[30]["ppid"] = 1
            detached = sampler.sample()
            self.assertEqual(detached["rssBytes"], 100)
            self.assertEqual([p["pid"] for p in detached["processes"]], [10, 20, 30, 40])
            summary = sampler.stop()
            self.assertEqual(len(summary["processes"]), 4)
            self.assertEqual(summary["peakRSSBytes"], 100)

    def test_gradle_daemon_discovered_without_live_parent(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            log = home / "daemon/8.14.4/daemon-30.out.log"
            log.parent.mkdir(parents=True)
            log.write_text("Gradle daemon")
            table = {30: process(30, command=GRADLE), 40: process(40, command=GRADLE)}
            self.assertEqual(gradle_daemons(home, table), {30})
            os.utime(log, (10, 10))
            table[30]["startEpoch"] = 20
            self.assertEqual(gradle_daemons(home, table), set())
            log.touch()
            table[30]["command"] = "java unrelated"
            self.assertEqual(gradle_daemons(home, table), set())

    def test_reparented_child_remains_but_reused_pid_is_not_adopted(self):
        reader = Reader({10: process(10, command="jman daemon"), 20: process(20, 10)})
        sampler = ResourceSampler(root_pid=10, reader=reader, start=False)
        sampler.sample()
        reader.table = {20: process(20, parent=1)}
        self.assertEqual([p["pid"] for p in sampler.sample()["processes"]], [20])
        reader.table = {10: process(10, started="replacement"), 20: process(20, started="replacement")}
        self.assertEqual(sampler.sample()["processes"], [])
        self.assertEqual(sampler.stop()["lastRSSBytes"], 0)

    def test_pid_reuse_by_new_child_gets_separate_cpu_identity(self):
        reader = Reader({10: process(10, command="jman daemon"), 20: process(20, 10, cpu=10)})
        sampler = ResourceSampler(root_pid=10, reader=reader, start=False)
        sampler.sample()
        reader.table[20]["cpuSeconds"] = 15
        sampler.sample()
        reader.table[20] = process(20, 10, started="second", cpu=2)
        sampler.sample()
        reader.table[20]["cpuSeconds"] = 5
        summary = sampler.stop()
        self.assertEqual(summary["observedCPUSeconds"], 8)
        self.assertEqual(len([p for p in summary["processes"] if p["pid"] == 20]), 2)

    def test_peak_is_simultaneous_sum_and_phases_are_separate(self):
        reader = Reader({10: process(10, rss=100), 20: process(20, 10, rss=100)})
        sampler = ResourceSampler(root_pid=10, reader=reader, start=False)
        sampler.phase("import")
        reader.table[10]["rssBytes"] = 10
        reader.table[20]["rssBytes"] = 200
        sampler.sample()
        sampler.phase("idle")
        reader.table[20]["rssBytes"] = 10
        summary = sampler.stop()
        self.assertEqual(summary["peakRSSBytes"], 210)
        self.assertEqual(summary["phases"]["import"]["peakRSSBytes"], 210)
        self.assertEqual(summary["phases"]["idle"]["peakRSSBytes"], 20)
        self.assertEqual(sum(p["peakRSSBytes"] for p in summary["processes"]), 300)

    def test_partial_pss_is_unavailable_not_a_partial_sum(self):
        reader = Reader({10: process(10), 20: process(20, 10, pss=None)})
        sampler = ResourceSampler(root_pid=10, reader=reader, start=False)
        sample = sampler.sample()
        self.assertEqual(sample["rssBytes"], 200)
        self.assertIsNone(sample["pssBytes"])
        summary = sampler.stop()
        self.assertIsNone(summary["peakPSSBytes"])
        self.assertTrue(any("smaps_rollup" in reason for reason in summary["limitations"]))

    def test_unsupported_platform_is_null_not_zero(self):
        sampler = ResourceSampler(root_pid=10, reader=ProcessReader(platform="unsupported-test"), start=False)
        summary = sampler.stop()
        self.assertEqual(summary["availableSamples"], 0)
        self.assertIsNone(summary["peakRSSBytes"])
        self.assertIsNone(summary["observedCPUSeconds"])
        self.assertTrue(any("unsupported-test" in reason for reason in summary["limitations"]))

    def test_missing_proc_is_null_not_zero(self):
        with tempfile.TemporaryDirectory() as tmp:
            sampler = ResourceSampler(reader=ProcessReader(platform="linux", proc=Path(tmp)), start=False)
            self.assertIsNone(sampler.stop()["peakRSSBytes"])

    def test_jsonl_and_host_extrema_preserve_samples(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "samples.jsonl"
            reader = Reader({10: process(10)})
            sampler = ResourceSampler(root_pid=10, output=output, reader=reader, start=False)
            sampler.sample()
            sampler.phase("idle")
            reader.host = {"availableBytes": 1000, "swapUsedBytes": 200}
            summary = sampler.stop()
            self.assertEqual(summary["host"]["minAvailableBytes"], 1000)
            self.assertEqual(summary["host"]["peakSwapUsedBytes"], 200)
            samples = [json.loads(line) for line in output.read_text().splitlines()]
            self.assertEqual(samples[-1]["phase"], "idle")
            self.assertEqual(len(samples), summary["samples"])
            self.assertIs(sampler.stop(), summary)

    def test_darwin_ps_rss_cpu_and_host_swap(self):
        reader = ProcessReader(platform="darwin")
        ps = SimpleNamespace(stdout="123 1 2048 Mon Sep 28 10:15:30 2026 01:02.50 /jdk/bin/java -jar /jdtls/org.eclipse.equinox.launcher.jar\n456 123 4096 Mon Sep 28 10:15:31 2026 1-02:03:04 java org.gradle.launcher.daemon.bootstrap.GradleDaemon\n")
        host = SimpleNamespace(stdout="17179869184\ntotal = 1024.00M used = 238.50M free = 785.50M (encrypted)\n")
        with patch("resources.subprocess.run", side_effect=[ps, host]):
            table, memory = reader.snapshot()
        self.assertEqual(reader.backend, "darwin-ps")
        self.assertEqual(table[123]["rssBytes"], 2048 * 1024)
        self.assertEqual(table[123]["cpuSeconds"], 62.5)
        self.assertEqual(table[456]["cpuSeconds"], 93784)
        self.assertEqual(table[456]["ppid"], 123)
        self.assertIsNone(reader.pss(table[123]))
        self.assertEqual(memory["totalBytes"], 17179869184)
        self.assertEqual(memory["swapUsedBytes"], int(238.5 * 1024**2))
        self.assertIsNone(memory["availableBytes"])
        self.assertIsNone(memory["memoryPressure"])

    def test_darwin_command_failure_or_empty_output_is_unavailable(self):
        reader = ProcessReader(platform="darwin")
        with patch("resources.subprocess.run", side_effect=FileNotFoundError("ps missing")):
            table, host = reader.snapshot()
        self.assertIsNone(table)
        self.assertIn("ps missing", reader.reason)
        with patch("resources.subprocess.run", return_value=SimpleNamespace(stdout="malformed row\n")):
            table, host = reader.snapshot()
        self.assertIsNone(table)

    def test_darwin_sampler_reports_rss_without_pss(self):
        reader = Reader({10: process(10, rss=1234)})
        reader.backend = "darwin-ps"
        reader.table[10]["pssBytes"] = None
        sampler = ResourceSampler(root_pid=10, reader=reader, start=False)
        summary = sampler.stop()
        self.assertEqual(summary["peakRSSBytes"], 1234)
        self.assertIsNone(summary["peakPSSBytes"])


if __name__ == "__main__":
    unittest.main()
