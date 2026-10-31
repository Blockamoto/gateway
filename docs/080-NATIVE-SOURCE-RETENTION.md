# Native header and Raw JSON projections

Native header appends, genesis header initialization and Raw body retention now
execute actual typed JSON through the common interpreter. The byte/field codec
checks proposals against their admitted input and then uses the existing
physical writer. Chain selection, header consensus-context verification,
retention consent, append rollback and publication fences keep their existing
responsibilities.

The three executable documents are `headers.json`, `raw-blocks.json` and
`raw-block-bodies.json`. The last uses `bitcoin.block_body`, an explicit body-by-
hash source whose event contains no height. This preserves the existing case
where a verified body is retained before its height is known. A later located
read can publish height membership while reusing the same stored body; a
hash-only body never substitutes height zero or establishes prefix coverage.

The eight-source catalogue and guided editor expose all three documents and
their independent saved/custom projections. Header-only saved runs use selected
local headers without body acquisition. Complete block and body projections
share an owned parse in a body cohort. The inspector exposes native source
documents separately from the independent retained value/script layers.

Native program/physical/source equivalence pins are independent:

| Source | Program fingerprint | Native equivalence bundle |
| --- | --- | --- |
| `bitcoin.header` | `682bae48afc949acd651bf769ea6ef81a39ceb5974452652d467c906e3a9bd90` | `fdd3a9afb329f21925bf99f0ba93560d729d8528244d1789a3aee70bb80db2bb` |
| `bitcoin.block` | `1504b0bc9f7567968cb6b94be9376316858fac0b7e1d6e3ace0fa23e5b139d5e` | `d8ec591a631997771fa2610ad01a119e514af2ab2a3b15019b29f09ee2a92f7a` |
| `bitcoin.block_body` | `ee398e8df136d7b3adddf81f76fe3a19f78b314503dc396c4cd0ceeea88ed413` | `8bd115523fdc96ef079180deb7c5847ce2f7051d554682ca562fbc7673f2418e` |

Unreviewed programs fail closed before native writes. Native header bytes, Raw
segment frames/catalogue records, reference schemas, native rule identities and
old readers remain unchanged. Preserved historical prefixes are reused; they
are not relabelled as outputs of a custom recipe.

Frozen pre-migration writer comparisons match the complete 160-byte genesis/
block-1 header file and the real block-792435 Raw frame, returned reference and
catalogue identity. The existing hash-only-to-height anchoring/publication-fence
case also passes. The focused native physical comparisons passed in 2.263
seconds; the semantic/unknown-height/retained-layer catalogue subset passed in
1.025 seconds.

A single fresh-root Raw writer comparison, given an already parsed authentic
block, measured 455.75 ms and 21,895,976 cumulative allocated bytes for the recipe
path versus 517.66 ms and 1,889,984 bytes for the frozen writer. These are noisy
single-case timings, not evidence of a speed improvement. The recipe path
includes its independently owned source parse; allocation overhead remains.
Acquisition, full-chain throughput and peak memory were not measured.

The wider header/Raw/restart/rollback/coverage/publication suite passed 126
tests with one optional skip and no failures in 126.137 seconds. The final
focused source/native comparison subset and production build passed; the
executable is 25,851,904 bytes. Guided unit, narrow headless
DOM and wider workspace checks pass. Visible browser acceptance remains a
separate unfinished requirement.
