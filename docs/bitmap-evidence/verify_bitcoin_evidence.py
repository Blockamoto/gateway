"""Offline Bitcoin evidence verifier; Python 3 stdlib only.

Run: python docs/bitmap-evidence/verify_bitcoin_evidence.py
This verifies a downloaded block's header hash/PoW, transaction Merkle root,
SegWit commitment, BIP34 coinbase height and the specified inscription witness.
It does NOT validate the entire Bitcoin chain, UTXOs, script execution or Ord.
"""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
TARGET = "86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732"
BLOCK = "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c"


def sha256d(data):
    return hashlib.sha256(hashlib.sha256(data).digest()).digest()


class Reader:
    def __init__(self, data):
        self.data, self.pos = data, 0

    def read(self, size):
        if size < 0 or self.pos + size > len(self.data):
            raise ValueError("truncated data")
        data = self.data[self.pos:self.pos + size]
        self.pos += size
        return data

    def compact(self):
        first = self.read(1)[0]
        if first < 253:
            return first
        size = {253: 2, 254: 4, 255: 8}[first]
        value = int.from_bytes(self.read(size), "little")
        if value < {2: 253, 4: 65536, 8: 4294967296}[size]:
            raise ValueError("noncanonical CompactSize")
        return value

    def varbytes(self):
        return self.read(self.compact())


def transaction(reader):
    start = reader.pos
    version = reader.read(4)
    segwit = reader.data[reader.pos:reader.pos + 2] == b"\x00\x01"
    if segwit:
        reader.read(2)
    base_start = reader.pos
    ins = []
    for _ in range(reader.compact()):
        prev = reader.read(36)
        script = reader.varbytes()
        sequence = reader.read(4)
        ins.append({"prev": prev, "script": script, "sequence": sequence})
    outs = []
    for _ in range(reader.compact()):
        value = int.from_bytes(reader.read(8), "little")
        outs.append({"value": value, "script": reader.varbytes()})
    base = version + reader.data[base_start:reader.pos]
    witnesses = []
    if segwit:
        for _ in ins:
            witnesses.append([reader.varbytes() for _ in range(reader.compact())])
    locktime = reader.read(4)
    raw = reader.data[start:reader.pos]
    return {"raw": raw, "txid": sha256d(base + locktime), "wtxid": sha256d(raw),
            "inputs": ins, "outputs": outs, "witnesses": witnesses}


def merkle(leaves):
    while len(leaves) > 1:
        if len(leaves) % 2:
            leaves = leaves + [leaves[-1]]
        leaves = [sha256d(leaves[i] + leaves[i + 1]) for i in range(0, len(leaves), 2)]
    return leaves[0]


def pushes(script):
    reader = Reader(script)
    result = []
    while reader.pos < len(script):
        offset = reader.pos
        op = reader.read(1)[0]
        if op <= 75:
            value = reader.read(op)
        elif op in (76, 77, 78):
            value = reader.read(int.from_bytes(reader.read(1 << (op - 76)), "little"))
        else:
            value = None
        result.append((offset, op, value))
    return result


def verify():
    data = (ROOT / "genesis-block.raw").read_bytes()
    reader = Reader(data)
    header = reader.read(80)
    block_hash = sha256d(header)[::-1].hex()
    if block_hash != BLOCK:
        raise ValueError("unexpected block hash")
    bits = int.from_bytes(header[72:76], "little")
    target = (bits & 0x007fffff) * 256 ** ((bits >> 24) - 3)
    if bits & 0x00800000 or int(block_hash, 16) > target:
        raise ValueError("invalid header proof of work")
    txs = [transaction(reader) for _ in range(reader.compact())]
    if reader.pos != len(data):
        raise ValueError("trailing bytes")
    if merkle([tx["txid"] for tx in txs]) != header[36:68]:
        raise ValueError("transaction Merkle root mismatch")
    coinbase = txs[0]
    height_script = coinbase["inputs"][0]["script"]
    height = int.from_bytes(height_script[1:1 + height_script[0]], "little")
    if height != 792435:
        raise ValueError("wrong BIP34 height")
    commitments = [o["script"][6:38] for o in coinbase["outputs"]
                   if len(o["script"]) >= 38 and o["script"][:6] == bytes.fromhex("6a24aa21a9ed")]
    reserved = coinbase["witnesses"][0][0]
    witness_root = merkle([bytes(32)] + [tx["wtxid"] for tx in txs[1:]])
    if len(reserved) != 32 or sha256d(witness_root + reserved) != commitments[-1]:
        raise ValueError("witness commitment mismatch")
    matching = [(i, tx) for i, tx in enumerate(txs) if tx["txid"][::-1].hex() == TARGET]
    if len(matching) != 1:
        raise ValueError("origin transaction missing or duplicated")
    tx_index, tx = matching[0]
    # This fixture has one ordinary script-path input and one inscription envelope.
    if len(tx["witnesses"]) != 1 or len(tx["witnesses"][0]) != 3:
        raise ValueError("unexpected origin witness layout")
    script = tx["witnesses"][0][-2]
    tokens = pushes(script)
    marker = [(i, token[0]) for i, token in enumerate(tokens)
              if token[1:] == (3, b"ord") and i >= 2
              and tokens[i - 2][1:] == (0, b"") and tokens[i - 1][1] == 99]
    if len(marker) != 1:
        raise ValueError("unexpected origin envelope count")
    i, marker_offset = marker[0]
    expected = [(1, b"\x01"), (24, b"text/plain;charset=utf-8"), (0, b""), (8, b"0.bitmap"), (104, None)]
    # The content-type string has 24 bytes. Check exact opcodes as well as values.
    if [(t[1], t[2]) for t in tokens[i + 1:]] != expected:
        raise ValueError("unexpected origin envelope payload: " + repr(tokens[i + 1:]))
    result = {"block_hash": block_hash, "height_from_bip34": height,
              "block_timestamp": int.from_bytes(header[68:72], "little"),
              "transaction_count": len(txs), "transaction_index_zero_based": tx_index,
              "txid": TARGET, "wtxid": tx["wtxid"][::-1].hex(), "inscription_id": TARGET + "i0",
              "input_index": 0, "envelope_index": 0, "ord_marker_script_offset": marker_offset,
              "body_hex": b"0.bitmap".hex(), "content_type": "text/plain;charset=utf-8",
              "checks": ["header_hash", "header_pow", "bip34_height", "transaction_merkle_root",
                         "bip141_witness_commitment", "exact_origin_envelope"],
              "limits": ["not a full Bitcoin chain/UTXO/script validation", "not an Ord index rebuild",
                         "inclusion does not prove absence of earlier matching inscriptions"]}
    (ROOT / "genesis-tx.raw").write_bytes(tx["raw"])
    (ROOT / "genesis-verification.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf8")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    verify()
