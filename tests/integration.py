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
from fixture import SOURCE, evaluate, materialize, writable_tree


def main():
    binary = str(Path(sys.argv[1]).resolve()) if len(sys.argv) > 1 else "jman"
    with tempfile.TemporaryDirectory(prefix="jman-integration-") as tmp:
        root = Path(tmp)
        fixture = materialize(root / "fixture")
        project = Path(fixture["project"])
        config_path = project / ".jman.json"
        project_config = json.loads(config_path.read_text())
        project_config["jdtlsMaxHeapMiB"] = 1024
        config_path.write_text(json.dumps(project_config))
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

            # Actual Gradle projects named like excluded output directories must
            # observe selection changes between two already-existing JARs.
            import zipfile
            artifact_dir = root / "reports-artifacts"
            artifact_dir.mkdir()
            jars = []
            for name, method in (("a", "toUpperCase"), ("b", "toLowerCase")):
                api = artifact_dir / "Api.java"
                classes = artifact_dir / name
                classes.mkdir()
                api.write_text(f'package dependency; public class Api {{ public static String apply(String value) {{ return value.{method}(); }} }}\n')
                subprocess.run(["javac", "-d", str(classes), str(api)], check=True, capture_output=True)
                jar_path = artifact_dir / f"{name}.jar"
                with zipfile.ZipFile(jar_path, "w") as archive:
                    archive.write(classes / "dependency/Api.class", "dependency/Api.class")
                jars.append(jar_path)
            fixture_project = project
            for structure in ("root", "subproject"):
                workspace = root / f"reports-{structure}"
                module = workspace / "reports"
                module.mkdir(parents=True)
                project = module if structure == "root" else workspace
                (project / "settings.gradle").write_text("rootProject.name = 'reports-regression'\n" + ("include ':reports'\n" if structure == "subproject" else ""))
                (project / ".jman.json").write_text('{"offline":true,"jdtlsMaxHeapMiB":768}')
                build_file = module / "build.gradle"
                build_text = "plugins { id 'java' }\ndependencies { implementation files(%s) }\n"
                build_file.write_text(build_text % json.dumps(str(jars[0])))
                caller = module / "src/main/java/Caller.java"
                caller.parent.mkdir(parents=True)
                caller.write_text('class Caller { String call() { return dependency.Api.apply("value"); } }\n')
                caller_location = str(caller.relative_to(project)) + ":1"
                selected_a = query("definition", caller_location, "--symbol", "apply")
                assert "toUpperCase" in selected_a["results"][0]["snippet"], selected_a
                for _ in range(3):
                    time.sleep(.2)
                    query("prepare", expected=(0, 3))
                build_file.write_text(build_text % json.dumps(str(jars[1])))
                time.sleep(.2)
                selected_b = query("definition", caller_location, "--symbol", "apply")
                assert selected_b["snapshot"] != selected_a["snapshot"], selected_b
                assert selected_b["results"][0]["binaryDigest"] == hashlib.sha256(jars[1].read_bytes()).hexdigest(), selected_b
                assert "toLowerCase" in selected_b["results"][0]["snippet"] and "toUpperCase" not in selected_b["results"][0]["snippet"], selected_b
                query("refresh", expected=(0, 3))
                refreshed = query("definition", caller_location, "--symbol", "apply")
                assert refreshed["snapshot"] == selected_b["snapshot"] and refreshed["results"] == selected_b["results"], refreshed
                print(f"PASS reports Gradle {structure} JAR A -> B selection agrees with refresh", flush=True)
            project = fixture_project
            if os.environ.get("JMAN_INTEGRATION_CASE") == "reports":
                return

            before = evaluate(project, fixture["binary"])
            assert not before["success"], "fixture is not initially broken"
            prepared = query("prepare", expected=(0, 3))
            assert not prepared.get("warnings"), prepared
            initial_status = query("status")
            assert initial_status["context"]["maxSessions"] == 2, initial_status
            assert initial_status["context"]["idleTimeout"] == "15m0s", initial_status
            initial_session = initial_status["results"][0]["detail"]
            assert initial_session["pid"] > 0 and initial_session["jdtlsMaxHeapMiB"] == 1024, initial_status
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

            build = project / "build.gradle"
            original_build = build.read_text()
            project_config["jdtlsMaxHeapMiB"] = 768
            config_path.write_text(json.dumps(project_config))
            build.write_text(original_build.replace("implementation 'com.acme:shared-text:2.4.1'", "implementation 'com.acme:shared-text:1.0.0'; constraints { implementation 'com.acme:shared-text:2.4.1' }"))
            conflict = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            assert conflict["results"][0]["artifact"] == "com.acme:shared-text:2.4.1", conflict
            changed_session = query("status")["results"][0]["detail"]
            assert changed_session["pid"] != initial_session["pid"], changed_session
            assert changed_session["jdtlsMaxHeapMiB"] == 768, changed_session
            print("PASS configured heap and effective session status after restart", flush=True)
            print("PASS selected dependency version differs from declared version", flush=True)
            build.write_text(original_build)

            test_caller = project / "app/src/test/java/example/TestCaller.java"
            test_caller.parent.mkdir(parents=True)
            test_caller.write_text('package example; class TestCaller { String value(String s) { return com.acme.text.TextUtil.normalize(s); } }\n')
            query("refresh", expected=(0, 3))
            test_binding = query("definition", "app/src/test/java/example/TestCaller.java:1", "--symbol", "normalize", expected=(0, 3))
            assert test_binding["context"]["sourceSet"] == "test", test_binding
            assert test_binding["context"]["configuration"] == "testCompileClasspath", test_binding
            assert test_binding["results"][0]["artifact"] == fixture["artifact"], test_binding
            model = query("prepare", expected=(0, 3))["results"][0]["detail"]["model"]
            main_context = next(c for c in model["contexts"] if c["project"] == ":app" and c["sourceSet"] == "main")
            test_context = next(c for c in model["contexts"] if c["project"] == ":app" and c["sourceSet"] == "test")
            assert set(main_context["outputs"]) & set(test_context["compileClasspath"]), model
            print("PASS test source-set resolution context", flush=True)

            broken_test = test_caller.with_name("BrokenTest.java")
            broken_test.write_text('package example; class BrokenTest { MissingType value; }\n')
            scoped_main = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            main_diagnostics = scoped_main["coverage"]["diagnostics"]
            assert not main_diagnostics["critical"] and main_diagnostics["advisory"], scoped_main
            scoped_test = query("definition", "app/src/test/java/example/TestCaller.java:1", "--symbol", "normalize", expected=(3,))
            assert scoped_test["coverage"]["diagnostics"]["critical"], scoped_test
            broken_test.unlink()
            query("prepare", expected=(0, 3))
            print("PASS caller test errors are critical but advisory for main navigation", flush=True)

            # A true composite build substitutes the external module with another repository.
            settings = project / "settings.gradle"
            original_settings = settings.read_text()
            settings.write_text(original_settings + "\nincludeBuild('../internal-text')\n")
            composite = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            assert composite["results"] and "internal-text" in composite["results"][0]["path"], composite
            assert composite["results"][0]["origin"] == "workspace-source", composite

            composite_pid = query("status")["results"][0]["detail"]["pid"]
            included_source = Path(composite["results"][0]["path"])
            included_source.write_text(included_source.read_text() + "\n// composite incremental regression\n")
            incremental = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            assert incremental["snapshot"] != composite["snapshot"], incremental
            assert query("status")["results"][0]["detail"]["pid"] == composite_pid
            # An unchanged build-file touch forces fingerprint/full-scan validation
            # without changing the build content or intentionally restarting JDTLS.
            os.utime(build, None)
            full_snapshot = query("prepare", "--timings", expected=(0, 3))
            assert full_snapshot["snapshot"] == incremental["snapshot"], full_snapshot
            assert full_snapshot["timings"]["sync"] == 0, full_snapshot
            assert query("status")["results"][0]["detail"]["pid"] == composite_pid
            settings.write_text(original_settings)
            query("refresh", expected=(0, 3))
            print("PASS multi-repository composite build follows actual substitution", flush=True)

            sources = Path(fixture["binary"]).with_name("shared-text-2.4.1-sources.jar")
            sources.unlink()
            query("refresh", expected=(0, 3))
            fallback = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            assert fallback["results"][0]["origin"] in ("decompiled", "binary-only"), fallback
            print("PASS missing sources are not represented as original source", flush=True)

            original_project = project
            other = project.parent / "commerce-other"
            subprocess.run(["git", "worktree", "add", "--detach", str(other), "HEAD"], cwd=project, env=env, check=True, capture_output=True)
            project = other
            isolated = query("definition", location, "--symbol", "normalize", expected=(0, 3))
            assert isolated["context"]["root"] != fallback["context"]["root"], isolated
            assert isolated["snapshot"] != fallback["snapshot"], isolated
            active = query("status")
            roots = {result["name"] for result in active["results"]}
            assert str(other) in roots and str(original_project) in roots, active
            print("PASS independent Git worktree sessions", flush=True)

            # An external file dependency is not covered by workspace watchers.
            import zipfile
            project = root / "external-classpath"
            project.mkdir()
            (project / "settings.gradle").write_text("rootProject.name = 'external-classpath'\n")
            external = root / "external-artifact"
            external.mkdir()
            jar = external / "api.jar"
            api = external / "Api.java"
            classes = external / "classes"
            classes.mkdir()

            def replace_jar(method):
                api.write_text(f'package dependency; public class Api {{ public static String apply(String value) {{ return value.{method}(); }} }}\n')
                subprocess.run(["javac", "-d", str(classes), str(api)], check=True, capture_output=True)
                with zipfile.ZipFile(jar, "w") as archive:
                    archive.write(classes / "dependency/Api.class", "dependency/Api.class")

            replace_jar("toUpperCase")
            (project / "build.gradle").write_text("plugins { id 'java' }\nsourceSets.main.java.srcDirs = ['reports/coverage']\ndependencies { implementation files(" + json.dumps(str(jar)) + ") }\n")
            (project / ".jman.json").write_text('{"offline":true,"jdtlsMaxHeapMiB":768}')
            declared = project / "reports/coverage/Caller.java"
            declared.parent.mkdir(parents=True)
            declared.write_text('class Caller { String call() { return dependency.Api.apply("value"); } }\n')
            external_location = "reports/coverage/Caller.java:1"
            external_before = query("definition", external_location, "--symbol", "apply", expected=(0, 3))
            assert external_before["results"] and "toUpperCase" in external_before["results"][0]["snippet"], external_before
            # Let import activity settle before testing only the external rewrite.
            for _ in range(3):
                time.sleep(.2)
                query("prepare", expected=(0, 3))
            old_pid = next(v["detail"]["pid"] for v in query("status")["results"] if v["name"] == str(project))
            replace_jar("toLowerCase")
            external_after = query("definition", external_location, "--symbol", "apply", expected=(0, 3))
            assert external_after["snapshot"] != external_before["snapshot"], external_after
            assert external_after["results"][0]["binaryDigest"] == hashlib.sha256(jar.read_bytes()).hexdigest(), external_after
            assert "toLowerCase" in external_after["results"][0]["snippet"] and "toUpperCase" not in external_after["results"][0]["snippet"], external_after
            assert external_after["results"][0]["path"] != external_before["results"][0]["path"], external_after
            new_pid = next(v["detail"]["pid"] for v in query("status")["results"] if v["name"] == str(project))
            assert new_pid != old_pid
            query("refresh", expected=(0, 3))
            external_refreshed = query("definition", external_location, "--symbol", "apply", expected=(0, 3))
            assert external_refreshed["snapshot"] == external_after["snapshot"], external_refreshed
            assert external_refreshed["results"] == external_after["results"], external_refreshed
            declared.write_text(declared.read_text() + "// declared-source edit\n")
            time.sleep(.2)
            declared_edit = query("definition", external_location, "--symbol", "apply", expected=(0, 3))
            assert declared_edit["snapshot"] != external_refreshed["snapshot"], declared_edit
            query("refresh", expected=(0, 3))
            declared_refresh = query("definition", external_location, "--symbol", "apply", expected=(0, 3))
            assert declared_refresh["snapshot"] == declared_edit["snapshot"], declared_refresh
            assert declared_refresh["results"] == declared_edit["results"], declared_refresh
            print("PASS external JAR definition/cache invalidation and declared ignored sources agree with refresh", flush=True)

            if os.environ.get("JMAN_FIXTURE_DEPS"):
                import shutil
                project = root / "stack"
                shutil.copytree(SOURCE / "stack", project, ignore=shutil.ignore_patterns("build", "bin", ".gradle", ".project", ".classpath", ".factorypath", ".settings"))
                writable_tree(project)
                built = subprocess.run(["gradle", "--offline", "--console=plain", "verifyStack"], cwd=project, env=env, capture_output=True, text=True, timeout=120)
                assert built.returncode == 0, built.stdout + built.stderr
                print(built.stdout, flush=True)
                query("prepare", expected=(0, 3))
                getter = query("definition", "src/main/java/example/Navigation.java:5", "--symbol", "getName", expected=(0, 3))
                assert getter["results"] and "Person" in getter["results"][0]["name"], getter
                assert getter["results"][0]["origin"] == "generated-source", getter
                builder = query("definition", "src/main/java/example/Navigation.java:8", "--symbol", "builder", expected=(0, 3))
                assert builder["results"], builder
                mapping = query("definition", "src/main/java/example/Navigation.java:11", "--symbol", "toDto", expected=(0, 3))
                assert mapping["results"][0]["origin"] == "generated-source", mapping
                qtype = query("definition", "src/main/java/example/Navigation.java:14", "--symbol", "name", expected=(0, 3))
                assert qtype["results"][0]["origin"] == "generated-source", qtype
                impl = query("implementations", "src/main/java/example/PersonMapper.java:7", "--symbol", "toDto", expected=(0, 3))
                assert any("PersonMapperImpl" in r.get("path", "") for r in impl["results"]), impl
                print("PASS Lombok getter/builder, MapStruct implementation, QueryDSL generated fields", flush=True)

                # Small settled warm-path smoke; import and first requests are
                # excluded. Keep diagnostics/coverage rather than requiring a
                # globally error-free workspace.
                warm_location = "src/main/java/example/PersonMapper.java:7"
                baseline = {}
                for command in ("definition", "references", "implementations"):
                    baseline[command] = query(command, warm_location, "--symbol", "toDto", "--timings", expected=(0, 3))
                    assert baseline[command]["results"], baseline[command]
                assert any(r.get("path", "").endswith("PersonMapper.java") for r in baseline["definition"]["results"]), baseline
                assert any(r.get("path", "").endswith("Navigation.java") for r in baseline["references"]["results"]), baseline
                assert any("PersonMapperImpl" in r.get("path", "") for r in baseline["implementations"]["results"]), baseline
                settled = 0
                deadline = time.monotonic() + 30
                while settled < 3 and time.monotonic() < deadline:
                    time.sleep(.2)
                    prepared = query("prepare", expected=(0, 3))
                    service = prepared["results"][0]["detail"]["service"]
                    settled = settled + 1 if service["cacheHit"] and not service["dirty"] else 0
                assert settled == 3, prepared
                for command, first in baseline.items():
                    for _ in range(3):
                        warm = query(command, warm_location, "--symbol", "toDto", "--timings", expected=(0, 3))
                        assert warm["timings"]["scan"] == 0 and warm["timings"]["sync"] == 0, warm
                        for field in ("snapshot", "results", "coverage", "warnings", "status"):
                            assert warm.get(field) == first.get(field), (field, first, warm)
                print("PASS settled warm smoke: nine stable navigation responses, scan=0 sync=0", flush=True)
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
