"""Materialize real independent Git repositories and a local Maven repository."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import zipfile

SOURCE = Path(__file__).resolve().parent.parent / "fixtures"


def run(args, cwd=None, **kwargs):
    return subprocess.run(args, cwd=cwd, check=True, text=True, capture_output=True, **kwargs)


def git_init(root):
    run(["git", "init", "-q", "-b", "main"], root)
    run(["git", "add", "."], root)
    run(["git", "-c", "user.name=jman fixture", "-c", "user.email=fixture@localhost", "commit", "-qm", "fixture baseline"], root)


def materialize(destination):
    root = Path(destination).resolve()
    if root.exists() and any(root.iterdir()):
        raise ValueError(f"refusing to overwrite nonempty directory: {root}")
    root.mkdir(parents=True, exist_ok=True)
    commerce = root / "commerce"
    library = root / "internal-text"
    shutil.copytree(SOURCE / "commerce", commerce)
    shutil.copytree(SOURCE / "internal-text", library)
    classes = root / "artifact-build"
    classes.mkdir()
    source = library / "src/main/java/com/acme/text/TextUtil.java"
    run(["javac", "--release", "17", "-d", str(classes), str(source)])
    artifact = root / "maven-repository/com/acme/shared-text/2.4.1"
    artifact.mkdir(parents=True)
    binary = artifact / "shared-text-2.4.1.jar"
    with zipfile.ZipFile(binary, "w") as jar:
        for file in sorted(classes.rglob("*.class")):
            # Fixed timestamps make content identity repeatable across materializations.
            jar.writestr(zipfile.ZipInfo(file.relative_to(classes).as_posix(), (2020, 1, 1, 0, 0, 0)), file.read_bytes())
    with zipfile.ZipFile(artifact / "shared-text-2.4.1-sources.jar", "w") as jar:
        jar.writestr(zipfile.ZipInfo("com/acme/text/TextUtil.java", (2020, 1, 1, 0, 0, 0)), source.read_bytes())
    (artifact / "shared-text-2.4.1.pom").write_text('''<project><modelVersion>4.0.0</modelVersion><groupId>com.acme</groupId><artifactId>shared-text</artifactId><version>2.4.1</version></project>''')
    shutil.rmtree(classes)
    (commerce / ".gitignore").write_text(".gradle/\n**/build/\n")
    (library / "settings.gradle").write_text("rootProject.name = 'shared-text'\n")
    (library / "build.gradle").write_text("plugins { id 'java-library' }; group='com.acme'; version='2.4.1'\n")
    git_init(commerce)
    git_init(library)
    return {"root": str(root), "project": str(commerce), "binary": str(binary),
            "binaryDigest": hashlib.sha256(binary.read_bytes()).hexdigest(),
            "location": "app/src/main/java/example/AccessService.java:7",
            "artifact": "com.acme:shared-text:2.4.1"}


def evaluate(project, binary):
    """Compile and execute separately from JDTLS. The evaluator is not an agent tool."""
    import tempfile
    project, binary = Path(project), Path(binary)
    with tempfile.TemporaryDirectory(prefix="jman-eval-") as tmp:
        source = project / "app/src/main/java/example/AccessService.java"
        result = subprocess.run(["javac", "--release", "17", "-cp", str(binary), "-d", tmp, str(source)], capture_output=True, text=True)
        if result.returncode:
            return {"success": False, "reason": "compile", "stderr": result.stderr}
        cases = [(" admin ", "true"), ("ADMIN", "true"), ("\u2003admin\u2003", "true"), ("alice", "false"), ("superadmin", "false")]
        actual = []
        for value, expected in cases:
            result = run(["java", "-cp", tmp + os.pathsep + str(binary), "example.AccessService", value])
            actual.append({"input": value, "expected": expected, "actual": result.stdout.strip()})
        return {"success": all(c["expected"] == c["actual"] for c in actual), "cases": actual}


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("destination")
    args = parser.parse_args()
    print(json.dumps(materialize(args.destination), indent=2))
