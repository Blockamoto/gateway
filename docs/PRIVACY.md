# Privacy and network behavior

Gateway retrieves public Bitcoin data, but the blocks you request and the sources you contact can reveal interests or activity.

## Bitcoin connections

Direct Bitcoin peers see your network IP address and protocol requests. Header synchronization, block retrieval and serving use ordinary Bitcoin connections. Peer aliases only conceal addresses on screen until you reveal them; they do not anonymize the network connection. Expanded diagnostics and logs may contain addresses.

Bitcoin serving is enabled for new profiles and can be disabled independently of outbound networking. For a completely disconnected test, disable both outbound networking and serving. Serving permission alone does not establish Internet reachability, complete history or a fully validating node.

## Local data and private cache

Gateway keeps settings, headers, cached/retained blocks, index records and diagnostic state in the profile shown in Settings. Installed Windows profiles use `%LOCALAPPDATA%\Gateway\data`. Optional Core credentials and update-source credentials are sensitive local configuration; do not upload a profile as a bug report.

Private cache marks newly cached objects private. Turning it off does not declassify older objects. Eligible public cached blocks may be served only when their relevant serving permissions are enabled. Missing or private cache objects are not fetched on an inbound requester's behalf. A separately enabled ready Core provider may serve its own public copy of the same chain block without changing a private object's classification.

Optional mounted Core/block files are read-only sources. Back up profiles with the app closed, and review an uninstaller's data choices before removal.

## Browser access

Windows browser routing and browser-companion approval are optional. The companion uses a narrowly registered native host for the approved extension identity and must complete a real exchange. Approval is per browser profile; it does not change your default search provider.

A browser may submit bare `.bitcoin` text to its search provider before the companion recognizes and redirects it. Use the companion popup or explicit local HTTP entry when avoiding that search fallback matters. Local routing is not anonymous DNS or an encrypted tunnel. Other pages should not be treated as trusted merely because they can link to a local resource.

## Inscription content

Inscriptions are untrusted content. The viewport uses a dedicated local content origin with sandbox and network restrictions, separate from Gateway's settings and management API. Viewing or resolving a referenced inscription can request another Bitcoin block when its location is known. An unresolved reference is not silently sent to a public web explorer.

In the first inscription stage, Gateway peer lookup and related transaction locator construction are gated. The later peer stage requires explicit networking and publication permissions; private locator knowledge must not be served. Lean inscription indexing records positions rather than keeping every content body. On-demand content caching is separate from committed index coverage. That cache is limited to 256 MiB of content and 4,096 records; older on-demand writes are evicted when needed. Committed Full index records and retained Bitcoin sources are not deleted by this cache policy.

An intended viewport receives a short-lived, read-only capability for bounded inscription dependency requests. Content can see that viewer capability, but it does not receive Gateway's management credential. The content origin restricts outgoing requests, and same-origin session recovery never sends the capability to an external destination. Closing or changing the viewer cancels its outstanding dependency work.

## Hosted updates and reports

The hosted update service and its infrastructure can observe your IP address, request timing and requested metadata/artifacts, as with other HTTPS downloads. Gateway checks its configured publisher; a custom publisher has its own operational practices. Publisher credentials and private signing keys are not required for ordinary public-client use.

When reporting bugs, redact tokens, passwords, personal paths and addresses you do not want to share. Share the smallest useful log excerpt. See [security reporting](../SECURITY.md) for sensitive issues.
