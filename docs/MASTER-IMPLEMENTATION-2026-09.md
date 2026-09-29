# GATEWAY CLIENT

## MASTER CODEX ASTRA HANDOFF + IMPLEMENTATION BRIEF

### Authoritative brief for the next Gateway release

**Repository:** `Blockamoto/gateway`  
**Development branch:** `main`  
**Workflow until v1.0.0:** completed work is committed directly to `main`  
**Starting version:** treat the actual repository HEAD at the moment work begins as authoritative  
**Authority:** full implementation, refactoring, testing, documentation, versioning, build, push, tag, and release authority is granted for this pass

This document supersedes earlier conflicting audit-only and implementation briefs.

Do not stop after producing an architecture report.

Do not merely recommend changes.

Audit the existing implementation, preserve what works, improve what needs improving, implement the work described here, integrate it, push it, and produce a runnable release.

The repository is already substantial. Roughly speaking, much of the machine already exists. Treat this as an **audit-and-evolve release**, not a greenfield rewrite.

The objective is to take Gateway from a capable experimental client into a coherent early-test system whose indexing architecture is finally visible in the product itself.

---

# 1. THE PRIORITY ORDER

When trade-offs are necessary, use this order:

1. **Indexing**
2. **Peer-to-peer capability and serving**
3. **Discovery**
4. **Discovery Probe**

That order matters.

Do not spend the available implementation budget perfecting the experimental probe while Bitmap or the general indexing architecture remains unfinished.

The major proof of this release is indexing.

Networking exists to make the indexes obtainable, verifiable, and shareable.

Discovery exists to get the user to useful peers.

The Probe exists to understand and improve discovery.

---

# 2. THE CENTRAL IDEA

Gateway is a Bitcoin-native, peer-to-peer information client built around one simple principle:

> **A user should be able to ask for the highest-level thing they want, and Gateway should resolve only the lower-level dependencies required to answer it.**

This produces a directional model:

**Top-down intent.  
Bottom-up resolution.**

A user with no local Bitcoin data should be able to say:

> Index Bitmap.

Gateway should determine what that requires, descend through the dependency graph, acquire the necessary Bitcoin material, perform the required inscription interpretation, derive the Bitmap state, and return the requested index.

The user should not have to understand or manually prepare every dependency first.

At the same time, Gateway should remain honest about every dependency it is satisfying.

No invisible magic.

No implied completeness.

No pretending that data exists locally when it was streamed from a peer.

---

# 3. BITCOIN REMAINS REALITY

Gateway is not a replacement authority for Bitcoin.

Its trust model remains:

> **Peers provide knowledge.  
> Bitcoin provides evidence.  
> Local verification determines what we actually know.**

A Gateway peer may tell us:

- which block contains a transaction;
- which transaction spends an output;
- where an index range is available;
- where a sat currently resides;
- that it possesses Bitmap coverage;
- that it knows an inscription dependency.

Those are useful claims.

They are not automatically truth.

Where possible, verify the underlying Bitcoin evidence locally.

Gateway should accelerate access to knowledge, not replace verification with consensus-by-gossip.

---

# 4. ANSWERABILITY MUST BE SEPARATE FROM STORAGE

Keep this sentence at the architectural center:

> **Gateway should decouple what a node can answer from what a node must permanently store.**

A node may:

- download a block;
- decode it;
- feed several indexers;
- retain derived index state;
- discard the raw block.

Or it may retain the block.

Or it may already possess it through Bitcoin Core.

Or another Gateway peer may possess the derived index.

Or the node may eventually retain the entire Bitcoin chain.

All are legitimate configurations.

Gateway should support a spectrum from a tiny sparse client to an extremely deep historical provider.

Do not force every node toward the same storage shape.

---

# 5. INDEX AS LITTLE OR AS MUCH AS YOU WANT

This is a core product philosophy.

For every substantial data family, the user should be able to choose depth.

Examples:

- only what is needed for the current query;
- a selected range;
- full historical coverage;
- current state only;
- full historical lineage;
- imported existing data;
- peer-provided coverage;
- locally derived coverage.

A small machine should remain useful.

A large machine with considerable storage should be allowed to become extraordinarily useful.

Gateway should not punish either choice.

---

# 6. RETENTION IS A SEPARATE CHOICE

When Gateway needs underlying data to build an index, distinguish:

### Ephemeral

Fetch, verify/use, derive the requested state, then discard.

### Cache

Retain opportunistically subject to cache policy.

### Retain

Persist deliberately as long-term Gateway-owned data.

This choice should be visible in CLI and GUI.

Example:

A user with little disk space selects Bitmap indexing.

Gateway fetches the Bitcoin blocks required for the Bitmap scan, extracts the required inscription information, advances the Bitmap index, and discards the raw blocks afterward.

That is a perfectly valid Gateway configuration.

Do not require permanent block retention merely because the blocks were needed during derivation.

---

# 7. CLI-FIRST, GUI-FIRST-CLASS

The architecture should be CLI-first.

That means all important capabilities must be available without the graphical interface.

A Linux user building or operating Gateway entirely from a terminal must not be treated as a second-class user.

At the same time:

**The frontend is extremely important.**

The correct relationship is:

> **One capable headless engine.  
> Multiple first-class interfaces.**

The GUI must not contain critical logic unavailable to the CLI.

The CLI must not be used as an excuse for a primitive GUI.

Both should expose the same underlying concepts:

- capabilities;
- index definitions;
- index instances;
- dependencies;
- coverage;
- jobs;
- provenance;
- peer capabilities;
- retention policy;
- verification state.

Automated tests should be able to exercise the same engine without clicking through the UI.

---

# 8. AUDIT, DO NOT REWRITE

Before substantial modifications:

- inspect current HEAD;
- build it;
- run the test baseline;
- map the important components;
- inspect the existing implementation and repair history.

But this should be a **short implementation-oriented audit**, not another lengthy architecture-only phase.

Preserve working systems by default.

Refactor when there is evidence that current boundaries impede the architecture described here.

Do not rewrite stable systems merely because cleaner code could theoretically exist.

The previous Astra audit correctly insisted that the repository and live QA evidence be treated seriously, including the discovery ledger, BIP 434 negotiation, networking floor, module API, BOD, Satline and test suites. [contextual citation omitted; verify against primary evidence]

Use that discipline, but this time continue through implementation.

---

# 9. CURRENT EXISTING CAPABILITIES MUST SURVIVE

Audit and preserve the useful existing Gateway stack, including where present:

