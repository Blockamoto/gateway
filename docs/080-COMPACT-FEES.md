# Compact transaction fee compositions

`recipes/builtins/transaction-fees.json` sums ordinary input amounts and outputs,
checks outputs do not exceed inputs, derives the transaction fee, and stores the
prior fee offset and block total. Native Sat fee receipts and saved/custom typed
COW tables execute the same program. The native adapter supplies already checked
ordered input amounts and validates row identity; the existing 256-row page and
manifest codecs retain their bytes, checksums, paths and publication order.

`bitcoin.input_value` is an explicit selected point input keyed by a current
ordinary input coordinate. It uses the latest preceding same-block transaction
incarnation or retained positional TXIO creation links plus exact output facts.
It neither starts Coin/Sat/TXIO nor fetches hidden historical blocks. Missing
amounts wait. Same-block transaction positions are indexed once per reader.
Selected creation anchors are checked on read and again at publication. Used
facts, values and anchors become independent child-owned COW imports, so queries
and maintenance do not require a surviving producer.

`recipes/builtins/checked-fee-prefix.json` composes externally checked fee facts
into the same rows and prefix. Its explicit `bitcoin.transaction_fee` / `core`
point source checks an active Core block, exact transaction order and decimal
satoshi conversion once per current block. Core supplies this source's authority;
it is not a detached Bitcoin proof. The existing Core-backed prefix path executes
this actual program. Repacking an already derived prefix into native page rows
is a physical codec operation. Neither source grants complete preceding coverage.

The generic `add_uint` and `sub_uint` functions preserve exact uint64 arithmetic,
reject overflow/underflow and impose no Bitcoin money-range cap. Existing money
and range primitives retain their contracts. Both fee documents and point
bindings are exposed in the native Sat/TXIO inspector and guided typed editor.
Saved copies can choose an independent continuous origin, including block 1.

Native equivalence bundles fail closed on unreviewed program/source/codec changes:

- Input/output fee derivation: `19fa54527affa93d4d5f83fb2f3ff542e4eeab2ffc944fd78fb3a07026540903`.
- Checked Core fee prefix: `c147da2ab49a7298f1afb473bddd6cc5fef571cde6e089c207cd0e6c6e2f8c1b`.

The focused suite passed in 16.208 seconds. Frozen pre-migration arithmetic and
physical receipts match across 600 transactions and page boundaries. Overflow,
outputs exceeding inputs, malformed order/layout, coinbase inputs and cancellation
publish nothing. A real same-block input in block 792435 supplies the exact
amount without probing retained history. A public saved run from block 1 and
prefix reuse start no native writer. A separate synthetic selected-chain fixture
checks positive fee rows and independent owned imports through maintenance.
Exact values above JavaScript's safe integer bound and Core prefix overflow are
covered. Guided unit and headless DOM checks round-trip both actual programs and
fit a narrow viewport. Wider regression and build results belong in the ledger.
These checks are not full-chain throughput or complete consensus validation.

The wider ordered/table/Sat/TXIO/catalogue/registry regression passed 151 checks
with no skips or failures in 583.899 seconds. Final source-specific, fee,
compatibility and release-gate checks passed 33 checks in 10.570 seconds. They
include read-only historical TXIO amounts and a public saved Core-fact generation
whose query/maintenance work after its fixture RPC has closed. The latter counts
fee-inventory requests separately from the coordinator's authority probes.

A fresh-root 600-transaction synthetic comparison measured recipe 69.94 ms and
11,181,712 cumulative allocation bytes versus frozen 32.82 ms and 277,104 bytes.
This includes physical receipt writes and already decoded/input-resolved source;
it excludes acquisition, full-chain throughput and peak memory. The recipe has
measured interpreter allocation/CPU overhead; no speed improvement is claimed.
