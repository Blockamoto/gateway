# Gateway audit repair candidate

Version: **0.5.4-repair.1**. This repairs known implementation defects found in the v0.5.4 audit before the proposed one-target Bitcoin-only discovery trial. It is a prerelease, with public propagation and native installer acceptance still to be measured.

## Repairs

- **Scout visits:** unsolicited singleton `addr`/`addrv2` messages no longer end an ordinary scout before its delayed address sample arrives. A multi-record message permits early completion; empty/singleton traffic keeps the original bounded probe deadline. Active requests retain their slot. Bitcoin address messages have no request ID, so this is a conservative completion heuristic.
- **Announcement retry:** an in-flight reservation prevents duplicate concurrent writes, and the long cooldown begins only after a successful socket write. Failure releases the reservation without destroying an earlier successful cooldown. A completed write does not prove admission or propagation by the receiving Bitcoin peer.
- **Connection reuse:** cancellation releases the request slot without closing a healthy socket. BOD and Satline replies are matched to request IDs; block replies and `notfound` are matched to the requested inventory. A timed-out header request disables further header requests on that socket because Bitcoin headers have no correlation ID. Other correlated work remains available. Replacement header transports can be obtained even when all ordinary slots have become unavailable.
- **Inbound peerhood:** after Bitcoin, BIP 434 and Gateway hello negotiation, the accepted socket joins the same managed reader, writer and request machinery. It is usable in both directions. Its ephemeral source port is never added as a dialable discovery address. Inbound sessions do not consume outbound discovery budgets or become scouts.
- **Long-lived service:** renewable, bounded per-session request windows replace lifetime BOD/Satline request cutoffs. Busy and disabled service receive explicit bounded responses. Gateway control negotiation remains available in receive-only mode. Disabling outbound networking preserves incoming serving connections.
- **Known-peer recovery:** reaching the discovery-history intake limit does not prevent reconnecting to an already verified Gateway. Recovery obeys pause, persistence failures, cooldowns, diversity, tokens and connection limits; it does not depend on that member having supplied BOD data.
- **Provider loss:** an existing member remains connected when its BOD service disappears. One supplemental provider can use an existing scout slot. A genuine loss of useful service can make one gracefully retired standby eligible again, but does not erase failure or negative-negotiation cooldowns. Default limits leave another scout slot available; a custom single-scout configuration uses that slot while the provider replacement is needed.
- **Automatic endpoint freshness:** when serving needs automatic public-IP observations and ordinary discovery is satisfied, bounded probes obtain real new Bitcoin handshake observations before old evidence expires. They share existing budgets, run at most once per minute, and do not replace healthy Gateway sockets. Pause, explicit endpoints, existing Core endpoint evidence and history limits still apply. Stale or conflicting evidence can still withdraw an endpoint rather than pretend reachability.
- **Windows header rollback:** checked batches use a writable, seekable handle rather than append-only access. Size, short writes, cancellation, truncate and sync failures are checked. A synced pending marker records the previous count and tip before append; interrupted or failed batches recover to that boundary before startup, selected-header reads or Core-storage anchoring can use them. Unrecoverable or inconsistent boundaries fail closed. Raw header records are unchanged. This is not a guarantee against all filesystem or hardware failure modes.
- **Public request bounds:** pre-negotiation frames are capped before allocation; post-handshake frames use the Bitcoin mainnet protocol ceiling with tighter command limits. Short writes are failures. Remote address-wide Core UTXO scans are explicitly unsupported; local lookup remains available.
- **Public graph provenance and integrity:** private or unclassified downloaded blocks do not enter the public graph. Missing, truncated or corrupt queried shards cannot establish an unspent result. Native BOD v1 graph service advertises positive knowledge only. Local module manifests are not advertised as active remote capabilities without runtime providers.
- **Local Ord content:** wildcard CORS is removed. Exact loopback Host/origin checks, per-inscription preview grants and the Ord enable switch gate access before cache lookup. Cached recursion stays on a separate content origin with no management routes. Intended HTTP request patterns are tested; browser-engine acceptance is not. Browsers omitting both Origin and Fetch Metadata on recursive requests fail closed.

