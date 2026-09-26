# Codex Astra Audit Brief
## Gateway as a Bitcoin-bootstrapped peer discovery layer, capability router, and TRIP index substrate

**Repository:** `Blockamoto/gateway`  
**Branch:** `main`  
**Current main baseline:** `5acabb0ac27c6bcd9786b65a43236dd2962982b8`  
**Current source baseline:** v0.5.4  
**Active repair brief:** `docs/v0.5.4.1-implementation-brief.md`  
**Workflow rule until v1.0:** development is committed directly to `main`. Do not create feature branches or a PR workflow unless explicitly asked.

## 1. Your assignment

Perform a first-principles architecture and implementation audit of the entire Gateway repository before proposing further development.

Do not treat the current architecture as sacred. Preserve the parts that are coherent, but challenge any design that obstructs the underlying goal. The purpose of the audit is not to rubber-stamp the present implementation. It is to determine the strongest practical architecture for Gateway while respecting what has already been learned through live testing.

**Phase A is audit and design only. Do not modify production source during the audit.**

After the audit, produce a concrete implementation plan. Implementation can then be authorized separately and, if authorized, should be committed directly to `main`.

Read the entire repository, with special attention to:

- `docs/v0.5.4.1-implementation-brief.md`
- `GATEWAY-PROTOCOL-v1.txt`
- `spec/gateway-protocol.md`
- `DISCOVERY-LEDGER-v1.md`
- `MODULE-API-v1.txt`
- `BOD-WIRE-v1.txt`
- `SATLINE-WIRE-v1.txt`
- `network_051.go`
- `network_052.go`
- `p2p.go`
- `discovery_scheduler_053.go`
- `discovery_quality_054.go`
- `discovery_advertisement_054.go`
- the discovery, networking, BOD, Satline, setup, cleanup, and compatibility test suites
- all implementation-status and verification documents

Treat the live v0.5.2 through v0.5.4 QA history embedded in the repository as evidence. Distinguish controlled-fixture success, native Windows observations, and unproven public-network behavior.

# 2. The architectural center

Gateway should be understood as a **communications and discovery substrate first**.

It has two foundational connection layers:

1. **Bitcoin peerhood**
   - Gateway first joins ordinary Bitcoin P2P.
   - It obtains Bitcoin connectivity, addresses, headers, and Bitcoin data from ordinary Bitcoin peers according to the capabilities those peers actually provide.

2. **Gateway peerhood**
   - Gateway uses Bitcoin as the entrance to find possible Gateway peers.
   - The current experimental discovery hint is **Bitcoin service bit 25**, not "BIP 25".
   - Bit 25 is only an unauthenticated hint.
   - BIP 434 feature negotiation establishes that the remote peer speaks the Gateway feature family.
   - The Gateway `gwmsg` exchange then declares protocol families and versions directly supported by that peer.

The intended path is:

`Bitcoin bootstrap -> ordinary Bitcoin peers -> Bitcoin address exchange -> possible Gateway hint -> direct TCP connection -> Bitcoin version exchange -> BIP 434 Gateway negotiation -> Gateway protocol listing -> protocol-specific use`

**Bitcoin gets the client to the door. Gateway tells it which compatible peers are inside.**

Above Gateway sit optional peerhoods, protocols, indexes, and applications.

# 3. The most important audit: make Gateway discovery "heat-seeking"

The highest-priority architectural question is whether Gateway can converge on actual Gateway peers through Bitcoin substantially more efficiently and reliably than blind random crawling.

Do not assume the current crawler is optimal.

Audit Bitcoin's actual peer-discovery semantics from first principles and inspect the implementation against them. In particular:

- legacy `addr`
- BIP 155 `addrv2`
- `getaddr` behavior and request limits
- address sampling
- address-manager retention and filtering behavior
- service-bit propagation
- experimental service-bit behavior
- Bitcoin Core's treatment of service flags
- protocol versions
- network-group diversity
- stale addresses
- false or indiscriminate service masks
- the live observation that many gossiped candidates advertised all service bits
- the current Gateway exact discovery ledger
- retry policy
- queue fairness
- connection-slot allocation
- direct evidence versus third-party claims

The current code already distinguishes specific hints from all-bits noise and keeps a durable discovery ledger. Audit whether those mechanisms are correct and sufficient.

