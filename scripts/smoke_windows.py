import argparse
import json
import pathlib
import subprocess
import tempfile
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument("--binary", type=pathlib.Path, default=ROOT / "bin/devrig.exe")
parser.add_argument("--docker", action="store_true")
args = parser.parse_args()
binary = args.binary.resolve()


def run(*arguments, check=True):
    result = subprocess.run([str(binary), *arguments], capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=30)
    if check and result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    return result


run("version")
run("--help")
unsupported = run("--backend", "kube", "list", check=False)
assert unsupported.returncode != 0 and "not supported on Windows" in (unsupported.stderr + unsupported.stdout)
if args.docker:
    name = "windows-smoke-" + uuid.uuid4().hex[:12]
    try:
        created = json.loads(run("--backend", "docker", "up", "postgres", "--name", name).stdout)
        assert created["ready"] and created["host"] == "127.0.0.1", created
        assert run("--backend", "docker", "url", name).stdout.strip().startswith("postgres://")
        run("--backend", "docker", "status", name)
        run("--backend", "docker", "list")
        run("--backend", "docker", "logs", name)
    finally:
        run("--backend", "docker", "down", name)
print("devrig: Windows executable smoke passed")
