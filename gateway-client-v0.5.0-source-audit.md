# Gateway Client v0.5.0: source audit of v0.4.9

Date: 23 September 2026. This is an audit for planning, not a new release or a claim of native Windows testing.

Input: `gateway-client-v0.4.9-source.zip` supplied in the conversation.
SHA-256: `11b1934e2056ca61ca1813a1a20ba229f94a03d1651f50af046c79d13e61803f`

## Findings and confidence

### A. Browser origin navigation loses its boot target
Confirmed by source inspection. `bitcoinProxyHandler` adds `?resolve=<host>` only to the upstream reverse-proxy request. The index handler returns a static HTML constant. Its boot script reads `location.search` in the actual browser, whose address remains `http://0.bitcoin/` with no query. It does not recover the forwarded target from the Host header. Thus this path can display the application without calling the search action. `browserResolveURL` selects this path on Windows when an NRPT rule is present. This is a concrete explanation consistent with the reported symptom, not a native Windows reproduction.

Required change: authenticated activation queue plus validated server-provided boot resource, frontend readiness acknowledgement, and end-to-end rendered-object tests. Do not count an HTTP 200 or a launched process as successful resolution.

### B. Companion control channel depends on the namespace bridge
Confirmed by source inspection. Its service worker posts to `http://gateway.bitcoin/api/v1/browser`. The content script silently leaves the search unchanged when that call fails. The extension cannot itself start Gateway with this implementation. A native-messaging control channel removes this dependency, but still requires browser-approved extension installation and does not turn arbitrary custom protocols into HTTP transports.

### C. Installer opens setup pages; it does not verify companion installation
Confirmed by source inspection. Setup copies companion files, opens Explorer and searches conventional Chrome/Edge/Brave executable paths. It does not await an extension/native handshake in the selected browser profile. Browser-start failures are ignored. A manifest has no fixed public `key`, so development extension identity must be stabilized before native-host origin registration is relied on.

### D. Slow resolution has several concrete sources of avoidable work
Confirmed code paths, but no timing measurements on the user's machine. `fetchAndDecodeTarget` holds a global `fetchMu` over retrieval, decoding, derived indexing and cache writes. `localStorageBlock` calls `inspectCore` before reading Core; this probes P2P and performs blockchain/index RPCs. Status checks also recur elsewhere. `indexVerifiedBlockKnowledge` rewrites the full JSON cache index on reads and invokes graph indexing. Core-backed transaction lookup may fetch transaction metadata, block headers, the block's txid list and then the full block again for verification. These are credible performance bottlenecks. Their relative contribution needs profiler spans and a real Core benchmark.

### E. Satline has fixed job and RPC timeouts
Confirmed by source inspection. A Satline job is created with a ten-minute total deadline. New Core RPC clients use a twelve-second HTTP timeout (status inspection shortens its own client to three seconds). RPC requests are not built with the caller's context. Either job or RPC timeout can appear during lineage resolution. The reported hop-23 error alone does not identify which fired.

Required change: durable job lifecycle, per-operation context propagation, explicit provider timeout vs missing evidence, pause/resume, request-stage timing and cancellable work. Do not merely set a very large global timeout.

### F. Merkle mismatch is recorded but not rejected
Reproduced using the shipped parser and its genesis-block test fixture. The audit changes a transaction byte without modifying the block header. `parseBlockDetailed` returns no error, reports `MerkleRoot=false`, and still sets `VerificationState=header_anchored`. `fetchAndDecodeTarget` then has paths which upgrade a result using Core authority and index derived knowledge without first rejecting this mismatch. No evidence of exploitation or actual user-cache contamination was obtained.

Audit output:

```text
=== RUN   TestAudit050ObserveMerkleMismatch
    audit050_test.go:13: OBSERVED: error=nil, MerkleRoot=false, HeaderChainMatch=true, VerificationState=header_anchored
--- PASS: TestAudit050ObserveMerkleMismatch (0.00s)
PASS
ok  	_/mnt/data/gc050-audit/gateway-client-v0.4.9	0.002s
```

The test is named as an observation of the defect. Its PASS means reproduction succeeded, not that the parser is correct. Baseline source ZIP was not modified.

Required change: reject invalid transaction Merkle roots and mutation cases before caching, indexing, serving or promotion. Add negative tests and a verifier-revision receipt. Treat affected older derived records as needing recheck, not automatically valid or automatically deleted.

### G. Witness evidence requires its own validation
Source inspection found witness parsing and wtxid calculation but no BIP141 witness-commitment validation in the block parser. A block hash or txid proof alone does not authenticate inscription witness bytes. This must be addressed before peer-supplied inscription content is promoted as verified. This is not a request to implement a new full Bitcoin consensus validator.

### H. Raw outpoints are missing from the common search parser
The general search parser supports positional coordinates, addresses, numeric blocks, and 64-character hash queries. Raw satpoint parsing exists in Satline but is not a unified Gateway resource path. This supports adding typed txid:vout and txid:vout:offset handling centrally rather than adding another module-specific parser.

## Source locations

- `satline_session.go:241`: `context.WithTimeout(context.Background(), 10*time.Minute)`
- `core.go:93`: `Timeout: 12 * time.Second`
- `core.go:200`: `func inspectCore`
- `core.go:266`: `func coreTxLocation`
- `block.go:224`: `func (a *app) fetchAndDecodeTarget`
- `block.go:225`: `a.fetchMu.Lock()`
- `block.go:329`: `a.indexVerifiedBlockKnowledge(view)`
- `block.go:418`: `merkleOK :=`
- `cache.go:85`: `func (a *app) saveCacheIndex`
- `cache.go:150`: `func (a *app) indexVerifiedBlockKnowledge`
- `web.go:40`: `w.Write([]byte(indexHTML))`
- `web.go:633`: `const bootResolve=`
- `domainbridge.go:194`: `q.Set("resolve", host)`
- `runtime.go:72`: `nrptRulePresent()`
- `runtime.go:78`: `func forwardToRunningInstance`
- `browser-companion/service-worker.js:4`: `const GATEWAY =`
- `packaging/windows-installer/main.go.txt:258`: `func openBrowserCompanionSetup`

## Scope limitations

No full Bitcoin Core store, user logs, native Windows login/install test, or live Ord differential test was available in this audit. Root causes are separated above into reproduced behavior, inspected implementation, and hypotheses requiring measurement. Previous release test totals are not treated as evidence of end-to-end correctness.