- Bitcoin P2P;
- Bitcoin DNS/address bootstrap;
- headers;
- Bitcoin block retrieval;
- Bitcoin on Demand;
- sparse Bitcoin storage;
- mounted/local Bitcoin Core data;
- Bitcoin Core RPC support;
- transaction lookup infrastructure;
- TXO spender support;
- Satline;
- Ord on Demand;
- Gateway protocol negotiation;
- BIP 434 Gateway feature negotiation;
- peer discovery ledger;
- peer introductions;
- Gateway browser routing;
- `.gateway`;
- `.bitcoin`;
- `god://`;
- installer/portable builds;
- setup and cleanup;
- user privacy controls.

Do not regress existing features to make room for Bitmap.

---

# 10. SOURCE PROVIDERS ARE INTERCHANGEABLE, BUT PROVENANCE IS NOT

A dependency may be satisfied by:

- Gateway-owned local state;
- Bitcoin Core RPC;
- mounted Bitcoin Core files;
- an existing Ord installation;
- an ordinary Bitcoin peer;
- a Gateway/BOD peer;
- a peer-served index;
- a cache;
- a verified imported checkpoint.

Use the best available legitimate provider.

But retain provenance.

A result should be able to answer:

> Where did this come from?

Examples:

- Local Gateway
- Local Bitcoin Core
- Mounted Core files
- Bitcoin peer
- Gateway peer
- Peer index
- Existing Ord index
- Derived locally from Bitcoin
- Cached
- Imported checkpoint

Do not silently collapse all providers into the same trust state.

---

# 11. THE INDEX DEPENDENCY MODEL

Indexes should explicitly declare what they need.

Conceptually:

```text
USER REQUEST
    │
    ▼
HIGH-LEVEL INDEX / QUERY
    │
    ▼
DEPENDENCY PLANNER
    │
    ├─ already local?
    ├─ available from Core?
    ├─ available from existing Ord?
    ├─ available from Gateway peer?
    ├─ obtainable from Bitcoin peer?
    └─ must be derived locally?
    │
    ▼
LOWER-LEVEL DATA / INDEXES
    │
    ▼
DERIVATION
    │
    ▼
REQUESTED RESULT
```

A user should not need to manually descend this tree.

Gateway should.

---

# 12. ONE BLOCK STREAM, MANY CONSUMERS

Do not repeatedly decode the same Bitcoin block for independent indexers when one traversal can safely feed all active consumers.

Conceptually:

```text
Bitcoin Block
      │
      ▼
Shared Decode / Event Extraction
      │
      ├── Transaction Locator
      ├── TXO Spender
      ├── Address State
      ├── Address History
      ├── Sat State
      ├── Satline
      └── Inscription Extraction
               │
               ▼
             Bitmap
```

Reuse computation in memory.

Do not entangle persistent state.

Each index must remain independently:

- defined;
- versioned;
- queryable;
- checkpointed;
- discardable;
- rebuildable;
- shareable where appropriate.

---

# 13. TRIP

Retain the TRIP model from the previous architecture work.

## T — Theory

The human idea.

What does the system mean?

Why does it exist?

## R — Rule Set

The deterministic executable rules.

Explicit.

Versioned.

Machine-repeatable.

Testable.

## I — Index

The derived state resulting from applying the rule set to declared inputs.

## P — Product

Explorer, marketplace, game, map, analytics, wallet, world, website, etc.

Products consume indexes.

Products do not silently rewrite the rules that created them.

The previous audit expressed this separation explicitly and correctly. [contextual citation omitted; verify against primary evidence]

Make it concrete in code.

---

# 14. INDEX DEFINITION VS INDEX INSTANCE

These must remain distinct.

## Index Definition

The recipe.

It should be capable of declaring things such as:

- stable ID;
- human-readable name;
- version;
- theory reference;
- rule-set ID/version/hash;
- network;
- dependencies;
- starting requirements;
- whether arbitrary starting ranges are valid;
- state transition rules;
- checkpoint schema;
- output schema;
- canonical serialization;
- fingerprint scheme;
- compatibility profile;
- implementation version.

## Index Instance

What a particular Gateway node actually has.

It includes:

- definition;
- network;
- coverage;
- gaps;
- checkpoint;
- starting state;
- chain anchor;
- dependency state;
- fingerprints;
- provenance;
- completeness;
- local queryability;
- remote serveability;
- retention policy;
- verification state.

Two peers may implement the same index definition but possess radically different coverage.

That is normal.

---

# 15. INDEX CATALOGUE FOR THIS RELEASE

Make these concepts explicit in architecture and UI.

Some are already substantially implemented. That does not matter. Their existence should become visible and coherent.

## 15.1 Bitcoin Headers / Block Coverage

Not necessarily a derived index in the same sense as later layers, but must expose coverage honestly.

## 15.2 Transaction Locator Index

Conceptually:

```text
txid -> block location
```

The purpose is to bridge a major historical-query gap in ordinary Bitcoin P2P.

Once the block location is known, fetch the real block from Bitcoin where possible and verify that the transaction is present.

Support Gateway-owned state and Bitcoin Core `txindex` as providers.

Expose which provider answered.

## 15.3 TXO Spender Index

Treat the currently integrated Bitcoin Core 31-era TXO spender functionality as a first-class declared capability.

Conceptually:

```text
outpoint -> spending transaction
```

It must no longer feel like an invisible implementation helper.

Expose whether this capability comes from:

- Bitcoin Core;
- Gateway-built state;
- Gateway peer;
- another legitimate provider.

This is particularly valuable for Satline and lineage operations.

## 15.4 Current Address State

Separate index.

Question:

> What outputs associated with this address are currently spendable at this snapshot?

Do not confuse it with history.

## 15.5 Historical Address Activity

Separate index.

Question:

> What historical transaction/output activity involved this address?

This may be substantially deeper and more expensive.

Make it opt-in.

Do not make historical address reconstruction a hidden dependency of ordinary Gateway use.

## 15.6 Current Sat Location / Sat State

The ability to know current sat placement is conceptually separate from full historical lineage.

Do not call this full Satline.

## 15.7 Satline

Full or partial sat lineage.

Given a sat/satpoint, trace movement through transactions using the required ordinal semantics.

Satline should remain capable of:

- on-demand resolution;
- checkpointing;
- peer acceleration;
- sparse operation;
- deeper optional indexing.

A node may retain enough bridge/index information to answer large amounts of Satline without storing all Bitcoin blocks.

A node with the hardware may choose extremely deep Satline-related state.

That must be explicit and opt-in.

## 15.8 Inscription Occurrence / Content Index

This indexes inscriptions, not an amorphous thing called "Ord."

It may be bounded.

A user can index inscriptions in selected Bitcoin ranges.

The result may include:

- inscription ID;
- reveal;
- content;
- envelope semantics;
- location/evidence where derivable.

