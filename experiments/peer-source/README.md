# Experimental acquisition boundary

This package sketches replaceable `Authority`, `Policy`, `Provider`, and
`Verifier` interfaces for acquiring an already-selected block. There is no
networking, Bitcoin wire transport, Bitcoin verifier, persistence, UI, peer
metadata, privacy cache, discovery, coalescing or index fanout implementation.
No source is implicitly enabled or started. Source kinds are supplied by trusted
configuration; providers must not misrepresent their behavior. Core and ordinary
Bitcoin-peer permissions are independent. Tests use only in-memory fakes.

Before each permitted provider, the boundary checks current consent and the
selected header/hash and chain revision. It rejects oversized, short or
wrong-header payloads before asking the trusted injected verifier to check the
complete body. After verification it rechecks chain and policy revisions and
returns a private copied buffer. It provides an optimistic publication recheck;
the caller must coordinate that fence atomically with any durable commit. The
hash is double-SHA256 in Bitcoin internal byte order. This does not verify
Bitcoin transactions, Merkle or witness commitments by itself; trusting a fake
verifier does not validate a Bitcoin block. Authority and policy implementations
must advance revisions when relevant state changes.

A policy snapshot and a later fetch are not atomic. A concurrent permission
revocation may occur just after a check while a provider is already starting a
request. An actual transport must synchronize or cancel outstanding requests
according to current user policy. The post-fetch size check is not a network
allocation limit; the transport must enforce message framing and size limits
before allocation. The current contract checks staleness before returning and
again on explicit publication check, but it does not make either check atomic
with database commit. It does not decode once and share a parsed body, yet.

The relevant current Gateway implementation is `block.go`'s
`fetchBlockWithPolicyContext` and `acquireBlockBaseContext`, followed by
`bitcoinNetwork.fetchTargetBlockContext` in `network_block_download.go` and
`parseBlockDetailed` in `block.go`. Existing local/Core/archive sources precede
peer acquisition; source settings and cancellation are rechecked around
coalesced fetches. The current peer downloader bounds message sizes, queue and
in-flight download memory; the real parser authenticates headers, PoW, Merkle
and witness commitments. Raw retention and opportunistic indexing are separate
policy barriers after acquisition. Full script/UTXO consensus is not implied.
Extracting these components safely requires separating their unexported main
package types and current session/network dependencies, preserving per-source
privacy, retry and lifecycle semantics, and integrating current-chain and
consent fences with transactional publication. Gateway peerhood remains future
scope.

Run `go test ./...` here with Go 1.22 or later. Tests exercise disabled sources,
local fallback, invalid payload/failover, verifier failure, cancellation,
chain/policy changes, publication recheck and defensive byte copies. They use
synthetic fake verification, make no connections, and do not touch profiles.
They do not demonstrate real Bitcoin peer acquisition or a production-ready
adapter.
