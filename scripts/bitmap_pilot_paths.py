"""Safety helpers for disposable Bitmap pilot workspaces; no profile discovery."""
import argparse
import os
from pathlib import Path
import stat
import subprocess

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / "build"
MARKER = ".bitmap-populated-pilot-owned"


def stop_owned(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)


def reject_links(path):
    path = Path(os.path.abspath(path))
    for part in reversed((path, *path.parents)):
        if not os.path.lexists(part):
            raise ValueError(f"missing path component: {part}")
        info = os.lstat(part)
        if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
            raise ValueError(f"link or reparse point refused: {part}")
    return path


def workspace(path):
    build = reject_links(BUILD)
    out = Path(os.path.abspath(path))
    if out.parent != build or not out.name.startswith("bitmap-pilot-"):
        raise ValueError("output must be a direct build/bitmap-pilot-* child")
    reject_links(out)
    marker = out / MARKER
    reject_links(marker)
    if not marker.is_file() or marker.read_text(encoding="ascii") != "owned bitmap pilot v1\n":
        raise ValueError("missing owned pilot marker")
    return out


def io_path(path):
    path = os.path.abspath(path)
    if os.name == "nt" and not path.startswith("\\\\?\\"):
        path = "\\\\?\\" + path
    return Path(path)


def inspect_tree(path):
    reject_links(path)
    def onerror(exc):
        raise exc
    for current, dirs, files in os.walk(io_path(path), followlinks=False, onerror=onerror):
        for name in dirs + files:
            item = Path(current) / name
            info = os.lstat(item)
            if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
                raise ValueError(f"link or reparse point in seed: {item}")
            if stat.S_ISREG(info.st_mode) and info.st_nlink > 1:
                raise ValueError(f"hard-linked seed file refused: {item}")
            if not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
                raise ValueError(f"special file in seed: {item}")


def init(path):
    build = reject_links(BUILD)
    out = Path(os.path.abspath(path))
    if out.parent != build or not out.name.startswith("bitmap-pilot-"):
        raise ValueError("output must be a direct build/bitmap-pilot-* child")
    out.mkdir(mode=0o700)  # exclusive; never reuse any existing directory
    with (out / MARKER).open("x", encoding="ascii") as marker:
        marker.write("owned bitmap pilot v1\n")
    print(out)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--init", required=True, help="new build/bitmap-pilot-* directory")
    args = parser.parse_args()
    init(args.init)
