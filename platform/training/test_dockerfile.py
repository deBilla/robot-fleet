"""The training image lists its files one by one; every local module a copied
script imports must be copied too, or the job dies on import in the cluster."""

import ast
from pathlib import Path

TRAINING = Path(__file__).resolve().parent


def _copied_files() -> set:
    files = set()
    for line in (TRAINING / "Dockerfile").read_text().splitlines():
        if line.startswith("COPY "):
            files.update(p for p in line.split()[1:-1] if p.endswith(".py"))
    return files


def _local_imports(script: str) -> set:
    modules = set()
    for node in ast.walk(ast.parse((TRAINING / script).read_text())):
        if isinstance(node, ast.Import):
            modules.update(alias.name.split(".")[0] for alias in node.names)
        elif isinstance(node, ast.ImportFrom) and node.module and node.level == 0:
            modules.add(node.module.split(".")[0])
    return {f"{m}.py" for m in modules if (TRAINING / f"{m}.py").is_file()}


def test_image_copies_every_local_module_its_scripts_import():
    copied = _copied_files()
    missing = {f"{script} imports {dep}"
               for script in copied for dep in _local_imports(script) if dep not in copied}
    assert not missing, f"Dockerfile doesn't copy: {sorted(missing)}"