## 3.1 The target behavior

A fresh client with no hard-coded Gateway endpoint should:

1. obtain ordinary Bitcoin peers;
2. actively explore Bitcoin's address graph;
3. preserve exactly what it has already explored;
4. identify possible Gateway endpoints;
5. prioritize the strongest available Gateway evidence;
6. directly verify candidates with the Bitcoin handshake and BIP 434;
7. retain compatible Gateway connections;
8. stop wasting most effort on the Bitcoin haystack once useful Gateway peers are established;
9. return to Bitcoin exploration automatically if the Gateway mesh disappears.

The goal is not omniscient discovery of every Bitcoin node. The goal is **preferential convergence toward Gateway-capable peers while remaining bounded, decentralized, and honest about uncertainty**.

## 3.2 Investigate heat-seeking strategies

Evaluate combinations of:

- prioritizing precise bit-25 advertisements over all-bits or ambiguous claims;
- weighting introducers by the quality and novelty of addresses they have supplied;
- tracking new-endpoint yield per source;
- preferentially exploring branches of the Bitcoin address graph that have produced specific Gateway hints;
- maintaining network-group/source diversity so one introducer cannot trap the crawler;
- maintaining durable negative evidence so false hints do not repeatedly consume the search budget;
- separating ordinary Bitcoin transport slots from exploration slots and Gateway-candidate slots;
- preserving known compatible Gateway endpoints across restart without turning them into trust authorities;
- retaining a small healthy Gateway mesh once entered;
- resuming wider Bitcoin crawling only when required;
- using direct peer service bits observed in `version` as stronger evidence than third-party address gossip;
- using Gateway peers, once found, to accelerate discovery of other Gateway peers without replacing Bitcoin as the root fallback.

Do not invent a service-filtered `getaddr` query if Bitcoin does not provide one.

Measure convergence, not just raw address counts. Useful metrics include time/dials to first real Gateway, time/dials to N healthy Gateways, repeated-attempt ratio, new-endpoint yield by introducer, candidate false-positive rate, and recovery time after all Gateway peers disappear.

## 3.3 Audit the first-contact advertisement problem

Determine exactly under what circumstances a Gateway endpoint advertised through Bitcoin address exchange is retained and propagated by unmodified Bitcoin Core peers.

Do not solve propagation by falsely claiming Bitcoin service capabilities that Gateway does not actually provide.

Separate:

- advertisement sent;
- recipient accepted;
- entered addrman;
- relayed;
- learned by another Gateway crawler;
- advertised port externally reachable;
- Gateway successfully negotiated.

If experimental service-bit propagation is intrinsically unreliable in some topology, quantify the limitation and propose the narrowest additional mechanism that preserves Bitcoin as the decentralized root.

Do not default to a hard-coded Gateway bootstrap list.

## 3.4 Once inside Gateway, reconsider peer exchange

Current Gateway v1 advertises only protocol families directly supported by the connected peer and does not gossip third-party capability inventories.

Preserve that boundary unless there is a compelling reason to change it.

Audit whether Gateway needs a separate, explicit **Gateway peer exchange capability** after the first successful Gateway connection. It could share bounded hints about other Gateway endpoints while requiring direct re-verification.

If useful, design it as a distinct protocol/capability rather than silently changing v1 `hello`.

Evaluate TTLs, provenance, spam/Sybil controls, privacy, whether cryptographic peer identity is necessary, and compatibility with future signed manifests.

**Bitcoin is the entrance of last resort. Once a client enters Gateway, Gateway should help it stay inside Gateway.**

# 4. Fix the networking floor before expanding the ceiling

The active v0.5.4.1 brief documents live QA where ordinary Bitcoin transport flapped, Gateway state was asymmetric, inbound negotiated sockets were not reliably usable both ways, and BOD fallback could not find a stable connected BOD peer.

Audit that diagnosis before building higher layers.

The minimum floor is:

- bounded ordinary Bitcoin pool that replenishes automatically;
- request timeout does not unnecessarily destroy a healthy transport;
- inbound and outbound negotiated Gateway connections share one coherent session abstraction;
- negotiation state is symmetric;
- negotiated connections are full duplex;
- protocol capabilities are usable from either side;
- BOD can be deliberately tested without ordinary Bitcoin taking credit;
- closure reasons are explicit;
- Gateway membership and BOD data availability are separate concepts.

