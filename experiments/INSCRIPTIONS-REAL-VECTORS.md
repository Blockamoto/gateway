# Verified inscription declaration vectors

Three real Bitcoin blocks were acquired as isolated test fixtures on
2026-10-27. A copied 0.7.8 client first tried ordinary Bitcoin-peer acquisition
from a newly created profile with Core, serving and automatic updates off; Raw
was armed only in that profile. Four local block-map requests per candidate
returned HTTP 400 and no body was obtained. The helper did not retain the server
error details, so the cause of those failures is unknown. The copied process was
stopped. **This is not evidence of successful ordinary-peer acquisition.**

The fixture fallback fetched raw blocks from
[Mempool's documented raw-block endpoint](https://mempool.space/docs/api/rest)
over HTTPS. The public HTTP transport is not a Bitcoin P2P implementation.
Bodies and profiles live only under this checkout's ignored `build/` directory.
The input header snapshot was copied from the previously owned baseline and is
pinned by SHA-256
`534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155`.

The separate opt-in `inscription_primary_vectors_080_test.go` checks each raw
body against that header, its expected block hash and recorded SHA-256. It then
uses Gateway's existing block verifier to check proof of work, transaction
Merkle root, witness commitment, structure and weight; it requires the
complete full-block extraction receipt and matches the exact transaction ID
and inscription index. The common-engine projection's raw ordered pushes, tag
map, metadata bytes, metaprotocol values and parent declarations are compared
against that extractor. The test passed for all three candidates:

| Height | Block hash | Candidate transaction / index | Blocator | Verified raw tag declarations | Raw body SHA-256 |
| --- | --- | --- | --- | --- | --- |
| 825334 | `00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82` | `d5dca5ad2ec1cf84cf6b1dcf2592ae69b562fcff4f50d8a56370412e3c61ea2b` / 229 | `2332i229.825334` | `05`, `07` | `4ab2f4938c1e4692c1d4d9adf712b28ecfc70117a2e36ef954de6f1691239ee7` |
| 839704 | `00000000000000000000561319cb5fb7015ac787156b219d43d18c0fcc8ce415` | `ab924ff229beca227bf40221faf492a20b5e2ee4f084524c84a5f98b80fe527f` / 1 | `2286i1.839704` | `03`, `05` | `02f1ecfd2b0499d3b5c034fadcb83f2ab63d37b7b4c21ee906ba155f34a128e1` |
| 844782 | `00000000000000000001ba4032567e702c2331891081ff06abad397343753d28` | `c4c65ea6ea567b26e5adb645b569099683e7ff2b776cc846e9a9ea9f4cf85eac` / 7 | `1118i7.844782` | `03`, `05` | `d40ce4dc860de071e6b22b4ffb9626c5105a9e5cd94a5452980556fefa47339d` |

The compact verified evidence contains exact ordered pushes and raw tag values at
`build/inscription-vectors-verified-01.json`, SHA-256
`75a19d544d96d3716dca47c4752b5eae22f55f0e17cb203bbaadaec1ce3d278a`.
It is an ignored local artifact, not an independently self-authenticating proof.
For both tag-03 cases the engine reports `declared-unresolved`; the test does
not claim input provenance, a proven parent-child relationship or ownership.
Tag 05 is raw metadata bytes and tag 07 is raw metaprotocol bytes; no additional
semantic decoding is implied.

Candidate leads came from the primary-source
[Ord explorer for 825334](https://ordinals.com/inscription/d5dca5ad2ec1cf84cf6b1dcf2592ae69b562fcff4f50d8a56370412e3c61ea2bi229),
[Ord examples for 839704](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/docs/src/inscriptions/examples.md),
and [Ord explorer for 844782](https://ordinals.com/inscription/c4c65ea6ea567b26e5adb645b569099683e7ff2b776cc846e9a9ea9f4cf85eaci7).
The raw HTTP origins are the three `/api/block/<block-hash>/raw` URLs recorded in
`build/inscription-vectors-http-01/acquisition-result.json` and the report.

To reproduce with new output names, first prepare the pinned baseline inputs in
[storage/BASELINE-078.md](storage/BASELINE-078.md). The optional peer attempt
and public-HTTP fallback are separate commands:

```powershell
python experiments/peer-source/acquire_vectors.py --out build/inscription-vectors-owned-02 --deadline 180
python experiments/peer-source/acquire_http_vectors.py --out build/inscription-vectors-http-02 --timeout 25
$env:GO111MODULE = 'off'
$env:CGO_ENABLED = '0'
$env:GATEWAY_080_VECTOR_DIR = (Resolve-Path 'build/inscription-vectors-http-02').Path
$env:GATEWAY_080_VECTOR_EXPORT = (Join-Path (Resolve-Path build).Path 'inscription-vectors-verified-02.json')
go test -tags gateway_district_diff -run '^TestInscriptionPrimaryVectors080$' -count=1 -v -timeout=10m .
```

The export is written only after every test assertion passes and refuses to
overwrite an existing file. The fixture snapshot and verifier do not establish
full Bitcoin script/UTXO consensus, current selected-chain membership, or
independent agreement with all Ord versions. These three candidates do not
prove general inscription or custom-index completeness.