A bounded inscription scan must not imply that global canonical inscription numbering is available.

## 15.9 Inscription Numbering

Separate dependency/state layer.

Canonical inscription numbering is sequential historical state.

When derived directly from Bitcoin:

- a full sequential numbering build must begin from the required historical beginning;
- alternatively, a valid verified prior numbering checkpoint may satisfy the earlier state.

A bounded mid-chain inscription scan does **not** magically know canonical inscription numbers.

If Gateway displays canonical inscription numbers, implement the historical numbering rules faithfully.

That includes the relevant cursed inscription and Jubilee/vindication behavior.

If that state is not available, show IDs without pretending numbers are known.

## 15.10 Bitmap Base Index

The principal new index for this release.

Detailed below.

## 15.11 Bitmap Compatibility / Contestability Validation

This is not a replacement truth index.

It is an optional deeper validation layer beside the base Bitmap result.

Detailed below.

---

# 16. DO NOT CALL EVERYTHING "ORD"

The user-facing and internal model should distinguish:

- inscriptions;
- inscription numbering;
- sat current state;
- Satline;
- provenance/history;
- delegates;
- historical Ord semantics.

"Ord" can remain the umbrella module/integration name.

It must not obscure which state is actually required.

This distinction is essential to sparse indexing.

---

# 17. EXISTING ORD DATA

If an existing local Ord installation is available, use it when it can legitimately satisfy a dependency.

Do not rederive information merely to prove Gateway can.

Like Bitcoin Core, Ord should be a provider rather than an architectural requirement.

Gateway must also work without Ord.

---

# 18. ORD IMPORT / SALVAGE — STRETCH FEATURE

This is useful but must not endanger the primary release.

After core Ord integration is sound, investigate a safe import/salvage pathway for an existing Ord data set that may no longer be syncing correctly.

The goal is:

- recover reusable valid information;
- mount or import readable data where possible;
- avoid throwing away useful historical indexing work.

Do **not** promise arbitrary repair of corrupted Ord databases.

Do not mutate a user's original Ord database in place unless there is an extraordinarily strong reason and explicit consent.

Prefer:

- read-only inspection;
- extraction;
- conversion;
- mount;
- import into Gateway-owned state.

If compatibility or integrity cannot be established, leave the source untouched.

This is a stretch checkpoint, not a blocker.

---

# 19. DEFER THESE INDEXES

Do not expand this release into every imaginable Ord or metaprotocol index.

Explicitly defer:

- Runes;
- generic metaprotocol index;
- galleries;
- unrelated creator indexes;
- arbitrary protocol marketplace;
- wallets.

The generic architecture should make those possible later.

Do not implement them now.

---

# 20. BITMAP IS THE PROOF CASE

Bitmap is not a decorative module.

It is the first major demonstration that Gateway can start from a high-level user request and resolve a deterministic protocol index from Bitcoin without requiring an unrelated giant global database.

The required flow is:

```text
User chooses Bitmap
       │
       ▼
Bitmap Definition
       │
       ▼
Dependency Planner
       │
       ▼
Required Bitcoin Coverage
       │
       ▼
Required Inscription Interpretation
       │
       ▼
Bitmap Rule Evaluation
       │
       ▼
Bitmap Index
       │
       ├── optional compatibility validation
       └── optional ownership resolution
```

---

# 21. BITMAP GENESIS

The known first Bitmap inscription is:

```text
86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732i0
```

Bitcoin block:

```text
792435
```

A fresh Bitmap index build must begin at the first Bitmap block and progress sequentially forward.

This is important.

Do not treat Bitmap as arbitrarily range-startable when building its state from scratch.

---

# 22. BITMAP RANGES: BUILD VS EXCHANGE

There are two different concepts.

## Fresh local build

Must begin from the correct Bitmap starting state and proceed forward.

For the current base index that means beginning at Bitmap genesis, block `792435`.

## Peer-served existing coverage

A peer that has already correctly built Bitmap state may advertise and serve bounded index ranges.

Example:

```text
792435–850000
850001–900000
```

That does not mean a naive fresh indexer may begin at 850001 with no prior state.

A served range must carry enough checkpoint/dependency context to make its meaning explicit.

Do not confuse **range exchange** with **arbitrary derivation starting point**.

---

# 23. BITMAP BASE RULES

Before implementation, enumerate the exact base rule set in the repository as a versioned machine-readable definition.

Do not leave the rules buried inside parser conditionals.

The project-level semantics we have established are:

## Districts

Claim form:

```text
{block}.bitmap
```

A claim cannot validly claim a Bitcoin block that does not yet exist at the relevant reveal point.

For each district, the accepted result is the first valid claim under the defined base ordering and validity rules.

Do not reorder historical results by later/current inscription numbering.

Record the Bitcoin evidence establishing claim order.

## Parcels

Claim form:

```text
{tx-index}.{block}.bitmap
```

Parcels belong structurally to the district defined by the corresponding Bitcoin block.

Preserve district/parcel provenance.

The schema and implementation should allow separately claimed parcels while retaining their ancestry back to the district.

The exact claim/provenance rules must be written explicitly as part of the Bitmap rule definition and covered by tests before relying on them.

District indexing is mandatory for this release.

Do not let ambitious parcel UI delay a correct district index.

---

# 24. BITMAP RULES MUST BE VISIBLE

The frontend should contain a proper rule surface.

A user should be able to inspect:

- active Bitmap base rule profile;
- version;
- plain-English explanation;
- machine rule identity;
- provenance/source;
- examples;
- historical compatibility profiles;
- exact rules responsible for disagreements.

The CLI should expose equivalent information.

Something like:

```text
gateway index rules bitmap
gateway index rules bitmap --profile base-v1
gateway index rules bitmap --profile opi-compat
```

Exact commands may differ.

The capability matters more than the spelling.

---

# 25. CONTESTED BITMAPS

"Contested" must not mean "we do not know anything."

It should mean that a Bitmap claim produces different results under explicitly named historical/compatibility interpretations.

The base rule result remains intact.

Compatibility disagreement is metadata beside it.

Conceptually:

```text
District: 123456.bitmap

Base Bitmap:
ACCEPTED

OPI compatibility:
REJECTED

Contested:
YES

Reason:
Reinscription handling differs

Evidence:
...
```

Do not silently replace the base Bitmap interpretation with marketplace behavior.

---

# 26. CONTESTED CHECKING SHOULD BE OPTIONAL DEPTH

A normal Bitmap build should establish the base index.

Deeper compatibility checking can be optional.

Users who want lightweight Bitmap indexing should not necessarily have to perform every compatibility analysis.