If the v0.5.4.1 hypotheses are wrong, replace them with evidence.

# 5. Gateway is a control plane, not automatically every application's data plane

The desired model is:

- an application/module tells local Gateway which protocol family it understands;
- Gateway advertises support when allowed;
- the app asks Gateway to find compatible peers;
- Gateway resolves and maintains the peer relationship;
- the app handles its own semantics.

Do not assume all application traffic must be tunneled through one Gateway socket.

Define the boundary between:

**Discovery/control plane:** peer discovery, capability negotiation, session establishment, peer health, rendezvous, endpoint exchange, protocol/version matching.

**Application data plane:** app messages, high-bandwidth transfers, WebRTC streams, BitTorrent traffic, Nostr events, game state, private app protocols.

Some light protocols may multiplex over Gateway. Others should use Gateway only for discovery/rendezvous and then establish their own transport.

# 6. Vocabulary: transport, protocol, peerhood, capability, index, product

## Transport
How bytes move. Examples: TCP, Bitcoin-framed Gateway connection, Tor, WebRTC data channel.

Tor is primarily a transport, not itself a peerhood.

## Protocol family
A machine-readable language peers can speak. Preserve stable protocol IDs plus separate versions.

## Peerhood
A discoverable population participating in a protocol context. It may be a whole protocol or a narrower realm, swarm, world, game session, Nostr community, or invite-only group.

## Capability
A concrete action/data service: block bytes, transaction location, spender lookup, an index range, WebRTC signaling, chat, game sync.

## Index
Deterministic derived data from declared inputs and rules.

## Product
Human-facing application/service built using protocols and/or indexes.

A product must not silently redefine the index rule set.

# 7. Generalize the current module API into an app/provider contract

The current module API already includes module ID, version, protocol ID/version, required/optional capabilities, CLI namespace, API prefix, storage schema, and `network_advertised`.

Audit whether it can evolve into a generic local registration contract.

A local app could declare:

- stable app/module ID;
- protocol family ID;
- versions;
- optional peerhood/realm selectors;
- required transports;
- required and optional Gateway capabilities;
- local handler/IPC endpoint;
- active state;
- advertisement consent;
- privacy/publication policy;
- data-plane mode: shared Gateway session, rendezvous-only, or external transport.

Conceptual local primitives might include `registerProtocol`, `findPeers`, `watchPeers`, `openSession`, and `getPeerEvidence`. Do not freeze API names before the audit.

**Gateway should be able to connect applications it does not semantically understand.**

# 8. Leave room for Gateway Apps without making the core an app store

Separate:

1. protocol/peer discovery;
2. application distribution and metadata;
3. application trust/signing;
4. human-facing app browsing.

The core must work with no app store.

A future Gateway Apps directory may distribute adapters/modules, but must not become the authority deciding whether a protocol ID exists.

Do not build the marketplace during this audit.

# 9. Index architecture: decouple answerability from permanent storage

Preserve this principle:

> **Gateway should decouple what a node can answer from what a node must permanently store.**

A node may retain full blocks, mount Core data, cache selected blocks, stream blocks from archival Bitcoin peers, obtain data from Gateway/BOD peers, derive an index, discard source blocks, retain only derived state, and serve that index if allowed.

Raw Bitcoin data and derived indexes are separate.

Do not overpromise remote availability. A streamed index build still needs a legitimate way to locate and retrieve required blocks.

# 10. TRIP is the canonical model for indexable systems

Gateway should adopt **TRIP**:

## T - Theory
The human idea/purpose. It explains what the system means. It is not executable consensus.

## R - Rule Set
Hard deterministic machine-repeatable rules. Versioned, explicit, reproducible, testable, and uniquely identifiable. This is the normative computational layer.

## I - Index
Derived output produced by the declared rule set over declared inputs. It has coverage and evidence.

## P - Product
Anything built on the index: explorer, marketplace, game, map, wallet, analytics, metaverse.

Product policy must not leak backward and redefine the rule set.

# 11. Split index definition from index instance

## Index Definition
The reusable recipe:
- ID/name/version;
- theory reference;
- rule-set ID/hash/version;
- required/optional inputs;
- starting conditions;
- transitions;
- checkpoint schema;
- output schema;
- canonical serialization;
- verification/fingerprint scheme;
- range/dependency semantics;
- compatibility profile.

