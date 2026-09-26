# Networking floor audit — Gateway v0.5.4 baseline

Audited baseline: `5acabb0ac27c6bcd9786b65a43236dd2962982b8`, with the audit brief added by `7f2ce1b9d39f2105c7ba0f116d5cd9925f0d2ff2`. No production files or Git state were modified by this audit subtask.

## Conclusion

The narrow stability repair is justified. Two brief hypotheses are actual defects: a request timeout closes its healthy transport, and negotiated inbound sockets are unavailable to managed outbound request routing. However, the hypothesis that ordinary peer replacement is absent or fundamentally broken is too strong: a controlled native Windows test shows automatic replacement already works when eligible peers exist. Preserve the existing manager and bounded scheduler while repairing lifecycle and capability selection.

The v0.5.4 code already retains a bounded number of negotiated Gateway members without requiring useful BOD coverage. Do not rebuild that feature or incorrectly describe the whole codebase as treating membership and usefulness identically. Discovery's stopping condition, request routing, header selection and status presentation still conflate some of these concepts.

A subsequent native Windows regression investigation found another release blocker: rejected header batches do not roll back the selected header file (NF6). This is a reproducible production defect, separate from fixture/environment failures involving Windows symlink privileges.

## Evidence and test conditions

Native Windows, Go `go1.27.1 windows/amd64`, `GO111MODULE=off`. Extra fixtures are stored in `network_floor_repro_test.go.txt` and introduced only via `audit-working/network-floor-overlay.json` as virtual root file `zz_audit_network_floor_test.go`. They use local sockets and the repository's existing fixture helpers; they do not contact public peers. Command:

```powershell
$env:GO111MODULE='off'
& "$env:TEMP/gateway-audit-toolchain/go/bin/go.exe" test -vet=off -overlay audit-working/network-floor-overlay.json -run '^TestAudit' -v .
```

All six completed in 2.528 seconds. Output: `network-floor-test-output.txt`. Five deliberately assert the defective baseline behavior and label it `CONFIRMED`; a passing reproduction means the defect exists, not that the acceptance requirement passes. The sixth is positive counter-evidence for replenishment. A normal test command first failed at Go vet's existing `satline_store.go:182:21` nonconstant `fmt.Errorf` diagnostic. Runtime tests were then explicitly separated with `-vet=off`.

These are real Windows fixture tests, not two-machine Windows acceptance, public discovery evidence, race testing, long-running liveness testing, or evidence that a proposed repair works.

After the full suite reported `Test051HeaderBadBatchRollsBack`, an isolated run reproduced that existing regression failure. An additional audit-only `TestAuditWindowsHeaderRollbackCause` proves the Windows handle-permission cause. Its output is `header-rollback-cause-output.txt`; the original isolated failure is `header-rollback-isolated-output.txt`. The seventh audit fixture took 0.03 seconds (package total 0.208 seconds) and deliberately passes only when the defective baseline and contrasting OS handle behavior are observed.

## Concrete findings

### NF1 — P1: a request deadline removes a responsive shared connection

`network_051.go:436–480`, specifically `request` at 470–472, calls `s.close` when the request context expires. `close` then removes the session and records a transport failure/backoff at 342–356. This affects getheaders, Bitcoin blocks, Gateway BOD and Satline requests sharing the socket. Ordinary traffic, ping/pong and successful work by another subsystem cannot prevent it.

Reproduction `TestAuditBaselineRequestTimeoutKillsResponsiveSession`: a managed peer receives getheaders, withholds headers, sends a valid ping and receives its pong; a 150ms request deadline still closes the session and removes it from the manager. The ping/pong completes before the deadline. This validates the brief's claim with direct evidence of healthy transport.

Smallest safe repair: separate request outcome from connection outcome and record both. Keep real EOF/write/protocol/integrity failure handling. Do **not** merely delete the close call: the response dispatch design needs the late-response protection below. Check context before waiting for a writer and sending a request too; the current send path does not honor the request context while waiting for `writeMu`.