However, design storage so that additional validators can be run later without corrupting or rewriting the base result.

If enough evidence already exists, a compatibility validator should preferably operate over existing candidate records rather than forcing a total rescan.

---

# 27. BITMAP COMPATIBILITY CASES TO RESEARCH AND CODIFY

Do not guess these rules.

Enumerate and verify them against project history, relevant implementations and actual historical examples.

Known classes that require explicit treatment include:

- reinscriptions;
- negative/cursed inscription treatment;
- Jubilee/vindication consequences;
- differing inscription-number assumptions;
- batch/multiple-envelope behavior;
- pushdata/parser differences;
- encoded content/text interpretation;
- delegate behavior;
- malformed or historically accepted envelope variants;
- implementation-specific exclusions;
- ordering differences;
- other historically observed Bitmap implementation disagreements.

This list is a research checklist, not permission to invent rules.

For every implemented compatibility profile, document exactly what differs from the base profile.

---

# 28. BITMAP RECORDS

A Bitmap district record should be capable of retaining at least:

- district/target block;
- inscription ID;
- reveal transaction;
- reveal transaction index;
- reveal Bitcoin block height;
- reveal block hash;
- relevant content bytes;
- normalized candidate where appropriate;
- claim ordering evidence;
- base rule profile/version;
- accept/reject result;
- reason;
- compatibility results;
- contested flag;
- historical evidence references;
- satpoint if known;
- current location if known;
- ownership state if known;
- provenance;
- verification state.

Do not let ownership block claim indexing.

A legitimate state is:

```text
Bitmap claim: VALID
Current owner: UNKNOWN
```

---

# 29. BITMAP OWNERSHIP

Do not invent a separate Bitmap ownership system.

A Bitmap inscription ultimately sits on Bitcoin sat/UTXO state.

Use the appropriate Sat/Ord/Satline substrate to determine current location where possible.

If that dependency is missing:

```text
ownership = unknown
```

Do not infer.

---

# 30. BITMAP CHECKPOINTS

Bitmap builds must resume.

Persist enough information to establish:

- last fully processed Bitcoin block;
- checkpoint block hash;
- Bitmap rule version;
- upstream dependency version/state;
- derived-state commitment;
- index state.

On restart:

- validate the checkpoint;
- resume;
- do not restart from 792435 without cause.

On reorg:

- find a valid common checkpoint;
- rewind;
- replay.

Crash recovery is part of acceptance, not polish.

---

# 31. INDEX FINGERPRINTS

Derived-state fingerprints must bind enough context that equal fingerprints mean something useful.

For stateful range/index data, include or bind to:

- index definition ID/version;
- rule-set identity;
- Bitcoin network;
- coverage/range;
- starting checkpoint;
- chain anchor;
- dependency fingerprints;
- canonical serialization;
- resulting state.

Do not simply hash an unordered JSON dump and call it consensus.

---

# 32. COVERAGE IS FIRST-CLASS

Every index instance should be able to honestly express:

```text
full
partial
range(s)
gaps
unknown
```

For example:

```text
Transaction locator:
700000–710000
840000–850000

Bitmap:
792435–900000

Historical address:
NONE

Satline:
on-demand only
```

Do not imply continuity where gaps exist.

---

# 33. PEER CAPABILITY ADVERTISEMENT

This is the glue between indexing and P2P.

A Gateway peer should say precisely what it can do.

Not merely:

> "I support Satline."

But where useful:

```text
Protocol: Satline
Version: X
Coverage: ...
Query capability: ...
Checkpoint: ...
Serveable: yes
Evidence mode: ...
```

Likewise for indexes:

```text
Index: bitmap
Definition: base-v1
Coverage: 792435–900000
Fingerprint: ...
Serveable: true
```

And:

```text
Index: tx-locator
Coverage: ...
Provider: local Gateway
```

A capability advertisement guides routing.

It is not proof.

---

# 34. INDEX PEERS AS ACCELERATORS

Peers may supply:

- direct answers;
- locators;
- index ranges;
- checkpoints;
- fingerprints;
- dependency records.

A peer-served index is an acceleration mechanism.

It must not become a trusted opaque database.

Where the user or system requires deeper confidence, validate relevant records against Bitcoin evidence.

---

# 35. SATLINE P2P SHOULD REMAIN LEAN

Do not equate Satline networking with synchronizing a gigantic universal lineage database.

Separate:

- coverage advertisement;
- current sat state;
- transaction location;
- spender location;
- cached lineage segments;
- actual Bitcoin evidence.

A node can answer a Satline query using small indexes plus Bitcoin data obtained elsewhere.

A node can also choose to build far deeper Satline-supporting state.

Do not force the latter.

---

# 36. THE HEAVY PART IS OFTEN THE UNDERLYING INDEX

Be careful with terminology.

Satline as an interpretation layer need not itself be enormous.

Universal transaction location, spender information, historical address state and other supporting indexes can be the heavier components.

The UI should therefore show actual dependency costs rather than simply labelling "Satline" as heavy.

---

# 37. CORE INTEGRATION

Preserve and strengthen Bitcoin Core integration.

If Core already provides the required data:

use it.

Do not download another copy merely to maintain architectural purity.

Potential providers include:

- block data;
- headers;
- `txindex`;
- TXO spender state;
- RPC;
- mounted block files;
- other safely readable Core-owned data.

Never mutate Core's databases or wallets.

Gateway-owned derived indexes remain Gateway-owned.

Core is a provider and accelerator, not an architectural requirement.

---

# 38. GATEWAY AS A BITCOIN CLIENT

If Gateway has the capability to operate as a complete Bitcoin-serving node, either using Gateway-owned Bitcoin data or properly integrated underlying Bitcoin storage, it should be able to participate reciprocally in Bitcoin P2P.

If Gateway advertises an ordinary Bitcoin service capability, it must actually satisfy the semantics of that capability.

Do not advertise false service flags.

Where the architecture delegates actual serving to Core, make that relationship coherent.

Where Gateway itself possesses and can serve the data, allow it to serve that data subject to user policy.

The long-term shape is not a parasitic downloader.

A sufficiently complete Gateway node should be a useful Bitcoin peer.

---

# 39. P2P BEFORE DISCOVERY RESEARCH

After the indexing work, make the existing peer-to-peer capability layer dependable.

Audit:

- inbound Gateway sessions;
- outbound Gateway sessions;
- full-duplex use;
- symmetric negotiation;
- capability usability from either side;
- BOD provider routing;
- connection lifetime;
- request timeout behavior;
- connection closure reasons;
- peer health;
- serving permissions.

Gateway membership and possession of a particular index are separate.

