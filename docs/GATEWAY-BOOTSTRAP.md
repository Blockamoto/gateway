# Gateway entrances and discovery

Configure up to 16 Gateway entrances in **Discovery → Configured Gateway peers**, one per line. An entrance can be an ordinary DNS hostname, IPv4 address or IPv6 address. Omit the port to use Gateway's default **48333**, or specify the remote Gateway listener's port. This setting does not configure Bitcoin Core RPC or change ordinary Bitcoin peers' default port, 8333.

There are no bundled Gateway hosts. A configured address is an entrance, not an authority or evidence of Gateway compatibility. The existing Bitcoin handshake, Gateway negotiation and protocol checks still apply. Domain names are resolved by the operating system on each actual dial; the ledger preserves the configured name and port rather than inventing a permanent resolved-IP identity. Configured aliases can resolve to the same server and do not prove independent operators or independent network routes.

Examples deliberately use no project-operated endpoint. Supply peers you actually intend to contact. Private/LAN endpoints are permitted when explicitly configured; received gossip still cannot authorize arbitrary private-network scanning. This feature supports ordinary TCP/IP DNS resolution, not Tor/I2P transport configuration.

## Configuration and compatibility

The settings field is `gateway_bootstrap_peers`, an array of canonical endpoints. It is copied into the local settings/status response. Existing `manual_peer` settings and the existing single `-gateway-peer` argument remain supported; a bare Gateway hostname now consistently receives port 48333. There is no new CLI flag in this change.

The existing Peers **Add manual Gateway** action appends instead of replacing a prior entrance. Editing the list migrates any legacy manual entry into the list. Replacing or clearing the configured list does not destroy discovery history, ban previously learned peers, disconnect a healthy session, or reset retry deadlines. Previously verified peers remain eligible for ordinary remembered-peer recovery.

Local API clients can replace the list with:

```json
{"action":"bootstrap_peers","peers":[]}
```

POST this object to `/api/v1/discovery`; an empty array clears configured entrances. To add one entrance through the existing API, POST `{"action":"add","address":"<your endpoint>"}` to `/api/v1/peers`. These endpoints retain the application's existing local API protection. Invalid lists are rejected before changing live settings. Older settings forms that omit the new field preserve it.

## Discovery policy

The existing connection manager remains the only managed P2P stack. Configured entrances, remembered peers and Bitcoin-derived hints all use its ledger and budgets:

- Previously verified Gateway peers retain their existing prioritized reconnect turns.
- Configured entrances can take alternating candidate turns after remembered peers. Other candidate turns retain the specific/weak Bitcoin-hint queues. The setting does not create extra sockets or a separate dial loop.
- A failed unverified endpoint uses the existing exponential delay from 30 seconds to one hour; previously verified members use 10 seconds to ten minutes. Both add deterministic jitter of up to 20 percent. Direct-negative Gateway deadlines remain independent and are not erased by configuration or Retry.
- The default limits remain three Bitcoin transport slots, two Gateway member slots, two scout slots, at most three concurrent dials, and one new attempt per five seconds with an initial burst of three. Dial and handshake timeouts remain bounded.
- Once the existing useful-BOD-provider target is met, normal Bitcoin scouting steps back. Stable transport/member sessions remain. Missing capabilities or lost useful providers automatically re-enable wider seeking. A negotiated Gateway without BOD remains a member; it is not silently promoted to a useful Bitcoin data provider.
- Bitcoin transport acquisition, ordinary Bitcoin DNS fallback and the bounded public-IP observation refresh remain available under their existing policies. Explicit networking/discovery pause controls, ledger errors, history limits, global capacities and cooldowns still apply.

At the history limit, recovery can reuse already recorded verified peers under existing bounds. Adding a new configured endpoint does not bypass the requirement to record attempts durably before opening a socket. Increase the history budget if new entries cannot be retained.

`/api/v1/network` exposes `configured_gateway_peers` and `discovery_policy` for the local UI. The latter reports the useful-provider target and whether normal scouting is active; it is telemetry, not a new network capability or a claim of global reachability.

## Probe views

Discovery opens with a simple explanation, current connection/provider counts, controls and the configured entrance list. **Show forensic details** reveals the existing ledger, per-candidate evidence, introduction routes, service masks, direct negotiation results, cooldowns, announcements, event history and diagnostic exports. Collapsing it does not discard evidence. The list editor preserves unsaved text during status refreshes.

Configured and Bitcoin-derived paths remain distinguishable in the ledger. A successful loopback fixture, a configured bootstrap connection and a Bitcoin-derived public connection are different kinds of evidence; none should be relabeled as another.

## Verification

The focused tests cover normalization and rejection of malformed hosts, multiple entrance persistence, append versus replacement, legacy migration, private-gossip admission only after explicit configuration, preserved direct-negative/failure deadlines, remembered-peer priority, continued Bitcoin-hint opportunities and automatic return to scouting after a useful service is lost.

A real loopback test starts a Gateway listener, configures a failed local entrance followed by a `localhost` DNS entrance, and verifies that the second endpoint negotiates Gateway while the first retains its failure/backoff record. Related existing recovery, history-limit, membership and public-IP-refresh regressions are also exercised. These are local tests; they do not establish public propagation, public DNS availability, NAT reachability or a guarantee of finding the last Gateway on Bitcoin.