### NF2 — P1: inbound Gateway is visible but cannot initiate managed BOD requests

`overlay.go:350–484` owns accepted sockets in `overlayServer.handleConn`; it never constructs/registers a `bitcoinSession`. It answers Gateway hello and BOD messages in its own loop. `network_051.go:594–623` discovers/routs BOD only through `n.sessions`. `peers.go:12–59` tracks inbound sockets separately in `source.live`.

`discoverySnapshot` counts BOD and usefulness from managed sessions at `network_052.go:564–603`, then adds inbound Gateway count only to `gateway_connected` at 608–622. As a result there can be one live negotiated inbound BOD-serving socket, `gateway_connected=1`, `connected=0`, `bod_available=0`, and no managed BOD peer selectable for an outbound lookup.

Reproduction `TestAuditBaselineInboundNotManagedOrBODCounted`: a real loopback TCP connection completes Bitcoin + BIP434 + Gateway/BOD negotiation and a BOD status response. The listening app has exactly the inconsistent counts above and no routed BOD peers.

Smallest safe repair: after inbound hello is valid and its acknowledgement is written, transfer ownership exactly once into the managed session reader/writer/service machinery. Preserve direction and the actual negotiated protocols. Derive all live counters from the same objects. Keep protocol support, willingness to serve, coverage and demonstrated success as separate fields. Give a physical socket a session identity; an inbound source port is not its dialable listener or stable peer identity. A future handoff must not leave two readers or two write owners on the socket.

### NF3 — P2: valid persistent BOD sessions are forcibly closed at request 65

Inbound `overlay.go:454–456` increments a lifetime request counter and closes after 64. Managed serving `network_052.go:683–686` does the same. Satline has a lifetime limit of 16 (`network_052.go:691–697`); the inbound listener uses one shared `requestCount` for both BOD and Satline (`overlay.go:434–456`), so previous BOD requests can consume the Satline lifetime allowance.

Reproduction `TestAuditBaselineBODSessionLifetimeQuota`: 64 unique, valid BOD status exchanges succeed; the 65th receives EOF. This is independent of the rate at which requests arrive. The source comment at `overlay.go:370–371` still assumes one request per connection, while v0.5.x retains sessions and requests health every two minutes (`network_052.go:238–241`). Even an otherwise idle retained session eventually reaches this cap; ordinary user traffic accelerates it.

Smallest safe repair: replace lifetime quotas with bounded per-time operation budgets and bounded in-flight work. Keep bounded replay-ID tracking with expiry and a hard memory limit. If throttled, send a bounded `busy` response instead of destroying healthy membership. Do not simply remove all limits.

### NF4 — P2: an outbound managed receive-only client silently drops supported BOD requests

`gateway.go:58–60` and `satline_network.go:79...` advertise BOD protocol support independently of data-serving consent. That distinction is reasonable. But `network_052.go:660–669` returns without responding when `ServeData=false`, whereas the inbound listener sends `unsupported` at `overlay.go:466–470`. The same capability exchange therefore has different behavior depending on which side dialed TCP.

Reproduction `TestAuditBaselineServingDisabledDropsManagedRequest` constructs a managed negotiated BOD session with serving disabled and proves the handler emits no reply. With current request behavior a reverse capability/status request would eventually kill the requesting side's socket. This will become especially visible when NF2 is repaired; fixing adoption alone is insufficient.

Smallest safe repair: unify request dispatch and send an explicit bounded capability-unavailable/unsupported response when serving is disabled. Retain protocol membership and serving consent separately. Do not solve this by enabling serving automatically.

### NF5 — P2: header sync selects receive-only Gateway members as Bitcoin header providers

