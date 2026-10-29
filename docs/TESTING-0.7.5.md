# Gateway 0.7.5 prerelease testing

Use a disposable profile for destructive fixtures and reviewed rebuilds. The source adds UTXO state and optional TXIO value/script history. Gateway peerhood, Sats/Satline, address and later feature stages remain locked. The corrected frozen source and exact Windows candidate have passed the checks below. Final archive, published Linux executable and public updater acceptance are recorded separately in the release acceptance evidence.

## UTXO build and state

1. Select **UTXO set**. The panel must show no processed prefix before a build, an explicit selected target, Pruned retention, a fixed 288-transition policy and `consensus_validated=false`. Unknown byte measurements must remain unavailable.
2. Prepare the plan. Reviewing it must not start processing or write a state head. Start the reviewed build and confirm that it begins at genesis. The UI must not offer an arbitrary fresh UTXO range.
3. Use a small complete prefix with local verified bodies. Check processed height/hash, live output count/value and output lookup against the fixture. Zero-based output BPS and `txid:vout` lookup must identify the same Coin. Genesis outputs, OP_RETURN outputs and scripts larger than 10,000 bytes must not enter live membership.
4. Pause while processing or waiting for a missing body. Restart and Resume. Only a committed prefix may be reported, and existing state must survive missing providers. Restore the body and confirm continuation from the next block.
5. A prefix below the selected target must not be called Current. Equality of heights with different hashes must not be called Current either. When selected-chain anchors cannot be obtained, saved state remains intact and the UI waits.

The dedicated UI follows the selected tip. Developer fixtures can use a bounded genesis-prefix API job for predictable tests. Do not use an isolated high-height range as a substitute for constructing UTXO state.

## State transitions, recovery and interrupted writes

- Spend an ancient output and retain its full facts even when its creation block is outside the recent recovery window. Remove source bodies and verify Coin lookup and local undo still work.
- Create an output and spend it later in the same block. Undo the block: its intermediate output must stay absent, while the original input Coin is restored.
- Use exact historical BIP30 exception fixtures. Native outpoint lookup must select the replacement incarnation, then restore the displaced incarnation after undo. A height-only exception or an ordinary duplicate live transaction ID must be rejected.
- Exercise radix leaf splitting with many outputs and rollback across that split. The restored state must use its authenticated parent roots and preserve exact values/scripts.
- Inject interruption after facts, pages, journal and commit writes, before head publication, and after head publication. Reopen the store. Before-publication failures must preserve the old head; an already published head must survive restart. Retrying uncommitted work must not double-count coins.
- Reject missing or double-spent inputs and outputs exceeding inputs without advancing the head. These structural/value checks do not imply complete Bitcoin consensus validation.
- Reorganize within the retained window, obtain replacement bodies and replay the new branch. Undo exactly 288 transitions to the retained boundary. A deeper ancestor must produce Rebuild required without silently resetting state.
- Select a known shorter chain within the recovery window. Heights above its explicit tip must be treated as disconnected rather than unavailable evidence. Change the captured selected tip during recovery: no further parent head may publish under that stale anchor.
- Corrupt a head checksum, remove a state root, remove a referenced fact page and damage an undo journal. The corresponding operation must fail closed. Cheap status validates the head and four root pages; descendant pages and full facts are authenticated when accessed, not fully walked on every poll.
- Run maintenance and confirm expired commits/journals and unreachable COW pages become eligible for cleanup. A full state walk is not part of every block commit or management lookup.
- Run page collection on coarse-timestamp storage. Even-second marks must be read back exactly before sweep; a successful setter which stores a different time must abort cleanup without deleting live or orphan pages. The opt-in source test uses `GATEWAY_TEST_GC_DIRECTORY` and preserves its fixture directory for inspection.

These failure-injection tests model interrupted application writes. They do not simulate every filesystem, hardware or power-loss failure.

## Explicit rebuild

Show **Rebuild required**, select **Review rebuild** and inspect the genesis plan. No reset may occur until the reviewed build starts. Preserve the prior engine directory and its fact pins. Pause or restart the same rebuild job after new progress has committed: Resume must keep that progress rather than archive/reset it again.

A corrupt prior head can be preserved by an explicit rebuild. Its archive must stop shared-fact collection until the archive is inspectable; cleanup must not guess its dependencies. Prior archives are not deleted automatically. Check available space and include archive bytes in storage inspection.

## Independent history and grouped coverage

- Transaction Locator remains a separate track/store. Positional TXIO lookup must work without a complete locator history, while missing creation translations remain unresolved.
- Build TXIO links without Values or ScriptPubKeys. Then enrich Values alone, scripts alone and both. Verify independent coverage and exact creation-block facts; do not copy predecessor facts onto each input.
- Keep optional history off during UTXO processing. Required live and rollback facts must still persist. Shared page collection must retain the union of UTXO, recovery, archives and selected TXIO history, including old spent Coins still recoverable.
- Read existing version-zero/version-two TXIO and 0.7.4 Blocks profiles. Preserve original committed records and the usable legacy state until a verified replacement is available. Restart halfway through compatible enrichment/migration.
- Collapse/expand Blocks and TXIO. Header coverage must remain distinct from raw-body coverage; TXIO links, Values and ScriptPubKeys must have distinct stable colors and their own coverage. Existing files must not fabricate missing layers.
- Verify desktop/narrow layouts in both themes, keyboard focus, review/build/pause/resume/waiting/rebuild states and disabled controls when status is unavailable. BPS navigation must restore the normal block visualization after leaving the UTXO panel.