A Gateway peer may speak Gateway while serving no Bitmap data.

Another may have deep Bitmap coverage.

Model both honestly.

---

# 40. CONTROL PLANE VS DATA PLANE

Keep the previous architecture distinction.

Gateway is fundamentally excellent at:

- discovery;
- capability negotiation;
- routing;
- index/service advertisement;
- session establishment;
- peer health.

It does not need to become the mandatory transport for every future application's payload.

Future systems may use Gateway for rendezvous and then establish:

- WebRTC;
- BitTorrent;
- another TCP protocol;
- future transports.

Do not build these future transports now.

Preserve clean protocol IDs and capability interfaces so they can exist later.

---

# 41. FUTURE BITMAP / WEBRTC WORK

Bitmap worlds and WebRTC relaying are future applications.

Do not implement them in this release.

But avoid architectural assumptions such as:

> All Bitmap traffic must permanently traverse today's Gateway TCP session.

Gateway should eventually be able to help peers discover each other and negotiate the appropriate world/session protocol without understanding every application's semantics.

Future-proof interfaces.

Do not future-build features.

---

# 42. DISCOVERY: OUTCOME FIRST

For this release the client's job is:

> **Find a verified Gateway peer.**

Do not artificially hamstring it to one discovery mechanism.

Use all legitimate configured methods.

Possible paths include:

- Bitcoin-native discovery;
- configured Gateway bootstrap/seed peers;
- manually supplied peers;
- previously learned Gateway peers;
- Gateway peer introductions;
- future legitimate mechanisms.

The user should not care which path happened to work first.

The client should retain provenance internally.

---

# 43. BITCOIN-NATIVE DISCOVERY REMAINS THE DEEP GOAL

The long-term resilience target remains:

> If all project-operated Gateway seeds disappeared but valid Gateway nodes remained reachable and discoverable through Bitcoin, a fresh Gateway client should eventually be able to rediscover the network.

Do not abandon that work.

But do not make an early tester wait indefinitely for an unproven experimental path.

The previous architecture brief correctly described Bitcoin as the root entrance and direct BIP 434/Gateway verification as stronger than gossip. [contextual citation omitted; verify against primary evidence]

Preserve that research direction.

Do not make it the only alpha entrance.

---

# 44. RELIABLE ALPHA ENTRANCE

For actual testers, use a pragmatic bootstrap path if needed.

That may mean one or more configured Gateway seeds/providers.

Those endpoints are:

- entrances;
- not Bitcoin authorities;
- not index truth;
- not permission servers.

Once connected, normal Gateway verification still applies.

If bootstrap peers disappear, other discovery paths continue.

---

# 45. KEEP SEEKING INTELLIGENTLY

If Gateway currently has no healthy verified Gateway peer:

seek.

Use available mechanisms.

Once Gateway possesses a useful healthy mesh:

reduce wasteful exploration.

If the Gateway mesh disappears:

resume wider discovery automatically.

The user should not have to repeatedly press "find peers."

---

# 46. DISCOVERY LEDGER

Preserve the durable discovery work already present.

The historical ledger and active queue are different concepts.

Track where appropriate:

- heard endpoint;
- introducing peer;
- advertisement provenance;
- specific service hint;
- ambiguous/all-bits hint;
- direct observed service bits;
- attempted;
- last attempt;
- direct-negative;
- connected;
- Gateway verified;
- useful capability provider;
- cooldown;
- retry eligibility;
- failure reason.

Do not forget history merely because an endpoint leaves a bounded active queue.

---

# 47. DO NOT CREATE TWO RANDOM P2P STACKS

Do not introduce libp2p merely because discovery is difficult.

Do not build a second parallel general-purpose P2P system unless there is a concrete property the existing architecture cannot supply.

Simpler is better.

---

# 48. DISCOVERY PROBE

The Probe is useful.

It is also the fourth priority.

Do not let it delay the index architecture or Bitmap.

When implemented/polished, make it a deliberately entered advanced area.

A normal Gateway user should not be dropped into experimental discovery instrumentation.

---

# 49. PROBE UX: TWO LEVELS

The Probe should be understandable by someone who does not want to decipher raw networking logs.

It should provide two simultaneous explanatory layers.

## Simple explanation

Plain language.

Examples:

> "Gateway is asking Bitcoin peers for addresses."

> "12 possible Gateway candidates were found."

> "This candidate advertised the Gateway hint, but direct verification failed."

> "A verified Gateway peer was found."

## Technical detail

Expandable/full view showing things such as:

- peer endpoint;
- Bitcoin connection;
- `addr` / `addrv2`;
- introducer;
- service mask;
- bit-25 interpretation;
- direct `version`;
- BIP 434 result;
- Gateway negotiation;
- protocol list;
- attempt history;
- cooldown;
- timing;
- failure reason.

The Probe should explain exactly what it is doing and why.

Not merely emit logs.

---

# 50. PROBE EXPERIMENT BOUNDARIES

The Probe must distinguish:

- controlled/synthetic test;
- LAN test;
- public-network test;
- successful hint;
- successfully heard endpoint;
- socket connection;
- successful Bitcoin negotiation;
- BIP 434 Gateway verification;
- useful Gateway session.

Do not turn a weaker result into a stronger claim.

If the standalone `gateway-bitcoin-discovery` experiment remains useful, keep it as a research laboratory.

The product may reuse concepts or code.

Do not erase the distinction between experiment and ordinary client behavior.

---

# 51. FRONTEND: INDEXES SHOULD BECOME LEGIBLE

The frontend needs a proper Indexes/Data surface.

The user should be able to see something like:

```text
Bitcoin
  Headers                Full to 967,xxx
  Blocks                 Sparse
  Transaction Locator    Full / Core txindex
  TXO Spender            Full / Core
  Address UTXOs          Not built
  Address History        Not built

Ordinals
  Inscriptions           767430–tip
  Numbering              Full
  Current Sat State      Available
  Satline                On demand

Bitmap
  Base index             792435–912000
  Compatibility checks   Partial
  Current owner state    On demand
```

Exact UI design is open to Astra.

The conceptual clarity is mandatory.

---

# 52. FRONTEND: DEPENDENCY VIEW

When a user selects an index, show what it requires.

Example:

```text
Bitmap Base Index
│
├── Bitcoin blocks from 792435 onward
│
└── Inscription extraction
     │
     └── Bitcoin transaction/witness data
```

If ownership is enabled:

```text
Bitmap ownership
│
└── Current inscription satpoint
     │
     └── Sat movement/spender dependencies
```

The user should be able to expand into detail rather than be assaulted with it by default.

---

# 53. FRONTEND: PLAN BEFORE LARGE BUILDS