`network_051.go:640–658` selects any non-scout for `headerSession`, with no header-serving eligibility. `waitSession(ctx, false)` has the same broad filter at 523–541. `retainGatewaySessionsLocked` deliberately makes a compatible non-serving Gateway member non-scout (`discovery_quality_054.go:94–125`). The next header sync sends getheaders (`headers_051.go:119–139`), which a receive-only member ignores (`network_052.go:660–669`, or inbound `overlay.go:407–413`). The request deadline then causes NF1.

Reproduction `TestAuditBaselineHeaderSelectionIncludesReceiveOnlyGateway` shows a retained Gateway member advertising zero Bitcoin services is chosen as a header session. The selector defect is directly tested; the end-to-end 25-second timeout is inferred from those exact call sites, not separately waited through.

Smallest safe repair: request-specific eligible provider selection. Ordinary Bitcoin header transport and Gateway membership may share physical sessions when the peer can fulfill both roles, but membership alone is not header capability. A header readiness policy should permit legitimate limited/pruned header providers; blindly requiring archival `NODE_NETWORK` everywhere would overcorrect.

### NF6 — P1: Windows rejects header rollback truncation, and the error is discarded

The existing `Test051HeaderBadBatchRollsBack` (`standalone_051_test.go:464–482`) fails both in the full native Windows suite and in an isolated `-vet=off -run '^Test051HeaderBadBatchRollsBack$' -count=1 -v .` run. It supplies valid Bitcoin block 1 followed by the same block-1 header as height 2. The second header correctly fails linkage validation, but the first remains in the selected file.

Cause: `appendCheckedHeaders` opens the file with `os.O_WRONLY|os.O_APPEND` at `headers_051.go:406`. Its validation/cancellation/write-error path calls `f.Truncate(original*80)` at 419 and ignores that error; the sync-error path does the same at 426. Under the tested Windows Go runtime, append-only access lacks `FILE_WRITE_DATA`, so truncation returns `Access is denied`. The local Go1.27.1 `syscall/syscall_windows.go:385–393` explicitly removes `GENERIC_WRITE` for `O_APPEND` and substitutes append/attribute rights, corroborating the runtime experiment.

`TestAuditWindowsHeaderRollbackCause` records:

```text
production appendCheckedHeaders: before_bytes=80 after_bytes=160 returned_count=1 batch_error=header does not link to the candidate chain
minimal native handle: O_APPEND=true truncate_error=... Access is denied. final_bytes=160
minimal native handle: O_APPEND=false truncate_error=<nil> final_bytes=80
```

The contrasting minimal case uses `O_WRONLY`, seeks to the end, writes the same 80-byte extension and successfully truncates on the same machine. This isolates the cause from antivirus, symlink permissions, external peer behavior, test ordering and invalid-proof-of-work handling.

Impact: the caller receives an error and the old count while the selected authority file has advanced. `syncIndependentHeaders` closes the peer and returns immediately at `headers_051.go:197–200`, before updating status at 222–240, so in-memory status and persisted selected-header state can disagree. The observed retained prefix was individually valid; this finding does **not** demonstrate acceptance of an invalid header or a consensus bypass. Failed write/sync paths use the same ignored rollback and merit fault-injection coverage.

Smallest safe repair: open an appropriate normal writable handle without `O_APPEND`, seek/check the expected end while holding the existing ownership lock, and check rollback truncation and sync errors. Preserve/propagate both the initial error and any rollback failure. Do not add `O_TRUNC` as a way of acquiring rights: that would erase the existing chain. Alternatively stage a validated batch and commit it with a Windows-tested replacement strategy. Test successful extension, rejected later header, context cancellation after an appended prefix, write failure, sync failure and restart state. No production repair was made in this audit.

## Positive counter-evidence: bounded replenishment already exists

`bitcoinNetwork.run` ticks maintenance every second (`network_051.go:216–240`) and sends managed-session pings every 35 seconds. `maintainDiscovery` plans and persists new attempts (`network_052.go:263–289`); `planDiscoveryLocked` retains dial concurrency, role budgets and global tokens (`discovery_scheduler_053.go:70–171`).