## Source verification and release checks

Focused engine/rebuild checks were run successfully on the development worktree with:

```text
GO111MODULE=off CGO_ENABLED=0 go test -short -run '^TestUTXO' -count=1 -timeout=120s .
```

The short suite checks fact-cache reauthentication across operations, complete-value/script requirements, cancellation and page/payload eviction bounds. It excludes the larger synthetic maintenance fixture. The source also contains runtime tests in `index_utxo_runtime_test.go`, shared-fact/TXIO tests, and `scripts/test-utxo-panel.js` / `scripts/test-utxo-build-browser.js`. The browser script uses isolated mocked status/providers: it verifies UI behavior and requests, not a whole-chain build or real peer delivery. Record broad Go, static, race and browser results separately before promotion.

The ordinal tree/cleanup and shorter-chain checks passed on 13 October 2026, including an actual removable exFAT fixture. That filesystem rounded the earlier odd-second cleanup mark, revealing a page-deletion defect before release. The final code uses a disjoint versioned even-second mark encoding and verifies timestamp readback. Independent native/profile acceptance against the corrected executable passed, including genuine 0.7.3 migration, local UTXO processing and cleanup on exFAT. This does not retroactively establish the earlier build's integrity.

The larger synthetic fixture also passed on this development machine: **10,000 live Coins, 321 non-genesis transitions and 320 actual spend mutations**, including old-Coin restoration after cleanup. Prefix construction took 69.436 seconds. Maintenance plus subsequent inspection and undo checks took 12.541 seconds, using 44 resumable mark passes. Engine COW page files fell from 4,220 / 13,145,792 bytes to 3,907 / 12,228,549 bytes; these figures exclude shared fact pages, raw bodies, commits and journals. The test confirmed the final 289 checkpoints and 288 journals.

Earlier runs of the same geometry exceeded a four-minute limit before maintenance. They exposed repeated fact-page decoding and long common prefixes in internal positional keys and the fixture's zero-padded synthetic transaction IDs. The final code uses operation-local authenticated fact caching and dispersed internal keys while retaining original positional/native identities in checked records. No completed equivalent baseline, speedup ratio or full-chain throughput estimate is claimed. The fixture uses trivial scripts and synthetic bodies; it does not measure Bitcoin peer retrieval, consensus validation, power loss or full-mainnet storage.

Frozen source `56bdc48864958b2c60dd41beb3b28a1897062cc0` passed full Windows tests (1,051 pass / 45 skip / zero fail) and vet. Linux CI passed full tests (1,029 / 50 / zero), root race checks (511 / 17 / zero), updater race checks (159 / one / zero), static/helper/packaging and browser checks. The exact Windows client passed 36 browser/native/local-updater checks with zero skips. Genuine mainnet genesis..170 processing passed with deterministic Pause/Resume, raw eviction and offline restart, simulated ancestor-169 rollback and real block-170 replay. The exact run reused authentic bodies previously fetched through existing Bitcoin providers; networking was disabled for the local acceptance. A genuine 0.7.3 profile migrated without changing its original legacy records. All seven compiled components passed binary/version and installer payload audits.

Final archives and genuine 0.7.4 extractor/publisher-origin compatibility passed. All eight original public assets matched accepted sizes and GitHub SHA256 digests. [Published Linux acceptance](https://github.com/Blockamoto/gateway/actions/runs/38449695150) authenticated exact fresh/application-only archive members, executable hashes and compatibility before bootstrap, helper no-write rejection, header import/restart and native offline UI execution. Released source is `cd9d6088653601f3a104e9ed3ba8405420f81428`; final runtime equality confirms all six runtime components are byte-identical to the accepted `56bdc48` candidate.

Initial hosted verification authenticated app9/header8 and actually downloaded both application ZIPs plus 97 chunks: 99 objects / 87,879,424 payload bytes. An initial Python transport check failed one Linux payload; the failure was preserved, while native curl diagnostic and unchanged full verification passed. A genuine published 0.7.4 Windows client automatically installed/restarted into exact 0.7.5 with four executable hashes and saved settings/theme/policy/headers/bootstrap verified. A separate fresh 0.7.5 default profile automatically checked both feeds. The [renewal workflow](https://github.com/Blockamoto/gateway/actions/runs/38452032129), independent app10/header9 signature and unchanged-payload comparison, all 99 renewed downloads, and exact 0.7.5 normal-startup acceptance passed. That client performed no application package download, staging, installation or restart and retained its full validated header prefix while ordinary header synchronization continued.

Hosted checks used disposable header/policy fixtures and a warmed publisher, not complete historical TXIO/UTXO databases; separate native early-chain/migration checks cover those scenarios. No whole-chain UTXO replay, performance/storage reduction ratio, system installation, antivirus clearance, cold-start measurement or independently consensus-validated state is claimed. See [the final acceptance record](RELEASE-ACCEPTANCE-0.7.5.json) for receipt hashes and precise scopes. The frozen package receipt records the prepublication stage; later operational acceptance is recorded here without replacing released binaries or changing the tag.

See [UTXO storage and retention](UTXO-INDEX.md), [the implementation brief](IMPLEMENTATION-BRIEF-0.7.5.md) and [status](STATUS.md). Earlier Blocks/inscription/transaction behavior is covered by [the 0.7.4 guide](TESTING-0.7.4.md).