## Index Instance
A node's built state:
- definition/version;
- network;
- covered ranges and gaps;
- starting checkpoint/state;
- provenance;
- snapshot height/hash;
- dependency fingerprints;
- output fingerprints;
- retained state;
- completeness;
- verification status;
- local queryability;
- remote serveability;
- retention policy.

Two peers can implement the same definition at different depths.

# 12. Range indexing is first-class

Where valid, support bounded build, resume, repair, verify, compare, exchange, discard, and rebuild.

Do not assume every index can start arbitrarily.

For stateful indexes, range fingerprints must bind enough context to prevent false equivalence, including definition ID, rule-set identity, network, range, chain anchor, starting-state/checkpoint hash, dependency fingerprints, canonical output encoding, and resulting output fingerprint.

Audit Merkle/hierarchical range structures only after establishing required operations.

# 13. Stateful dependencies must be explicit

Examples:

- transaction locator may be range-local;
- historical address activity can be range-local if it only records events in the range;
- current UTXO state requires prior state/checkpoint;
- spender coverage can require future coverage relative to an output;
- absolute sat tracking requires correct prior sat state;
- Ord may require historical context;
- Bitmap depends on the relevant upstream inscription semantics.

Possible starting requirements include genesis, verified UTXO snapshot, prior checkpoint, sat-state checkpoint, previous range fingerprint, or upstream index instance.

Never convert "we have blocks from H" into "we can reconstruct every stateful index from H."

# 14. Separate current address state from address history

These are different indexes.

**Current address-to-UTXO state:** what remains spendable at a snapshot.

**Historical address activity:** historical outputs/transactions/events involving an address, including later-spent outputs.

Apply the same discipline to transaction locator vs body, spender index vs spend history, sat current location vs lineage, inscription occurrence vs full Ord state, and Bitmap base result vs compatibility/dispute metadata.

# 15. Efficient stacked indexing without entanglement

A decoded block should feed multiple active indexers:

`Bitcoin block -> transaction/event extraction -> optional Bitcoin indexes -> Ord interpretation -> Bitmap interpretation`

Reuse work in memory, but keep each index independently defined, versioned, addressable, queryable, discardable, and rebuildable.

Audit a clean fan-out/event pipeline.

# 16. Peer-served indexes

Peers should eventually advertise index capabilities with enough metadata to understand claims without trusting them.

Possible fields:
- definition/version;
- network;
- coverage ranges;
- snapshot/checkpoint;
- completeness;
- query types;
- fingerprint scheme;
- range roots;
- availability hints;
- request limits;
- evidence types.

A peer may provide direct answers, bounded ranges, checkpoints, fingerprints, dependency records, or locators to underlying Bitcoin data.

Audit whether this belongs in one generic Index protocol or several index-family protocols. Favor composability without building abstract machinery that makes simple indexes painful.

# 17. Bitmap as the proof case

Eventual flow:

1. fresh Gateway user selects Bitmap;
2. resolve declared dependencies;
3. acquire required Bitcoin blocks without requiring permanent archival storage;
4. perform only necessary upstream parsing;
5. derive inscription stream under declared Ord dependency;
6. apply base Bitmap rule set;
7. write Bitmap index;
8. write range/checkpoint fingerprints;
9. optionally discard intermediate Bitcoin bytes;
10. offer compatible optional indexes that are cheap to retain during the same scan.

Do not assume the scan can begin at the first Bitmap-relevant block unless upstream state makes that valid.

## Base rules and compatibility validators

The original/base Bitmap rules should be an explicit versioned rule set.

Historical third-party interpretations must not silently redefine it.

Run namespaced compatibility validators for historical indexer/marketplace/platform interpretations. Candidate disagreement classes include reinscriptions, negative inscription-number treatment, encoding/parser differences, historical exclusions, and other implementation-specific behavior.

A disagreement record should preserve the district/resource, base result, compatibility result, responsible rule, inscriptions, historical provenance, and affected fingerprint/range.

A disagreement is metadata, not mutation.

# 18. Peerhoods and indexes are related but not identical

Not everything above Gateway is an index.

Live peerhoods include chat, multiplayer movement, WebRTC signaling, BitTorrent swarm participation, and Nostr event exchange.