Before a substantial sync/index job, show:

- what will be built;
- dependencies;
- existing usable sources;
- approximate known storage impact where estimable;
- retention policy;
- whether source blocks will be retained;
- whether full sequential history is required;
- whether peer data may be used.

For truly large indexes such as deep transaction/spender/address/Satline-related state, make the commitment clear.

No accidental multi-hundred-gigabyte surprises.

---

# 54. FRONTEND POLISH

Give the application a fresh visual pass.

Astra is explicitly allowed to improve visual hierarchy, consistency, clarity, empty states, job progress, navigation and general finish.

Do not redesign for the sake of redesign.

Preserve useful existing functionality.

The target is:

> The same machine, but the machine finally explains itself.

---

# 55. UNIFIED APPLICATION

Gateway remains one product.

Do not turn it into separate executables the ordinary user must assemble.

Architectural modules are good.

Fragmented product experience is not.

Use shared surfaces for:

- peers;
- capabilities;
- indexes;
- jobs;
- storage;
- provenance;
- modules.

---

# 56. PRESERVE GATEWAY BROWSER WORK

Do not make this release a browser rewrite.

Preserve:

```text
.gateway
.bitcoin
god://
```

and positional Bitcoin addressing where already implemented.

Do not regress routing while making the deeper architecture changes.

---

# 57. CLEAN UNINSTALL

Retain clean ownership boundaries.

Never delete external:

- Bitcoin Core data;
- Ord data;
- wallets;
- mounted external data;
- user exports;

unless explicitly requested through an appropriate external operation.

Gateway-owned caches/indexes/discovery state may be deleted only through explicit destructive controls that show the affected paths.

---

# 58. PRIVACY

Do not accidentally publish:

- queries;
- browsing history;
- private Satline requests;
- private inscription lookups;
- Bitmap browsing;
- private cache contents;
- filesystem paths;
- Core credentials;
- discovery history;
- private peerhood membership.

Serving is capability plus user policy.

Possession does not automatically mean publication.

---

# 59. IMPLEMENTATION PROCESS: BANK THE WORK

This is crucial.

Astra usage is finite.

Do not allow a large parallel implementation to exist only as uncommitted or unintegrated work until the very end.

Parallel agents are fine.

Progress must be banked.

Whenever a coherent feature slice is complete:

1. integrate it;
2. run the relevant tests;
3. ensure the build is coherent;
4. update status documentation;
5. commit;
6. push to `main`.

Prefer each major checkpoint to leave behind a runnable system.

---

# 60. RELEASE CHECKPOINTS

Do not save all release activity for one enormous final event.

Use sensible intermediate pre-releases when a milestone is genuinely runnable and useful.

Exact version numbers should follow the repo's current scheme.

Release names must include version numbers.

A release should correspond to a coherent state, not every tiny commit.

The aim is simple:

> If Astra access stopped at the next checkpoint, there should still be something useful in GitHub.

---

# 61. PARALLEL AGENTS ARE NOT AN EXCUSE FOR PARALLEL CHAOS

Astra may use multiple agents internally.

That is fine.

Ring-fence work into coherent component slices.

Avoid several agents simultaneously restructuring the same central interfaces without coordination.

Integrate frequently.

Do not leave unresolved branches, unmerged work, or invisible local state.

Until v1.0.0, the project workflow is intentionally direct-to-main after completed integration.

---

# 62. REPOSITORY MUST BECOME SELF-HANDING-OFF

This is a first-class deliverable.

A completely different agent should be able to clone `Blockamoto/gateway`, read the repository, and continue development correctly with no access to this conversation.

Create or maintain a clear "start here" path.

A suitable structure might include:

```text
docs/
  START-HERE.md
  STATUS.md
  ARCHITECTURE.md
  ROADMAP.md
  INDEX-ARCHITECTURE.md
  BITMAP.md
  NETWORKING.md
  DISCOVERY.md
  DECISIONS.md
```

Do not mechanically use those filenames if the repo already has a better established structure.

The capability matters.

---

# 63. START-HERE DOCUMENT

A new agent should be able to learn quickly:

- what Gateway is;
- what the architecture is;
- current version;
- last good commit;
- what works;
- what is experimental;
- what is broken;
- current priority;
- deferred work;
- build instructions;
- test instructions;
- release workflow;
- where protocol specs live;
- where index definitions live;
- what to do next.

The test is:

> I give a new agent repository access and say, "Continue Gateway from where we left off."

It should be able to do that.

---

# 64. STATUS MUST BE LIVING STATE

Update the repository status after major checkpoints.

It should say things like:

```text
Current release:
vX.Y.Z

Last validated commit:
abc123

Completed:
- Generic index definitions
- Bitmap base scanner

In progress:
- Bitmap compatibility validators

Known issues:
- ...

Next:
- ...

Deferred:
- Runes
- WebRTC
```

Do not leave status scattered across stale implementation briefs.

---

# 65. DECISION LOG

Record important architectural choices and why.

Examples:

- why Bitmap fresh builds begin at 792435;
- why range exchange differs from arbitrary range build;
- why inscription numbering is separate from inscription occurrence;
- why compatibility disagreement does not mutate Bitmap base results;
- why Core/Ord are providers rather than requirements;
- why discovery may temporarily use alpha bootstrap peers;
- why query caches are private by default.

Future agents should not repeatedly rediscover these decisions.

---

# 66. IMPLEMENTATION CHECKPOINT ORDER

Use roughly this order.

## Checkpoint A — Baseline and architecture map

Fast.

- inspect HEAD;
- build;
- run tests;
- map current implementation;
- identify genuine blockers;
- update repo status.

Do not spend the entire session here.

## Checkpoint B — Generic index substrate

Implement/solidify:

- index definitions;
- instances;
- dependencies;
- coverage;
- checkpoints;
- retention;
- provenance;
- jobs;
- CLI;
- frontend primitives.

## Checkpoint C — Make lower indexes explicit

Integrate currently existing capabilities cleanly:

- transaction locator / `txindex`;
- TXO spender;
- current address state;
- historical address architecture;
- current sat state;
- Satline dependencies.

Do not necessarily build every enormous full-history index by default.

Make the model correct.

## Checkpoint D — Inscription layers

Separate:

- bounded inscription indexing;
- sequential numbering;
- cursed/Jubilee historical state;
- existing Ord provider integration;
- selective historical dependency resolution.

## Checkpoint E — Bitmap

Implement:

- base rule definition;
- genesis/start boundary;
- scanner;
- checkpoint/resume;
- records;
- UI/API/CLI;
- rules surface;
- compatibility/contested validation;
- ownership where available.

This is a major release checkpoint.

