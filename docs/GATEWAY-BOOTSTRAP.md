# Gateway entrances and discovery

Configure up to 16 Gateway entrances in **Connections → configured Gateway peers** (also available in advanced Probe), one per line. An entrance can be an ordinary DNS hostname, IPv4 address or IPv6 address. Omit the port to use Gateway's default **48333**, or specify the remote Gateway listener's port. This setting does not configure Bitcoin Core RPC or change ordinary Bitcoin peers' default port, 8333.

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

The existing connection manager remains the only managed P2P stack. Configured entrances and remembered peers use its ledger and budgets. Bitcoin-derived hint seeking is now an explicitly opted-in experiment:

- Previously verified Gateway peers retain their existing prioritized reconnect turns.
- Configured entrances take candidate turns after remembered peers. Only while the experiment is enabled do other candidate turns follow the specific/weak Bitcoin-hint queues. The setting does not create extra sockets or a separate dial loop.
- A failed unverified endpoint uses the existing exponential delay from 30 seconds to one hour; previously verified members use 10 seconds to ten minutes. Both add deterministic jitter of up to 20 percent. Direct-negative Gateway deadlines remain independent and are not erased by configuration or Retry.
- The default limits remain three Bitcoin transport slots, two Gateway member slots, two scout slots, at most three concurrent dials, and one new attempt per five seconds with an initial burst of three. Dial and handshake timeouts remain bounded.
- Once the existing useful-BOD-provider target is met, permitted seeking steps back. Stable transport/member sessions remain. Missing capabilities or lost useful providers automatically resume permitted routes; this never enables the Bitcoin experiment. A negotiated Gateway without BOD remains a member; it is not silently promoted to a useful Bitcoin data provider.
- Bitcoin transport acquisition and ordinary Bitcoin DNS fallback remain available independently of the experiment and discovery pause. Bounded extra public-IP probes and address announcements require the experiment. Explicit networking/discovery pause controls, ledger errors, history limits, global capacities and cooldowns still apply.

At the history limit, recovery can reuse already recorded verified peers under existing bounds. Adding a new configured endpoint does not bypass the requirement to record attempts durably before opening a socket. Increase the history budget if new entries cannot be retained.

`/api/v1/network` exposes `configured_gateway_peers` and `discovery_policy` for the local UI. The latter reports the useful-provider target and whether normal scouting is active; it is telemetry, not a new network capability or a claim of global reachability.

## Explicit Bitcoin experiment opt-in

`bitcoin_gateway_discovery` is false for new installs and for old settings that lack the field. Prior `discovery_paused:false`, serving settings, LAN settings and saved histories are not consent. The advanced **Probe** exposes **Enable Bitcoin experiment** and **Stop Bitcoin experiment**. Local API clients use POST `/api/v1/discovery` with `{"action":"bitcoin_probe_start"}` or `{"action":"bitcoin_probe_stop"}`. Start also resumes permitted seeking; it does not override offline mode. A normal settings save preserves consent; legacy Resume does not opt in.

When off, the scheduler does not open Bitcoin scout visits or chase unverified Bitcoin-derived Gateway hints. Ordinary Bitcoin connections still acquire addresses for Bitcoin transport and fetch headers/blocks, but do not offer Gateway feature negotiation. Configured/manual Gateway endpoints, remembered verified Gateway peers and explicitly enabled LAN introductions can still negotiate Gateway. Incoming opt-in serving stays independent. Stopping cancels pending experimental dials, retires idle unverified scouts, and prevents new experimental announcements; already useful sessions and normal Bitcoin data requests are preserved. In-flight socket writes may already have completed. No known project seed endpoint is bundled.

The Probe presents detailed ledger, candidate history, announcement evidence and exports on demand. Its totals include prior and other-route activity and are **not a fresh trial**. To prove Bitcoin-only first contact, use a separate empty seeker profile with no target address or cached Gateway introductions, record a Bitcoin-derived introduction, verified Gateway negotiation and a useful request. Controlled fixtures, LAN tests, configured-peer tests and public trials must be distinguished. Block retrieval from ordinary Bitcoin peers is not proof of Gateway discovery.

`/api/v1/network` exposes `bitcoin_gateway_discovery` and `experiment` (`enabled`, `active`, `default`, `fresh_trial`, `note`). `fresh_trial` is false: toggling this app does not wipe history or create an isolated trial. `discovery_policy.normal_gateway_seeking` reports normal permitted seeking; `normal_scouting` now specifically requires experimental consent.

## Verification

The focused tests cover normalization and rejection of malformed hosts, multiple entrance persistence, append versus replacement, legacy migration, private-gossip admission only after explicit configuration, preserved direct-negative/failure deadlines, remembered-peer priority, continued Bitcoin-hint opportunities and automatic return to scouting after a useful service is lost.

A real loopback test starts a Gateway listener, configures a failed local entrance followed by a `localhost` DNS entrance, and verifies that the second endpoint negotiates Gateway while the first retains its failure/backoff record. Related existing recovery, history-limit, membership and public-IP-refresh regressions are also exercised. These are local tests; they do not establish public propagation, public DNS availability, NAT reachability or a guarantee of finding the last Gateway on Bitcoin.
