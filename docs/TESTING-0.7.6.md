# Addresses development checks

This source is for **v0.7.6: Addresses** on its development branch. It is not an approved public release or updater promotion. Use a disposable profile or a preserved copy when exercising damaged storage or alternate branches.

## Current address state

1. Open an address with no UTXO index. Confirm the balance is unavailable and the dependency action does not start a genesis scan.
2. Build a small complete UTXO prefix, then review Current UTXOs. Confirm its request has no arbitrary range and its source height/hash match the saved UTXO checkpoint.
3. Pause and restart the address build. With unchanged source state, confirm saved traversal resumes. With changed source state, confirm a fresh generation is built and earlier staging is preserved.
4. Query addresses with zero, one and many outputs. The cached balance and count must agree with shared output values across every page; page limits cannot change the total.
5. Advance UTXO processing, including same-block create/spend and a reorg. Confirm current memberships update or roll back without raw-block reads. A missed publication must be unavailable until repaired from authenticated recovery data.
6. Damage an address page or head in the disposable copy. Confirm no balance is returned, a reviewed replacement preserves prior data, and damage to optional address state does not strand the authoritative UTXO rewind.
7. When a lookup detects a damaged descendant that the quick status check has not visited, open its rebuild action. Confirm the panel requires review for the affected checkpoint and rejects the repair hint after that checkpoint changes. Pause and resume a reviewed rebuild using its saved queue entry.
8. Damage a required current or retained source Coin fact. It must remain a UTXO source-state error; a damaged fact belonging only to obsolete disconnected address evidence can require an optional address rebuild while the selected source remains usable.

## Historical address outputs

1. Review two separated ranges, including a block with no conventional-address outputs. Coverage must preserve the gap and include the empty evaluated block.
2. Build with complete retained TXIO values/scripts and no raw body. Confirm reuse succeeds; partial facts must require verified raw fallback.
3. Spend an indexed output and evict its raw block. Confirm historical records still return its BPS position and value, with unknown spend status unless separately established.
4. Page a large address, try a cursor from another address, and change the committed revision. Invalid cursors must reject and the UI must clear mixed lists.
5. Replace scanned blocks on a selected branch. Confirm reconciliation removes old-branch rows from active coverage and preserves earlier committed evidence and required facts. Missing chain evidence must not cause destructive reconciliation.
6. Enable Live from before an already retained range near the tip. Confirm it fills earlier gaps in bounded batches and reports synced only when those gaps are closed.
7. Select source-body retention. Confirm the plan explains that only newly fetched raw fallback blocks are affected; complete fact reuse and previously scanned bodies are not backfilled.
8. Reconcile away every previously scanned block. With no selected creation coverage remaining, history must be unavailable rather than report a known zero received total.

## Explorer and controls

Enter and click P2PKH, P2SH, P2WPKH, P2WSH and P2TR addresses, plus valid compressed and uncompressed P2PK public keys. Check mainnet checksums, witness encodings, mixed-case rejection, uppercase Bech32 canonicalization and public-key curve validation. P2PK destinations must be clearly labelled and kept separate from same-key P2PKH and alternative key encodings. OP_RETURN and nonstandard scripts keep their BPS/script views.

Open a profile built before P2PK support. Existing conventional-address results remain available, but incomplete P2PK coverage must not return a known zero balance or complete history. An explicit current rebuild and historical rescan backfill the new destination type while preserving previous evidence. Historical Live fills these coverage gaps before reporting synced. A profile containing newer Sat state or archives must defer shared-fact reclamation without preventing healthy UTXO or address processing.

Confirm balance, saved checkpoint, current output pages and historical received totals remain distinct. Test dependency navigation, reviewed Build/Rebuild, Pause/Resume, browser back navigation and a rejected or changed cursor. Expand Addresses on the timeline and check separate child coverage/colors in both themes and at narrow widths. Opening a page or selecting a track must not start a scan.

## Automated checks

Use the repository's Go environment (`GO111MODULE=off`, `CGO_ENABLED=0`):

```text
go test -run 'Test076Address|Test076CurrentAddresses' -count=1 .
go test -count=1 -timeout 25m ./...
go vet ./...
node scripts/test-address-panel.js
node scripts/test-addresses-browser.js
node scripts/test-p2pk-destinations-browser.js
node scripts/test-shell-navigation.js
node scripts/test-index-workspace.js
node scripts/test-index-timeline.js
node scripts/test-utxo-build-browser.js
node scripts/test-timeline-explorer-browser.js
```

[The complete Linux development run](https://github.com/Blockamoto/gateway/actions/runs/38469242401) passed against runtime source `cc56b6bcc8a8d836f3d82fd0b41084d2eb4a31c1`, including full Go tests, race checks, vet, companion/helpers, packaging and actual browser/native checks. The full corrected Windows root-package run passed in 786.677 seconds; final recovery changes also passed focused regressions. See [status](STATUS.md) for counts and the corrected initial CI failures.

Current/historical tests use bounded fixtures and authentic early-chain blocks. They do not establish a full-mainnet build, storage benchmark, system installation, public update delivery or complete Bitcoin consensus validation.
