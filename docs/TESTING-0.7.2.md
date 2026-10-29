# Gateway 0.7.2 inscription tester guide

This is the local review guide for the inscription prerelease. The published baseline and live updater remain on 0.7.1 until a separate release is approved. Use a separate profile with real validated headers and small, explicit ranges. See [status](STATUS.md) for which checks have actually completed.

## First-stage scope

Headers, Bitcoin Blocks and Inscriptions are available. The standalone Transaction Index, related inscription transaction locator construction (including the retroactive action) and Gateway peerhood remain gated. Their implemented groundwork is not an enabled feature. Existing block-scoped transaction browsing remains available.

Fresh profiles enable inscription viewing. An explicitly saved disabled inscription setting remains disabled until you enable it. Do not change a working installation merely to try this build.

## Positional lookup and viewport

1. Open Indexes. Headers and Bitcoin Blocks should appear by default, including when no block bodies are stored. Add the Inscriptions track with the plus row.
2. Fetch a known inscription-bearing Bitcoin block and select one of its verified inscription coordinates. Compact coordinates use `<transaction position>i<inscription index>.<block height>`, for example `12i0.840000`; the example describes syntax and is not a claim that this exact position contains an inscription.
3. Confirm the Inscription viewport stays in the upper workspace while the timeline stays docked below. Inspector, Explorer and viewport selection should remain coherent when switching tabs or tracks.
4. Confirm the selected coordinate, conventional ID when derivable from the fetched transaction, containing block and reveal evidence agree. Transaction position and inscription index are zero-based; inscription index counts recognized envelopes across transaction inputs.
5. Use a fresh profile without transaction locators to prove positional content lookup works. A conventional ID with no known location should explain what is missing rather than imply the inscription does not exist.
6. Try available image, text, HTML, SVG, audio and video content. Unsupported types should offer raw information without pretending they rendered. Check raw/copy actions, previous/next navigation and resize behavior.
7. Scrub rapidly, then stop. Loading should wait briefly and settle on the current selection. Late responses from older selections must not replace the active content. Changing tabs or stopping the viewer must leave the app usable.

## Lean and Full indexing

1. Review a small explicit range using **Lean** and ephemeral retention. Planning and selecting should not start fetching or indexing.
2. Confirm the related transaction locator choice and later build action are visible in the Inscription inspector and disabled with an explanation. A manual request cannot enable either while the stage is gated.
3. Start the reviewed Lean job. Inscription payloads should be a flat list of positional strings, not duplicated transaction hashes or bodies. Committed block metadata must still retain verified anchors, transaction counts and scanned coverage.
4. A block containing no inscriptions is still processed coverage. Coverage, retained source blocks and cached content are separate states; absent bodies are not missing checkpoints.
5. Pause, restart and resume. Committed coverage should survive without claiming uncommitted work. A saved locator-enabled job must remain paused while its gate is closed.
6. In another profile, use **Full** over the same bounded range. Reveal records and bodies should be retained only under the explicit Full choice. Lean indexing must not initiate historical numbering, ownership, parent-child verification or recursive content fetching.
7. Test cache eviction or ephemeral sources, then reopen a positional inscription. Committed positions should remain; content may need its known block fetched again. An unavailable source should remain an availability error, not a destructive rewind or proof of absence.

## Referenced content and evidence

- Load a recursive or delegated inscription whose referenced location is already known. Its referenced content should use the shared resolver and local verification.
- Repeat with a missing locator. The viewer should identify the unresolved dependency; it must not invent a position or use disabled Gateway peers.
- Distinguish an unknown location from a known block that cannot currently be fetched. Retrying should not start an unrelated index build.
- Parent fields are claims unless the necessary spend/sat evidence has been checked. Loading a parent is not proof of parent-child provenance, current ownership or a complete list of children.
- Dynamic script-generated content requests should receive the same scoped dependency handling. Cycles, oversized responses and excessive requests must stop with a clear status.

## Isolation and privacy

Use controlled content fixtures for these checks, rather than uploading hostile content to Bitcoin:

- HTML/SVG must not read Gateway settings, invoke management routes, navigate the app, submit forms, open popups or request arbitrary remote URLs.
- Stop/reset must keep the parent app responsive when content misbehaves. Note browser-specific limits; a test result in one browser does not establish every renderer's behavior.
- No private locator or private cache object may be returned to an inbound Gateway peer. Peer lookup and serving remain off in this stage.
- Turning Private cache off must not declassify earlier objects. Viewing content must not silently publish an index.

## Existing features and delivery

Repeat [the timeline checks](TESTING-0.7.1.md) and [setup/browser checklist](TESTING-0.7.0.md): installation, restart, header sync, block fetch/cache reuse, block transaction browsing, optional Core sources, peer aliases/serving, browser registration and real `.bitcoin` address entry.

Check the dedicated installer Updates page, advanced source choices and saved preferences. The bundled public publisher and verification key should remain configured. A local review build is not evidence that the hosted service offers this version; live upgrade and renewal must be checked after publication.

The default Bitcoin serving port can be shared with another Gateway copy only by choosing an available fallback port. The app should report its actual port and preserve the preference. An explicitly chosen address should fail clearly if occupied.

Report the exact version, platform, selected address/range, mode, retention, expected result and observed result. Redact personal paths, credentials and addresses you do not want to publish. Windows executables remain unsigned; record exact alerts without disabling protection.
