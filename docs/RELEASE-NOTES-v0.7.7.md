# v0.7.7: Sats

Development preview for testing. Publication, promotion to main and automatic-update promotion remain held for later approval. The acceptance record identifies the current preview, its exact executable and package checks, and the separately preserved earlier results.

- Genesis-first Sat identity and live placement, with shared output facts and paginated ordered ranges.
- A Sat ranges subtrack under TXIO, plus optional historical output ranges.
- Sat explorer navigation to containing outputs, offsets and addresses, plus address UTXO range views. Strict P2PK public-key destinations use the same exact-script identity as Addresses, separately from same-key P2PKH and alternative key encodings.
- Individual Sat pages use the main address bar; timeline Sat selection resolves an output-local offset from saved ranges and opens its numbered Sat, with unavailable identity shown when evidence is missing.
- Known inscription holdings at compatible checkpoints, with explicit identity coverage.
- Authenticated transaction fee-prefix caches retained during Sat processing.
- Satline history tracing is disabled for this version, with new and resumed traces blocked. Existing trace evidence is preserved. Saved Sat lookup, ranges and known input BPS labels remain available.
- Plain timeline wheel input scrolls vertically across labels and lanes; Ctrl/Command plus wheel zooms on the time area, with pinch zoom and deliberate horizontal pan preserved.
- Bare integers in Gateway open blocks through the selected tip and sats above it; explicit `.sat` and `.bitcoin` keep their meaning. The browser companion supports explicit `.sat` handoff without intercepting bare-number browser searches.
- Ordinal Sat names work with or without `.sat` inside Gateway, and explicit named `.sat` links work through the companion. Name conversion is local and does not launch tracing.
- Reviewed Build, Pause, Resume and Live controls with local-first evidence, missing-data waiting and independent pruned block retention. Retain the newest two savepoints at block heights divisible by 12 and the newest two divisible by 144, sharing identical heights and unchanged pages. Recovery restores an eligible selected-chain savepoint and replays forwards; this does not guarantee recovery from every 288-block reorg.

Existing Addresses, UTXO, TXIO and locator data remain compatible. Older experimental Sat state requires a reviewed rebuild and is preserved. Full consensus validation and Gateway peer-state sharing remain deferred.

Historical ranges start off; full historical Satline reconstruction is deferred. Known inscription holdings and retained history have explicit coverage limits. Full-mainnet build time, memory and storage are not established by the bounded authentic-block and synthetic fixtures. See [Sats testing](TESTING-0.7.7.md) and [this preview acceptance record](RELEASE-ACCEPTANCE-0.7.7.json).
