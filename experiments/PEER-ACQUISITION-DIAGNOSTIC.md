# Isolated 0.7.8 peer acquisition diagnostic

An earlier owned 0.7.8 run tried four local block-map requests for each of three
candidate blocks, but received HTTP 400 for all requests. It recorded response
status without retaining the server's error body. The cause of those failures is
therefore **unknown**; they do not establish that the endpoint was malformed or
that Bitcoin peers were unavailable.

A subsequent diagnostic copied the same pinned 0.7.8 candidate executable and
selected-header snapshot into a **new** workspace-owned profile. It starts
networking only after verifying the copied client's update mode is disabled or
manual; Core, the Core mount, serving, sharing and LAN discovery are off. It
arms Raw only in that disposable profile, waits a bounded time for connected
Bitcoin sessions and the relevant selected header, then issues one request. The
copied runtime is explicitly stopped in a `finally` block. No existing preview
or user profile is opened.

On 2026-10-27 the one request
`GET /api/v1/index/block-map?block=825334` returned HTTP 200 after the client
reported two connected Bitcoin peers and local headers through height 972932.
The Bitcoin block downloader counters changed from no initialized downloader to
one request, one response and 1,780,205 received bytes; Gateway peerhood was
locked. The block hash was
`00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82`.
The client reported matching selected header, proof of work, Merkle root,
transaction parsing and witness commitment, with `consensus_validated=false`.
The retained body SHA-256 was
`4ab2f4938c1e4692c1d4d9adf712b28ecfc70117a2e36ef954de6f1691239ee7`:
it exactly matches the independently witness-checked HTTP fixture described in
[INSCRIPTIONS-REAL-VECTORS.md](INSCRIPTIONS-REAL-VECTORS.md).

This single-block result demonstrates an ordinary Bitcoin peer acquisition in
this isolated configuration; it does not explain the earlier HTTP 400 responses,
prove availability of other historic blocks, or establish full Bitcoin
script/UTXO consensus validation. The full local API response and bounded
readiness snapshots are retained in ignored
`build/peer-diagnose-owned-02/diagnostic.json`; the profile and retained body are
also isolated there. A prior diagnostic independently returned the same 200
response and byte-identical body after one peer connected.

The original vector-acquisition helper now waits up to 45 seconds, within its
existing overall deadline, for a Bitcoin connection and selected headers before
spending per-block attempts. It also retains failure details in its own ignored
output. This improves readiness handling without claiming that it fixes the
unrecorded reason for the earlier 400s.

To reproduce with a fresh output, first prepare the pinned inputs described in
[storage/BASELINE-078.md](storage/BASELINE-078.md), then run:

```powershell
python experiments/peer-source/peer_diagnose_078.py --out build/peer-diagnose-owned-03 --wait 45
```

`--out` must be a new child of the checkout's ignored `build/` directory. The
readiness wait and single request have deadlines. The report can record HTTP
failure or timeout; `OBSERVED` alone is not a successful block fetch. The
peer-fetch run uses ordinary network connections and does not imply anonymity.