Indexable deterministic data include transaction locators, spenders, Ord inscriptions, and Bitmap.

The common abstraction is **peer capability discovery**, not database.

# 19. Privacy and trust

Audit privacy levels such as:
- local only;
- directly connected Gateway peers;
- public Gateway advertisement;
- explicit invite/rendezvous token;
- outbound-only discovery.

Do not automatically publish query history, discovery encounter history, private index coverage, local filesystem/provider paths, Core RPC credentials, wallet identity, or private peerhood membership.

Audit DoS/Sybil risks for peer adverts, manifests, coverage claims, range requests, third-party hints, app registration, large protocol lists, and realm selectors.

# 20. Scaling Gateway's protocol list

Current `gwmsg` hello is bounded to 16 KiB and lists directly supported protocol families.

Audit future evolution:
- compact hello summary;
- query by protocol ID;
- pagination;
- separate protocol-family support from peerhood-instance discovery;
- avoid global membership dumps;
- maintain wire-v1 compatibility where practical.

Do not replace stable protocol IDs with human names.

Do not use one BIP 434 feature ID per app without a very strong reason. The current single Gateway feature followed by Gateway-level protocol negotiation is intentionally scalable.

# 21. Thought experiments

## Nostr
Gateway may discover relevant peers/relays for a declared context. Decide whether it only rendezvous them or proxies traffic.

## WebRTC
Gateway may provide signaling/rendezvous so peers establish a direct data channel. Heavy game/media traffic need not traverse Gateway.

## BitTorrent
A swarm can be modeled as a peerhood. Gateway can help find Gateway participants while BitTorrent handles data transfer.

## Tor
Model mainly as alternate transport/reachability.

## Gateway Chat
A lightweight chat protocol could use the existing full-duplex Gateway connection, but only after networking stability is proven.

# 22. Compatibility and migration

Audit what remains wire-compatible with Gateway v1, what needs additive fields, what needs a new wire version, what belongs as a new protocol family, how old clients behave, how ledger persistence migrates, how index definitions coexist, and how fingerprints bind to versions.

Favor additive evolution where unambiguous.

# 23. What not to do

Do not:
- reintroduce libp2p merely because Bitcoin discovery is hard;
- hard-code a Gateway server and call it decentralized discovery;
- falsely advertise Bitcoin service flags;
- weaken BIP 434;
- trust bit 25 as identity;
- trust third-party capability claims as proof;
- turn root Gateway into an app-specific message switch;
- force every app's data through Gateway;
- make a central app store authoritative;
- require permanent genesis-to-tip Bitcoin storage for every index;
- assume every range index is stateless;
- merge current address state and historical address activity;
- let marketplace behavior redefine Bitmap base rules;
- build a plugin marketplace before local registration/capability abstractions are sound;
- let architecture work distract from v0.5.4.1 stability.

# 24. Required Astra deliverables

Produce one audit package containing:

## A. Current-state architecture map
Map connection manager, Bitcoin discovery, ledger, Gateway verification, BIP 434, hello negotiation, BOD, Satline, module API, UI, persistence, cleanup, tests. Identify bypasses and duplicate responsibilities.

## B. v0.5.4.1 diagnosis
Confirm or replace the current diagnosis and give the smallest safe repair.

## C. Gateway discovery heat-seeker report
This is the most important research section. Give Bitcoin P2P limits, current inefficiencies, strongest decentralized convergence algorithm, before/after-first-Gateway behavior, defenses against noise/Sybil/stale data, and required simulations/stock-Core tests.

## D. Gateway root protocol assessment
Assess the model:
`one Gateway BIP 434 feature -> Gateway hello -> directly supported protocol families`

State what should remain and what should evolve.

## E. Peerhood/application architecture
Propose the smallest generic model for local app registration, protocol IDs, realm selectors, discovery, session handoff, transport choice, consent/privacy, and future Gateway Apps.

## F. TRIP index architecture
Make Theory -> Rule Set -> Index -> Product concrete. Define definition/instance, dependencies, coverage, checkpoints, ranges, fingerprints, peer-served claims, verification, retention, and fan-out.

## G. Bitmap proof architecture
Describe base rules, Ord dependency, starting state, ranges, compatibility validators, disagreement records, fingerprints, and selective retention. Do not implement Bitmap unless separately authorized.

