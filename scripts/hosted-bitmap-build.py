#!/usr/bin/env python3
"""Build only test executables in a new owned workspace. No profile access."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path.cwd().resolve()
sys.path.insert(0, str(ROOT / "scripts"))
from bitmap_pilot_paths import workspace

BASE = "efc5b13c8a43d43b91e6423707e81ccf74e9f2db"


def sha(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT)


def write_exclusive(path, raw):
    with path.open("xb") as stream: stream.write(raw)


def stop_owned_build_tree(process):
    # The Go command starts compiler/linker descendants. Its process handle
    # remains open here, so its PID cannot be reused while we stop that tree.
    if process.poll() is None:
        try:
            subprocess.run(["taskkill.exe", "/PID", str(process.pid), "/T", "/F"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30, check=True)
        finally:
            if process.poll() is None:
                process.kill()
            process.wait(timeout=10)


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--out", required=True)
    parser.add_argument("--fixture", required=True, type=Path)
    parser.add_argument("--candidate-revision", required=True)
    parser.add_argument("--go", default="go")
    args=parser.parse_args()
    if os.name != "nt": raise ValueError("Windows runner required")
    out=workspace(args.out)
    head=git("rev-parse", "HEAD").decode().strip()
    if len(args.candidate_revision)!=40 or head!=args.candidate_revision:
        raise ValueError("checkout does not match explicit full candidate commit")
    if git("status", "--porcelain", "--untracked-files=all").strip():
        raise ValueError("source checkout contains tracked or untracked changes")
    if git("rev-parse", BASE).decode().strip()!=BASE:
        raise ValueError("baseline revision unavailable")
    fixture=args.fixture.resolve()
    source=out/"hosted-fixture.go";write_exclusive(source,fixture.read_bytes())
    mappings={"current":{str(ROOT/"bitmap_populated_pilot_test.go"):str(source)},"baseline":{str(ROOT/"bitmap_populated_pilot_test.go"):str(source)}}
    old={}
    for name in ("ordinal_state_tree.go", "ordinal_state_tree_test.go", "bitmap_development_evidence.go"):
        destination=out/("old-"+name);write_exclusive(destination,git("show",BASE+":"+name))
        mappings["baseline"][str(ROOT/name)]=str(destination)
        old[name]=sha(destination)
    env=dict(os.environ,GO111MODULE="off",CGO_ENABLED="0")
    binaries={}
    for label in ("current","baseline"):
        overlay=out/(label+"-overlay.json")
        write_exclusive(overlay,(json.dumps({"Replace":mappings[label]},indent=2)+"\n").encode())
        binary=out/(label+"-hosted.test.exe")
        if os.path.lexists(binary): raise FileExistsError(binary)
        with (out/(label+"-build.log")).open("xb") as log:
            proc=subprocess.Popen([args.go,"test","-c","-overlay",str(overlay),"-o",str(binary),"."],cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,creationflags=subprocess.CREATE_NO_WINDOW)
            try:
                status=proc.wait(timeout=900)
                if status: raise RuntimeError(label+" build failed; see build log")
            finally:
                stop_owned_build_tree(proc)
        binaries[label]={"name":binary.name,"sha256":sha(binary)}
    receipt={"schema":1,"candidate_revision":head,"workflow_revision":os.environ.get("GITHUB_SHA"),"baseline_revision":BASE,"baseline_overlay_sources_sha256":old,"current_ordinal_sha256":sha(ROOT/"ordinal_state_tree.go"),"current_evidence_sha256":sha(ROOT/"bitmap_development_evidence.go"),"hosted_fixture_sha256":sha(source),"go_version":subprocess.check_output([args.go,"version"],text=True).strip(),"binaries":binaries,"scope":"baseline overlays old ordinal and detached-evidence source onto the same candidate; not a whole-release comparison"}
    write_exclusive(out/"build-receipt.json",(json.dumps(receipt,indent=2)+"\n").encode())
    print(json.dumps(receipt,indent=2))

if __name__=="__main__": main()
