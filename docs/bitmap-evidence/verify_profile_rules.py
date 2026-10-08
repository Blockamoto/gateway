"""Execute only the pinned OPI pure grammar function, without starting its indexer.

These synthetic byte cases establish grammar behavior, NOT historical winners.
Python stdlib only; run from any directory.
"""
import ast
import codecs
import json
from pathlib import Path

root = Path(__file__).resolve().parent
tree = ast.parse((root / "opi-bitmap-index.py").read_text(encoding="utf8"))
function = next(node for node in tree.body if isinstance(node, ast.FunctionDef)
                and node.name == "get_bitmap_number")
namespace = {"codecs": codecs}
exec(compile(ast.Module(body=[function], type_ignores=[]), "pinned-opi-grammar", "exec"), namespace)
cases = [(b"0.bitmap", 0), (b"404.bitmap", 404), (b"00.bitmap", None),
         (b"0404.bitmap", None), (b".bitmap", None), (b"-1.bitmap", None),
         (b"+1.bitmap", None), (b"1.0.bitmap", None), (b"1.BITMAP", None),
         (b" 1.bitmap", None), (b"1.bitmap\n", None), (b"1.bitmap\x00", None),
         ("\u0661.bitmap".encode(), None), (b"\xff.bitmap", None),
         (b"792435.bitmap", 792435), (b"792436.bitmap", 792436)]
results = []
for body, expected in cases:
    actual = namespace["get_bitmap_number"](body.hex())
    if actual != expected:
        raise RuntimeError(f"unexpected OPI parse: {body!r}: {actual} != {expected}")
    results.append({"body_hex": body.hex(), "parsed_district": actual,
                    "eligible_by_height_at_792435": actual is not None and actual <= 792435})
report = {"source_commit": "0a09b987c87692ec3cabd404c8bcc7367707ee9a",
          "case_kind": "synthetic grammar cases, not historical acceptance evidence",
          "passed": len(results), "cases": results}
(root / "profile-rule-checks.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf8")
print(f"PASS: {len(results)} pinned OPI grammar cases; no indexer/database executed")