## Data compatibility

The public graph uses **schema 2** in `graph/public-v2`, with checksummed per-shard length receipts and a graph-schema tag on coverage. Legacy graph files are preserved and are not automatically published: rebuild the new public graph from explicitly public sources. Private cached blocks remain available locally. Public transaction-location coverage excludes private materialization. Receipts detect accidental corruption; they do not authenticate data against a hostile local process able to rewrite both files and receipts.

Bitcoin, Gateway, BOD and Satline wire versions, raw blocks, header records, and ordinary index/cache schemas are unchanged. The application, diagnostics and installer identify the new repair candidate; the unchanged Browser Companion keeps its own numeric version.

## Validation

The [machine-readable test record](audits/2026-09-26/discovery-repair-tests.json) records the final commands, results and environment limits. Focused regression tests cover the original reproductions, delayed replies, timeout recovery, inbound full-duplex use, renewable service budgets, rollback and restart recovery, bounded wire reads, retained-peer scheduling and public-data integrity. The combined run additionally exposed a timing-dependent spare-provider retirement and missing Core-source classification during migration; both received targeted repairs and regression checks before release. Controlled loopback discovery is distinct from public-network propagation.

The source audit's Core cache description was corrected: multiple inbound clients reaching the same network-class/local-listener cache key may share one address response. It is not a fresh sample for each requester network group.

## Trial interpretation and remaining acceptance

Use this candidate on both Gateway machines. On the Core-backed publisher, use the normal launcher with a separate test profile and configure the actual Core provider and reachable Gateway listener. Core RPC, Core P2P and the Gateway listener are separate endpoints. The standalone QA launchers are for the receiving machine: first-run settings disable Core and serving.

The seeker must start without the target address in manual settings, recovered history or LAN discovery. Record the source and time of the first Bitcoin-learned hint, its service flags, the attempted connection and completed BIP 434/Gateway negotiation. Keep a separate reachability check out of the seeker's discovery inputs.

The default eight-second scout window remains bounded but has not been established as sufficient for every stock-Core reply timing. Core can decline an address, omit it from a bounded cached sample, or lack any route to it. Gateway cannot make ordinary Bitcoin peers provide a deterministic rendezvous guarantee. Repeated cold-start trials must measure time to discovery and failures; one successful run proves a route, not universal reliability.

The installer is unsigned. Native installation, two-machine interoperability, prolonged real-network operation and public propagation require their own acceptance evidence. The broader application/provider/index architecture remains audit guidance. This repair does not claim every architectural proposal is implemented or that all possible bugs have been eliminated.

## Running the peer-to-peer trial yourself

1. Run the new build on both machines with separate test profiles. On the publisher, confirm Core readiness, serving enabled, and the intended Gateway listener/advertised endpoint in Discovery.
2. On the seeker, use a fresh standalone profile, turn off LAN discovery, and leave manual Gateway addresses empty. Do not enter the target into any add-peer control. Record the start time.
3. Let discovery run locally without an assistant session. Watch for Bitcoin-learned hints, then direct Gateway negotiation; a bit-25 hint alone is not success. Note time to first negotiated connection and whether it stays usable.
4. Export diagnostics and the discovery ledger locally after the run. Review them before sharing. Repeat a cold start and record failed attempts as well as successes. If help is needed, return with the outcome and relevant diagnostic excerpts rather than paying for continuous agent monitoring.

With both laptops on one LAN, public-address hairpin routing may affect the outcome even with LAN discovery disabled. A later test from a separate network is needed to establish external reachability. One successful trial is evidence of a working path, not a guarantee of future discovery through arbitrary Bitcoin peers.
