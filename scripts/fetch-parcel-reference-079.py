#!/usr/bin/env python3
"""Fetch the pinned optional Ord parent-replay fixtures; never index app data.

Run the matching Go test to verify PoW, transaction/witness commitments and the
actual Ord relationship. Header/payload hashes here only authenticate downloads.
"""

from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
from urllib.request import Request, urlopen


REFERENCES = (
    (818117, "000000000000000000035ef07d180022e09a52f44e7587cbac23f25613d35fc2", "ee936ab28fa84515d2ca5c6183aa735aca1577b922daa4cdd1f5e3d0e96a34cf"),
    (818174, "000000000000000000033aff8cd450e7a1a26d421a1d32e0d7701b1ea1c1a7c7", "f29fb2d54ae5019754b40a62b910a48f855141025c6046354f8bbd0615584591"),
    (818182, "00000000000000000002407a2a6bcca709f08aa425b65de3af9b58b295c62ae5", "28084e922f2e5d8f3425c4140610a27ea02bc93d4da3ea5dc4f4e8e75d4f1fbc"),
)


def main():
    workspace = Path(__file__).resolve().parent.parent
    root = (workspace / "build" / "parcel-reference-079").resolve()
    if not root.is_relative_to(workspace):
        raise ValueError("fixture destination escaped the workspace")
    root.mkdir(parents=True, exist_ok=True)

    def fetch(reference):
        height, block_hash, expected_payload = reference
        path = root / (block_hash + ".block")
        url = f"https://blockstream.info/api/block/{block_hash}/raw"
        if path.exists():
            data = path.read_bytes()
        else:
            request = Request(url, headers={"User-Agent": "Gateway-fixture-audit/0.8.0"})
            with urlopen(request, timeout=45) as response:
                data = response.read((8 << 20) + 1)
        if not 81 <= len(data) <= 8 << 20:
            raise ValueError(f"invalid fixture size at {height}")
        actual_header = hashlib.sha256(hashlib.sha256(data[:80]).digest()).digest()[::-1].hex()
        payload_hash = hashlib.sha256(data).hexdigest()
        if actual_header != block_hash or payload_hash != expected_payload:
            raise ValueError(f"pinned fixture mismatch at {height}; existing bytes preserved")
        if not path.exists():
            temporary = path.with_suffix(".part")
            temporary.write_bytes(data)
            temporary.replace(path)
        return {"height": height, "block_hash": block_hash, "bytes": len(data), "sha256": payload_hash, "source": url}

    with ThreadPoolExecutor(max_workers=3) as pool:
        rows = list(pool.map(fetch, REFERENCES))
    manifest = {
        "checked_utc": datetime.now(timezone.utc).isoformat(),
        "source_api_docs": "https://github.com/Blockstream/esplora/blob/master/API.md",
        "fixtures": rows,
    }
    (root / "source-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(manifest, indent=2))


if __name__ == "__main__":
    main()
