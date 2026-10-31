# Authenticated inscription identity points

Ordered table recipes can declare a `bitcoin.inscription_identity` source with
provider `selected`, key `inscription_ref` and type `inscription_identity`.
The installed binding pins the mainnet verifier, inscription parser and reference
profile. The key is a canonical positional inscription coordinate at or before
the current source block, with bounded transaction/envelope indices.

A complete authenticated reveal inventory returns a typed object containing
`ref`, conventional `id`, `txid`, selected `block`, `height`, `tx_index` and
transaction-wide `reveal_index`. A coordinate absent from that complete inventory
returns typed missing. Unavailable bytes remain unavailable, rather than proving
absence. Hash coordinates, noncanonical spellings and future positions are
rejected. This establishes identity, separately from ownership or parent
consumption/provenance.

The current independently owned source is reused; historical reads use the
read-only base acquisition path. One admitted inventory is indexed for repeated
point reads, with only the latest inventory retained. No Inscriptions, Raw,
locator, Coin or Sat writer is started. Each used identity or known absence
becomes a child-owned fact. Identity facts include the selected source-block
anchor even when their value is missing. Source headers are rechecked under
the child's final publication fence. Maintenance validates the fact's binding,
coordinate, value and evidence anchor before cleanup.

Block-context facts retain their existing encoding and bindings. Identity
evidence is an optional field used by the new source kind; old source and
checkpoint fact bytes do not change. These remain authenticated local facts,
not detached consensus proofs.

The guided editor adds installed identity bindings and supplies the current
reveal reference as a read key in reveal mode. Transaction-event mode constructs
the first envelope reference for that transaction, which can be edited. Nested
alias references, read-only targets and removal protection use the same model.
Declarations and edits start no work.

Real block 792435 checks compare every admitted identity with its parser
inventory, exercise known missing/future/malformed coordinates and historical
empty genesis inventory, and verify source-file preservation. Public saved
execution retains both an identity and an anchored absence, passes maintenance,
and returns the original conventional ID through the table query. The wider
point/import/District/catalogue/reveal suite passed 23 checks in 30.191 seconds; the audit
is `gateway-080-identity-points-regression.jsonl`.
The final point-source checks passed in 6.576 seconds after the coalesced
read-only callback guard. Guided unit and headless real-DOM checks passed.
The production build passed (25,309,696 bytes).

Validated Parcel relationship proof sources, actual Parcel JSON and full visible
preview acceptance remain open.