## Checkpoint F — Peer capability/index serving

Expose:

- capabilities;
- coverage;
- provider routing;
- peer-served indexes;
- verification boundaries;
- full-Bitcoin serving where semantics are actually satisfied.

## Checkpoint G — Discovery

Ensure:

- practical Gateway entry;
- persistent mesh;
- Bitcoin-native path remains active;
- reliable fallback;
- discovery ledger preservation.

## Checkpoint H — Probe and final polish

Finish:

- Probe frontend;
- simple/detail views;
- diagnostics;
- design polish;
- packaging;
- acceptance;
- release.

---

# 67. REQUIRED ACCEPTANCE: ZERO LOCAL BITCOIN STATE

Start from a fresh Gateway installation.

No Bitcoin Core.

No Ord.

No Gateway indexes.

User selects Bitmap indexing.

Gateway should:

1. understand the requested Bitmap index;
2. identify dependencies;
3. obtain Bitcoin connectivity/data;
4. begin from Bitmap genesis;
5. resolve necessary inscription information;
6. produce Bitmap state;
7. checkpoint;
8. resume after restart;
9. optionally discard fetched raw blocks;
10. expose result through CLI and GUI.

This is the conceptual golden path of the new architecture.

---

# 68. REQUIRED ACCEPTANCE: BITCOIN CORE

On a machine with Core:

- detect/configure it;
- use appropriate data;
- use `txindex` when available;
- use TXO spender functionality when available;
- avoid duplicate Bitcoin storage;
- leave Core-owned data untouched;
- allow Gateway indexes to exist over that provider.

Stopping Core must not be treated as identical to "all Bitcoin data disappeared" if readable mounted data remains legitimately usable.

---

# 69. REQUIRED ACCEPTANCE: EXISTING ORD

With usable existing Ord data:

- recognize it as a provider;
- use it where safe;
- avoid unnecessary re-indexing;
- retain independent Gateway operation when Ord is unavailable.

If the stretch salvage/import feature lands, test read-only handling of a non-syncing but readable source.

---

# 70. REQUIRED ACCEPTANCE: TRANSACTION LOCATOR

Given a historical txid:

1. obtain the block locator from a legitimate Gateway/Core/index provider;
2. obtain the real block;
3. verify the transaction is actually present.

A false peer locator must not poison trusted local state.

---

# 71. REQUIRED ACCEPTANCE: SPENDER

Given an outpoint:

- obtain the spending transaction if known;
- expose provider/provenance;
- verify against Bitcoin evidence where required;
- distinguish absence of knowledge from proof of unspent state.

---

# 72. REQUIRED ACCEPTANCE: ADDRESS STATE

Demonstrate clearly that:

- current address UTXO state;
- historical address activity

are different indexes.

One may exist without the other.

---

# 73. REQUIRED ACCEPTANCE: SAT STATE VS SATLINE

Demonstrate that Gateway can represent:

- current sat location knowledge;
- partial/on-demand lineage;
- deeper retained Satline capability;

without conflating them.

---

# 74. REQUIRED ACCEPTANCE: BOUNDED INSCRIPTIONS

Perform an inscription scan over a bounded Bitcoin range.

Gateway may expose:

- IDs;
- content;
- reveal evidence;

but must not imply that canonical global inscription numbering is known merely because the bounded scan succeeded.

---

# 75. REQUIRED ACCEPTANCE: FULL INSCRIPTION NUMBERING

Where Gateway claims canonical inscription numbers derived from Bitcoin:

- use the required full prior history or verified checkpoint;
- include historical curse/Jubilee numbering behavior;
- produce deterministic repeatable output.

If those prerequisites are absent:

do not claim the numbering.

---

# 76. REQUIRED ACCEPTANCE: BITMAP GENESIS

Confirm that the known first Bitmap:

```text
86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732i0
```

at Bitcoin block:

```text
792435
```

is correctly discovered and represented by the base Bitmap implementation.

---

# 77. REQUIRED ACCEPTANCE: BITMAP RESUME

Begin indexing.

Stop.

Restart Gateway.

Validate checkpoint.

Resume without rebuilding from genesis.

---

# 78. REQUIRED ACCEPTANCE: BITMAP COLLISION

Provide multiple claims for one district.

The base rule profile must deterministically choose the accepted result.

Rejected candidates/evidence must remain available where required for audit/compatibility evaluation.

---

# 79. REQUIRED ACCEPTANCE: BITMAP CONTESTED CASE

Use at least one historical case where:

- base rules;
- a named compatibility profile

produce different outcomes.

The UI/CLI must show:

- base result;
- compatibility result;
- contested flag;
- exact rule causing disagreement;
- evidence.

The compatibility profile must not rewrite the base index.

---

# 80. REQUIRED ACCEPTANCE: BITMAP RETENTION

Build Bitmap while configured not to retain source Bitcoin blocks permanently.

Confirm:

- derived index remains;
- source bytes are discarded according to policy;
- provenance/checkpoints remain valid;
- future queries behave honestly about what remains local.

---

# 81. REQUIRED ACCEPTANCE: PEER INDEX CLAIMS

Have a peer advertise index coverage.

Test:

- successful valid use;
- unavailable claimed range;
- false record;
- mismatching fingerprint;
- stale checkpoint.

A lying peer must not poison locally verified state.

---

# 82. REQUIRED ACCEPTANCE: BITCOIN SERVING

Where Gateway advertises an ordinary Bitcoin service capability, test that another Bitcoin peer can actually request and receive the advertised data.

Do not call Gateway a Bitcoin-serving node based solely on storage possession.

Test the real wire behavior.

---

# 83. REQUIRED ACCEPTANCE: DISCOVERY

Test multiple entrance paths.

At minimum:

- configured alpha bootstrap;
- learned Gateway peer;
- Bitcoin-native experimental discovery where available;
- manual peer entry.

A user should end with a directly verified Gateway session.

The UI should show which path produced it.

---

# 84. REQUIRED ACCEPTANCE: PROBE

The Probe should be able to explain one discovery session both ways:

### Simple

A human-readable story.

### Detailed

Technical event/evidence timeline.

The same underlying event data should power both.

---

# 85. REQUIRED ACCEPTANCE: CRASH, RESTART, REORG

Test interruption during long index work.

Persist only states that are safe to resume.

Verify chain anchors.

Handle reorg rewind.

Do not leave "99.6%" jobs permanently wedged because one block failed.

Surface:

- current block;
- current dependency;
- retry state;
- exact error.

---

# 86. PERFORMANCE WORK

Profile before rewriting.

For Satline and index building, measure:

