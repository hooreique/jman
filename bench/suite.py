"""User-provided Java project/task suites, independent of the bundled fixture."""
import fnmatch
import json
from pathlib import Path
import shutil

from fixture import git_init, materialize, writable_tree


def manifest(path):
    if not path:
        return None
    file = Path(path).resolve()
    data = json.loads(file.read_text())
    for key in ("template", "project", "prompt", "allowedEdits", "evaluate"):
        if key not in data:
            raise ValueError(f"suite requires {key}")
    if not isinstance(data["evaluate"], list) or not data["evaluate"]:
        raise ValueError("evaluate must be a nonempty argv array")
    if data["project"] not in data.get("repositories", [data["project"]]):
        raise ValueError("project must also be a repository root in repositories")
    data["template"] = str((file.parent / data["template"]).resolve())
    data["manifestPath"] = str(file)
    # Evaluator argv may use {suite} to reference evaluator code outside the agent workspace.
    data["evaluate"] = [part.replace("{suite}", str(file.parent)) for part in data["evaluate"]]
    return data


def create(spec, destination):
    if spec is None:
        return materialize(destination)
    root = Path(destination).resolve()
    shutil.copytree(spec["template"], root, ignore=shutil.ignore_patterns(".git", ".gradle", "build", "__pycache__"))
    writable_tree(root)
    project = (root / spec["project"]).resolve()
    if not project.is_relative_to(root) or not project.is_dir():
        raise ValueError("suite project must be a directory inside template")
    for relative in spec.get("repositories", [spec["project"]]):
        repo = (root / relative).resolve()
        if not repo.is_relative_to(root) or not repo.is_dir():
            raise ValueError("repository must be inside template")
        git_init(repo)
    return {"root": str(root), "project": str(project), "suite": spec["manifestPath"]}


def copy_edits(project, clean, paths, allowed):
    """Copy only permitted regular files; keep evaluator/build inputs pristine."""
    okay = True
    for relative in paths:
        source = project / relative
        target = clean / relative
        if not any(fnmatch.fnmatchcase(relative, pattern) for pattern in allowed):
            okay = False
            continue
        if not source.resolve().is_relative_to(project) or not target.resolve().is_relative_to(clean) or source.is_symlink():
            okay = False
            continue
        if source.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(source.read_bytes())
        elif not source.exists() and target.is_file():
            target.unlink()
        else:
            okay = False
    return okay
