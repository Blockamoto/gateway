# Gateway 0.7.4 prerelease testing

Use a separate profile for testing an upgrade or an unfamiliar index range. The prerelease enables the TXIO index alongside Headers, Blocks, Inscriptions and Transaction Index. Gateway peerhood and later index features remain locked.

## Positional spend navigation

- Build a bounded TXIO range. An input position such as `i1.2.840000.bitcoin` identifies input one (the second input) of transaction two at height 840000. Inputs and outputs use zero-based ordinals; transaction zero is coinbase.
- Open a transaction's Details or its flow and open an input's original output. Where the index knows the relationship, Gateway should use the saved BPS output directly. If it is unresolved, an explicit fallback can try the existing verified location providers.
- From an output, use the spending-input action. A known link should open the correct spending input and containing transaction. An unknown result must remain unknown, with no claim that the output is unspent.
- Test positions whose bodies are not cached. The saved relationship should remain available while the destination block may need fetching. Offline failure to load bytes must not erase the relationship.
- Navigate rapidly between transactions while a lookup is pending. Late or differently anchored responses must not move the current selection.

## Storage and upgrade

New Bitcoin Blocks writes use verified serialized raw bodies and compact transaction-location records. Upgrading inventories legacy Blocks records and prepares a versioned raw-backed root. Legacy committed batches remain readable and retained; incomplete conversion must not remove the usable legacy head. There is no automatic deletion of legacy duplicate bodies in this release.

Check an existing profile offline after migration and restart. Its retained blocks, transaction lookup, JSON views, Lean inscription positions and Full selected inscription bodies should remain available. Missing source evidence or insufficient space should leave the old usable records in place. Existing disk space is not reclaimed automatically; new writes avoid the complete decoded-body duplicate.

## Workspace fixes

- Add an empty Inscriptions track and fit the chain. Its ghost potential band should begin at the catalog's mainnet suggestion, 767430. Zoom on either side of that boundary. Earlier explicitly indexed ranges must remain visible, and an earlier custom build origin remains valid.
- Resize the viewport to a narrow panel. Map zoom, navigation and Fit controls should occupy their own toolbar and leave the map content unobscured. Map placement and square proportions should remain unchanged.
- Confirm wheel/pinch zoom, dragging, remembered views, fullscreen and compact/expanded timeline navigation still work.

## Automatic delivery

The existing hosted Gateway channel can offer this explicitly approved prerelease. Profiles with automatic installation enabled can download, apply and restart through the normal polling path. Check-only profiles continue to offer installation; manual, offline and custom-source preferences are preserved. A GitHub prerelease alone does not activate delivery: the signed hosted feed must name 0.7.4 and pass all signature, expiry, sequence, platform, checksum and compatibility checks.

Inscription reveal evidence alone does not establish global numbering, current ownership or parent-child provenance. TXIO indexing records verified spend relationships where creation locations are known; sparse missing predecessors are unresolved rather than inferred.