- block fetch time;
- tx locator time;
- spender lookup time;
- prevout resolution;
- decoding;
- repeated parsing;
- cache hit/miss;
- peer/Core/local source;
- dependency wait time;
- index write time.

Look especially for accidental repeated work.

The first resolution may be expensive.

Repeated work should get cheaper when legitimate reusable state exists.

---

# 87. DO NOT HIDE FAILURES BEHIND PROGRESS BARS

Every substantial job must expose exact current work.

Example:

```text
Bitmap
Processing block 842,191
Dependency: inscription extraction
Source: Bitcoin peer 203...
Retry: 2/5
```

If a block repeatedly fails, identify it.

Do not present an apparently frozen "68%" indicator.

---

# 88. WHAT NOT TO DO

Do not:

- rewrite Gateway from scratch;
- turn the audit into the entire task;
- require Bitcoin Core;
- require Ord;
- require full Bitcoin block retention for derived indexes;
- require full Ord for Bitmap;
- globally index every sat by default;
- confuse current sat state with Satline;
- confuse inscriptions with inscription numbering;
- claim numbers from a bounded scan without prior numbering state;
- allow marketplace rules to silently redefine Bitmap;
- discard contested evidence merely because one implementation became popular;
- allow peer index claims to become authority;
- claim Bitcoin services Gateway cannot actually serve;
- make Bitcoin-only Gateway discovery a blocker for alpha;
- abandon Bitcoin-native discovery research;
- introduce libp2p without a concrete requirement;
- build Runes;
- build generic metaprotocol indexes;
- build galleries;
- build WebRTC Bitmap worlds;
- build wallets;
- build an app marketplace;
- mutate external Core or Ord data;
- delete external data on uninstall;
- keep major implementation work unpushed until the end.

---

# 89. RELEASE QUALITY

The final checkpoint should produce a real early-test release.

Where current build infrastructure supports them, produce:

- Windows installer;
- Windows portable build;
- Linux build;
- version information;
- release notes;
- hashes;
- known issues;
- migration notes;
- GitHub release.

Use version numbers in release names.

Do not claim tests that were not actually performed.

Distinguish:

- unit tests;
- synthetic network tests;
- local/LAN tests;
- native Windows tests;
- public-network tests;
- unproven experimental behavior.

---

# 90. THE RELEASE IS SUCCESSFUL WHEN

I can hand Gateway to somebody with no Bitcoin Core installation.

They can open one application.

They can ask for a high-level thing.

Gateway understands what lower-level state is required.

It finds or builds that state.

It tells them where it came from.

It lets them keep or discard underlying data.

It can use peers without trusting them blindly.

It can grow from sparse knowledge toward deep indexed state.

And I can choose Bitmap with an empty local store and watch Gateway work its way down to Bitcoin and back up again.

That is the architectural proof.

---

# 91. THE BIGGER MODEL

Gateway should ultimately be able to support nodes such as:

### Tiny client

- few cached blocks;
- no full indexes;
- mostly on-demand queries.

### Bitmap-oriented node

- sparse Bitcoin;
- transaction/spender support;
- inscription dependencies;
- full Bitmap index;
- little unrelated state.

### Satline provider

- deep transaction/spender state;
- substantial sat-lineage capability;
- perhaps little unrelated product state.

### Historical address provider

- expensive address-history index;
- Gateway peer serving enabled.

### Full Gateway Bitcoin node

- full Bitcoin data;
- Bitcoin P2P serving;
- deep Gateway indexes;
- broad peer capabilities.

There is no privileged final shape.

The network benefits from heterogeneous useful nodes.

---

# 92. THE PHILOSOPHY IN ONE DIAGRAM

```text
                    USER INTENT
                        │
                        ▼
              "What do I want to know?"
                        │
                        ▼
                GATEWAY CLIENT
                        │
                Dependency Planner
                        │
        ┌───────────────┼────────────────┐
        │               │                │
       Local          Existing          Peers
      Gateway        Core / Ord     Bitcoin / Gateway
        │               │                │
        └───────────────┼────────────────┘
                        │
                        ▼
                 BITCOIN EVIDENCE
                        │
                        ▼
                 SHARED BLOCK FLOW
                        │
      ┌─────────────────┼──────────────────┐
      │                 │                  │
  Bitcoin Indexes    Sat / Ord         Future Indexes
      │                 │
      │                 ▼
      │              Bitmap
      │                 │
      └─────────────────┴─────────► Products
```

The user starts at the top.

Gateway resolves downward.

Truth comes back upward.

Storage remains optional.

---

# 93. THE PHILOSOPHY IN WORDS

Gateway is not trying to become one enormous database.

It is trying to make Bitcoin-derived knowledge **addressable**.

A node should know what it knows.

Know why it knows it.

Know what it does not know.

Know who claims to know something else.

Know how to verify that claim.

And be able to acquire just enough additional state to answer the next question.

That is the living thing inside the project.

---

# 94. FINAL MANDATE TO CODEX ASTRA

Start by reading the current repository.

Treat HEAD as authoritative.

Understand what already works.

Preserve it unless evidence justifies change.

Then build.

Do not return only an architecture document.

Implement the index substrate.

Make the existing lower-level capabilities explicit.

Separate inscriptions from numbering, sats from Satline, current address state from historical address activity.

Implement Bitmap as the first serious top-down derived index.

Begin a fresh Bitmap build at the correct historical boundary.

Expose the base rules.

Expose compatibility disagreements without allowing them to mutate the base state.

Make dependencies inspectable.

Make retention selectable.

Make CLI and GUI speak the same underlying language.

Let peers advertise exactly what they can serve.

Use Bitcoin evidence rather than peer authority.

Make a complete Gateway node useful to ordinary Bitcoin peers when it genuinely satisfies those service semantics.

Then strengthen Gateway P2P.

Then make discovery reliably find Gateway peers using whatever legitimate routes are available while preserving Bitcoin-native rediscovery as the deeper resilience goal.

Then give the Discovery Probe a human frontend with both simple and forensic views.

Throughout the work:

**finish slices, integrate them, test them, commit them, push them, and document where the repository stands.**

If work stops unexpectedly, the repository should still be coherent.

If another agent arrives tomorrow, the repository should explain itself.

If a user asks for Bitmap with nothing locally, Gateway should know how to descend all the way to Bitcoin, obtain only what is necessary, build upward again, and leave behind exactly the state the user chose to retain.

Keep these five sentences visible:

> **Top-down intent. Bottom-up resolution.**

> **Bitcoin remains reality.**

> **Peers provide knowledge; evidence determines what we know.**

> **Gateway should decouple what a node can answer from what it must permanently store.**

> **Index as little or as much as you want.**

Build that Gateway.