`TestAuditBitcoinPoolAutomaticallyReplenishes` supplies six ordinary controlled peers, sets a three-slot Bitcoin pool, and starts the real `n.run` loop. Closing one selected socket is followed by replacement without Retry. Closing two is followed by two replacements without Retry. For a fast fixture only, the token interval is 20ms; normal one-second maintenance remains in place. This does not prove recovery in every ledger/cooldown/public-topology state, but it disproves a general absence of automatic replenishment.

Recommendation: preserve replenishment. Trace whether each observed live failure is a request deadline, real EOF, cooldown exhaustion, eligibility shortage or ledger failure before altering the scheduler. The brief's reported UI flapping is evidence, not an established scheduler diagnosis.

## Late-response safety: a non-negotiable repair constraint

The current helper serializes requests using `slot` and drains `replies` before sending (`network_051.go:436–465`). Draining only removes responses already queued. A response arriving *after* the drain is not discarded by that mechanism.

* BOD matches command, ID, kind and type inside its acceptance predicate (`network_051.go:490–492`). Its unique IDs provide a useful starting point for safe late-response discard and eventually bounded concurrent requests.
* Satline accepts any satmsg (`network_051.go:761`) and checks ID only after request completion (769–770). With timeout-preserved sessions, a late previous response can terminate the next request with an envelope mismatch. Match the ID during dispatch.
* Bitcoin getdata requests accept any block or notfound (`network_051.go:574`, 705; `network_052.go:492`). A late old block would be treated as the new request's response and then as a wrong-block protocol offense. Parse and match the requested object hash; match notfound inventory entries too. Current identity verification prevents silent acceptance of wrong bytes, but it does not prevent false failure/disconnection after a naïve timeout change.
* Bitcoin headers have no request ID and are accepted solely by command (`headers_051.go:139`). Response validation verifies locator linkage afterwards, but an empty late response cannot be correlated by content. A safe repair needs a header-lane state model: quarantine a timed-out exchange, drain its eventual response before reusing that lane, or stop using that lane/session for new header requests and obtain headers elsewhere while retaining the Gateway socket. A ping barrier alone is not a protocol guarantee that all earlier expensive responses precede pong.

The safe minimum is not a generic complicated multiplexer. It is one transport owner plus explicit routing for IDs/object hashes and a deliberately serialized/quarantined header lane. Test late responses arriving both before and after the next request starts, empty headers, mixed BOD + getdata, cancellation while queued and simultaneous requests in both directions.

## Serving path and resource limits

Existing strengths: pre-allocation command-specific caps for addr/addrv2, feature, gwmsg, bodmsg and satmsg (`p2p.go:534–555`); strict CompactSize parsing for getdata; a four-message managed service queue; maximum 64 accepted inbound sockets; bounded BIP434 handshake time; Bitcoin block identity/Merkle/witness checks; late feature messages rejected; published-Satline and private-cache controls. These are worth preserving.

Additional source-confirmed concerns, not load-tested:

* The handshake still calls generic `readMessage` with a 32 MiB allocation ceiling (`p2p.go:23`, 533–548; `overlay.go:285`). Version, ping/pong and unknown commands do not receive tighter caps before allocation. Sixty-four unauthenticated sockets can declare large payloads and cause a theoretical 2 GiB live allocation within the 12-second handshake windows. Add handshake/state-aware frame limits and a global inbound byte budget. Do not infer safety from BOD's 64KiB cap alone.
* Remote BOD `address_utxos` invokes Core `scantxoutset start` directly (`overlay.go:690–695` to `core.go:418–445`) under the broad data-serving switch. This is potentially expensive full-chainstate scanning exposed as a cheap request; lifetime connection quotas do not meaningfully limit it. Require a separate expensive-work budget/permission, shared concurrency limits and a cancellable provider boundary, or stop advertising that capability until an indexed/budgeted implementation exists. The actual Core resource impact was not benchmarked.
* `serveMessage` holds the session `writeMu` around provider work as well as response writing (`network_052.go:670–697`). Meanwhile `readLoop` answers ping synchronously through `send` (`network_051.go:386–389`), taking the same lock. Slow provider work can therefore block the reader from processing unrelated outbound responses. Move provider work outside the frame-write critical section and retain one bounded writer. This call-path contention is source-confirmed; a stall/load fixture has not been run.