## H. Security/privacy review
Cover gossip, false bits, BIP 434 limits, protocol-ID copying, peer exchange spam, privacy, poisoning, resource exhaustion, query privacy, and credential isolation.

## I. Proposed target architecture
One coherent component model and diagram.

## J. Staged roadmap
1. networking floor;
2. discovery convergence;
3. generic capability/peerhood API;
4. generic index framework;
5. Bitcoin-native indexes;
6. Ord;
7. Bitmap;
8. external app experiments;
9. Gateway Apps if still justified.

For each stage: files/components, protocol impact, migration risk, tests, rollback boundary, deferred work.

# 25. Required proof tests

1. **Bitcoin-only entrance:** no known Gateway endpoint, discover unknown Gateway through Bitcoin, BIP 434 verify, negotiate and retain session.
2. **Heat-seeking under noise:** large graph with stale peers, all-bits false hints, precise false hints, real Gateways, adversarial introducers. Compare current and proposed scheduler.
3. **Gateway mesh recovery:** remove Gateway peers and verify preferential mesh repair, then Bitcoin fallback only when needed.
4. **Symmetric session:** both sides agree Gateway negotiated and can initiate requests.
5. **App-defined peerhood:** minimal test app registers protocol+realm and finds a peer while Gateway remains payload-opaque.
6. **Discovery-only external transport:** discover through Gateway, establish separate mock data plane.
7. **Deterministic stateless range:** same inputs produce same canonical output/fingerprint.
8. **Stateful range:** incompatible starting checkpoints produce distinguishable results.
9. **Sparse coverage:** ranges A and C but not B are advertised and queried honestly.
10. **Bitmap disagreement:** base and compatibility validator disagree while base result stays stable and disagreement evidence is retained.

# 26. Questions Astra must answer directly

1. Is service-bit-25 discovery through Bitcoin viable as Gateway's decentralized first-contact mechanism?
2. What are its hard limitations in stock Bitcoin/Core?
3. What is the strongest practical way to heat-seek real Gateways without a central directory?
4. Once one Gateway is found, should Gateway have bounded peer exchange?
5. What should Gateway know about app protocols and what stays opaque?
6. Is Gateway control plane, shared data plane, or per-protocol hybrid?
7. What is the minimal peerhood descriptor?
8. What changes, if any, are required to Gateway wire v1?
9. How should module API evolve into generic local app/provider registration?
10. How should index definition and instance be represented?
11. What must a range fingerprint commit to?
12. Which index types can start at arbitrary ranges and which require prior state?
13. What minimum upstream state is required to build Bitmap correctly without retaining a full Ord installation?
14. How should base Bitmap rules and historical compatibility interpretations coexist?
15. What belongs in 0.5.x and what waits?
16. What should be removed or simplified from the codebase?
17. What are the largest security/privacy/DoS risks?
18. What is the smallest next implementation that materially proves the architecture?

# 27. Design principles

**Bitcoin first.** Use Bitcoin's existing peer network as the decentralized entrance.

**Direct verification beats gossip.** Hints guide discovery; direct negotiation establishes compatibility.

**Stable Gateway membership matters.** Once inside, do not unnecessarily fall back into the haystack.

**Capability claims are not truth.** Each service defines evidence.

**Control plane and data plane are separable.**

**Protocol identity is machine-readable and versioned.**

**TRIP keeps meaning clean.**

**Indexes are reproducible capabilities, not opaque databases.**

**Definition and instance are different.**

**Dependencies are explicit.**

**Range is first-class.**

**Retention is a choice.**

**Products do not rewrite rules.**

**Gateway should be useful beyond Bitmap without becoming generic mush.**

# Final mandate

The spirit of Gateway is not "build another indexer."

It is:

> **Use Bitcoin to enter a decentralized communications fabric, use Gateway to discover what peers can do, let applications speak their own protocols, and let deterministic indexes be built, verified, ranged, retained, and served independently of the raw data they came from.**

Keep this architectural sentence visible:

> **Gateway should decouple what a node can answer from what a node must permanently store.**

And this networking sentence:

> **Bitcoin gets us into Gateway; once inside, Gateway should make it progressively easier to stay connected to the peerhoods we actually care about.**

Audit the code against those two statements. Strengthen them where reality requires it. Reject any current design that sounds aligned but does not survive protocol, security, and operational details.
