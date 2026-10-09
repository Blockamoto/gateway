# Known issues and limits

Gateway 0.7.1 is a public testing release. See [status](STATUS.md) for completed checks and hosted delivery acceptance.

- **Two copies can compete for Bitcoin serving port 48333 in 0.7.1.** Close the other copy and retry serving in Settings. Source changes after this release choose an available port when the default is busy, report the actual port in Settings and Peers, and preserve your serving preference. These changes are not yet in the published 0.7.1 downloads. An explicitly configured listen address still requires that address to be available.
- **Windows binaries are unsigned.** Reputation and antivirus results can vary by product and definitions. If blocked, record the file name, SHA-256 and exact alert and stop that check. Do not disable protection to complete testing. A clean local scan or a verdict about another binary does not clear this release.
- **Browser setup is manual and Windows-specific.** The companion is loaded unpacked and must be approved in the profile you use. External apps cannot reliably open Chromium's internal Extensions page directly, so setup provides a copy-and-open flow.
- **Bare addresses can become searches.** Search-engine recovery depends on the browser/search page. A provider may see the query first. Compare with explicit `http://0.bitcoin/` and the companion popup. HTTPS-only behavior or another service using local ports 53/80 can interfere with routing.
- **Headers are not block bodies.** A header snapshot can be behind the network tip. Peers may not provide historical blocks; missing data is not proof of nonexistence.
- **Sparse lookup needs context.** A transaction ID alone may require a containing block. Address history, global spender/sat lookup and other extra-index features are locked.
- **Validation has a defined boundary.** Gateway checks block identity and commitments but does not replace Core's full script/UTXO validation. It is not a wallet, ownership oracle or anonymity service.
- **Live indexing can do substantial work.** The saved/default start may be genesis. Review a small bounded range before enabling Live.
- **Hosted delivery depends on valid metadata and availability.** Network failures must remain visible. Renewal and release-specific live acceptance are tracked in [status](STATUS.md); they are not established by a local build.
- **Source changes can wait for staged work.** An installer-selected publisher change defers while an earlier authenticated stage/recovery exists. This prevents applying a package under a different authority; review the state in Settings.

Use the [timeline tester guide](TESTING-0.7.1.md) and [setup/browser QA checklist](TESTING-0.7.0.md) to report reproducible problems with a version, platform and precise steps.