The current advertisement code does **not** indiscriminately claim archival service. `localBitcoinAdvertisement` adds `NODE_NETWORK` only when `ServeData` and fresh `currentBitcoinServing().Archival` are true (`bitcoin_discovery_serving_052.go:157–172`). Archival readiness verifies mainnet, unpruned/synced Core and Core's NETWORK/WITNESS flags. `NODE_WITNESS` is offered for enabled sparse serving, which reflects ability to preserve witness on available blocks rather than complete archival coverage. This is a useful existing distinction; do not require nodes to lie about Bitcoin service to gain discovery eligibility.

## Membership versus useful BOD discovery

`retainGatewaySessionsLocked` already retains up to `GatewaySlots` compatible members, promoting useful providers and keeping limited members alive. However `seek` is controlled by `usefulCountLocked() < GatewaySlots` (`network_052.go:155–160`, 208–209). A fully connected membership set with no blockloc coverage therefore continues broad Bitcoin exploration. This is structurally biased toward BOD provider discovery rather than general Gateway peerhood. It can be an intentional BOD policy, but should not be the generic core network's sole stopping condition.

Historically negotiated endpoints enter the same specific-hint queue family as unverified hints (`discovery_scheduler_053.go:11–15`), with first tries and due retries distinguished, rather than a clear bounded member-reconnect priority. Global caps remain: `planDiscoveryLocked` limits total sessions+dials to Bitcoin+Gateway+Scout capacity at line 96. Candidate-role counting does not count existing gateway_member/service roles as candidates (79–90, 103), allowing bounded ongoing exploration despite a full membership set. This is not an unbounded socket leak. The architectural repair is explicit separate budgets for Bitcoin transport, retained Gateway membership and demand-driven capability search, not simply switching off discovery at the first member.

## Smallest safe delivery plan

1. Preserve this baseline and turn the defect reproductions into real acceptance tests that require the desired behavior. Existing green tests do not cover the five failure modes above.
2. Repair request/transport separation together with response correlation and the special header lane. Preserve independent transport death handling.
3. Adopt inbound sessions once after hello. Unify supported-versus-serving behavior, counters, session ownership and shutdown across directions. Make header/block/BOD selection capability-specific.
4. Replace lifetime request quotas with bounded reusable budgets. Avoid provider work while holding the write lock.
5. Add a user-triggered BOD-only location diagnostic routed exclusively through a selected current session. Record session ID/direction, negotiated versions, request ID/target, duration, returned locator and anchoring status. If optional bytes use Bitcoin getdata over the same negotiated socket, say so explicitly: BOD provided location; Bitcoin wire carried bytes. No local/Core/HTTP/ordinary-peer fallback should satisfy the diagnostic.
6. Re-run focused tests and existing discovery/privacy/integrity/cleanup tests, then race tests where a suitable toolchain exists. Run two-way simultaneous lookups, >64 legitimate BOD requests, disabled-serving reverse requests, late replies, ping cycles, member retention without data, and loss/replacement of one then two peers.
7. Hold the test-candidate label until both real Windows machines retain the same physical negotiated session across several maintenance and health cycles and each can execute the deliberate diagnostic. Public Bitcoin-only discovery remains a separate acceptance experiment.

No production implementation is included in this audit.

## Packaged reproduction runner

The scratch overlay paths in the original research command were local work files. From the repository root, run `python docs/audits/2026-09-26/run-reproductions.py --go <path-to-go>` to generate a temporary overlay for the packaged fixtures. See [verification.md](verification.md) for the complete environment and limitations.
