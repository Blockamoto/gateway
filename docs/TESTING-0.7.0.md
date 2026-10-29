# Gateway 0.7.0 — QA tester guide

Gateway retrieves the Bitcoin blocks you request. **Headers and Blocks are available; extra indexes and Gateway peerhood are locked.** You do not need a wallet, Bitcoin Core or a full blockchain download to try it.

For each check, record **Pass**, **Fail** or **Not tested**, the package/version, operating system, browser/profile and what happened. A missing optional Core node or second test machine is “Not tested”, not a failure. Start with a small range: do not enable a whole-chain build just to test a control. Use disposable profiles for removal, custom-publisher, automatic-install and recovery tests. See [current status](STATUS.md) for release acceptance; this checklist does not itself record a pass.

## 1. Install, launch and restart

- **Windows installer:** run the separate `gateway-client-v0.7.0-installer.exe`. Test its application-directory selection, launch into guided setup and Start-menu shortcut. The installer includes the app, browser companion and header snapshot. `GatewayClient.exe` is the application; it is not the installer.
- The installer should show directory selection, a separate **Updates** page, then a review before installation. Go Back and Next and confirm every choice survives. Advanced source settings should start collapsed, expand by keyboard or mouse, and stay readable without clipped controls.
- **Windows portable:** extract the entire `windows-amd64-fresh.zip` and double-click `GatewayOnDemand.exe`. Keep the files together. It should run without installation and use its own local data. An app-only ZIP is intended for an existing profile and omits the snapshot.
- **Linux:** extract the Linux fresh ZIP, make `gateway-client` and `gateway-update-helper` executable, then run `./gateway-client`. Windows browser registration and shortcuts do not apply.
- Check that the footer says **0.7.0**. Open guided setup, continue later, reopen it and finish it. Failed saves should leave the step open with a useful error.
- Installed Windows data should be under `%LOCALAPPDATA%\Gateway\data`, and Setup should show that Gateway path. A portable launch must use its own data rather than silently switching to installed data. A busy/conflicting profile must produce an explanation without overwriting or starting empty. Custom `-data` profiles should remain where explicitly selected.
- For a later update or reinstall of a disposable profile, confirm settings, headers, cached blocks and privacy classifications remain. An update already applying must complete its authenticated restart before another installation changes the running files. Do not rename a live profile.
- Change a harmless preference, exit normally and reopen. Settings, headers and completed block coverage should remain. A second launch should reuse the running profile rather than start a competing process.
- On Windows, test start-on-sign-in separately from browser routing and serving. Turn it back off after the test if unwanted.

If an antivirus product blocks a download or installation, record the exact detection, file name and SHA-256 and stop that check. Windows binaries are unsigned. A local clean scan or a verdict about another file is not general clearance. Do not disable protection to complete QA.

## 2. Headers and connection controls

A header describes a block and its place in the chain. Having headers does **not** mean every block has been downloaded. The bundled snapshot is a starting point; Gateway validates it and asks peers for newer headers.

- Start without Core. Check the connection summary and header progress. Open its details, pause headers, resume and retry Bitcoin connections. Controls and state should agree.
- Restart after header import. Previously verified headers should remain and catch-up should continue from saved progress. With an interrupted import in a disposable fixture, restart must resume at a validated boundary, not claim partial bytes as complete headers.
- Try an unavailable peer or temporary network loss. Expect a useful waiting/error state and recovery when connectivity returns, not invented progress.
- Test **Disable outbound networking** in Settings. For a completely disconnected test, also turn Bitcoin serving off. Saved local blocks should remain readable; missing blocks should report unavailable data.
- Optional: configure a manual Bitcoin peer. Confirm it is used or produces a useful error. Remove the test entry afterwards.
- Optional Core setup: test cookie or username/password RPC authentication, a bad credential and recovery after correcting it. Saved passwords should remain masked; leaving the password field blank should preserve a stored password. Test **Maintain independent headers while Core is connected** separately from using Core as a provider.
- Optional mounted files: select an existing Core data/block directory and prepare block-file locations read-only. Pause and resume preparation, then retrieve a retained block with Core closed. No Core file or wallet should be changed. A pruned/missing file should produce an unavailable result.

## 3. Block explorer and address schema

Try these in Gateway's own address field first. Positions are zero-based; suffix addresses put the block height on the right.

| Address | Expected destination |
| --- | --- |
| `0.bitcoin` | Genesis block |
| `0.0.bitcoin` | First transaction in block zero |
| `0.0.0.bitcoin` | First output of that transaction |
| `0.0.0.0.bitcoin` | Offset zero within that output |
| `170.bitcoin` | An early block with a non-coinbase transaction to explore |
| `1.170.bitcoin` | Transaction at position one in block 170 |
| `i0.1.170.bitcoin` | Its first input |

- Open a block's transaction, input and output links, return to its containing block and use Back/Forward. Copy the resource address and reopen it in another Gateway tab.
- In a block with more than 40 transactions, test **Next 40** and **Previous**. On a transaction, test **Show/Refresh value flow**, including unavailable input values; unknown values must stay unknown. Try raw `txid:vout` and `txid:vout:offset` copied from known transaction data, supplying block context when needed.
- Inspect source and verification details. A peer, cache, mounted file and Core are different data sources; header anchoring does not claim all of Core's script/UTXO consensus checks.
- Try a transaction ID copied from a known block. If local evidence cannot locate it, supply the containing-block hint. A missing global transaction index must not prevent viewing transactions inside an available block.
- Try a valid block hash and a known transaction/output coordinate. Invalid input, an out-of-range position and unavailable data should produce understandable errors; no blank page or false “does not exist” conclusion.
- Follow input references where supporting blocks are available. Global spender, address-history, sat-history and inscription actions should remain locked. A position inside an output does not establish current ownership or a sat's full history.
- Test a recent block and one historical block. Some Bitcoin peers lack old blocks; record the peer/connection state if a request waits or fails, then retry.

## 4. Browser registration and real `.bitcoin` address entry

This is a separate test from typing into Gateway. Test each installed browser/profile you intend to support: Chrome, Edge, Brave or Chromium. Windows system routing and browser companion approval are separate steps.

1. Open **Settings → Set up browser access**. Enable Gateway browser routing and approve the Windows permission prompt if you intend to install routing. If cancelled, setup should show which part remains incomplete and let you retry or continue deliberately.
2. Select the correct browser and profile. Use **Copy extensions address & open browser**, then press **Ctrl+L, Ctrl+V, Enter** in that browser. It should reach its Extensions page, for example `chrome://extensions/`. Chromium prevents another app from opening this internal page directly. If clipboard access fails, copy the displayed address manually; a launch error must be shown honestly.
3. Enable **Developer mode**, choose **Load unpacked**, and select the `browser-companion` folder shown by Gateway. **Open companion folder** should point to that exact folder. Use the companion from the 0.7.0 package; an already-loaded older unpacked extension may need Reload or its path updated.
4. Open the companion's popup and choose **Open Gateway**. Check that setup reports an authenticated native exchange. Registration alone should not falsely report a successful exchange.
5. In the **browser address bar**, enter `0.bitcoin`, then `0.0.bitcoin` and `0.0.0.bitcoin`. Each should reach the corresponding Gateway resource. Repeat after closing and reopening Gateway to test activation.
6. Also test explicit `http://0.bitcoin/`. This checks local domain routing separately from the browser deciding that bare text is a search. An HTTPS-only browser prompt or a ports 53/80 conflict should be recorded, not confused with block availability.
7. Test `.gateway`/`settings.gateway` navigation and a future `.bitmap` address. The latter should show an unavailable/locked result, without enabling extra indexes.
8. Disable browser routing through setup and confirm Gateway's own address field still works. Re-enable it only if wanted. Test companion removal/reapproval in a disposable browser profile if available.

The companion recovers exact addresses when supported Chromium search pages receive them. Ordinary search phrases must remain ordinary searches. A search provider may receive a bare address before recovery; this is not private DNS or anonymity. Record browser, profile, search provider and whether bare versus explicit HTTP entry failed. Windows integration is not supported on Linux in this release.

## 5. Indexes, jobs and storage

- Open **Indexes**. Headers and Bitcoin Blocks should be available. Merely opening a card must not start a large build.
- Open Blocks details and inspect the recipe, dependencies, rule identity, schema and coverage. These should agree with the shared address/schema behavior in the explorer.
- Build a small explicit range, such as 0–2, and inspect its committed records. Test pause/resume and restart recovery. Changing the requested range must not erase previously committed coverage. A controlled reorganization fixture should preserve old-branch evidence and count only selected-chain coverage; do not manufacture a reorganization in a working profile.
- Test records pagination and automatic following, then disable following and confirm your chosen page stays put. Queue another small range while a build is active and check the displayed job order/state.
- Test Blocks **On/Off** and **Live**. The Live switch starts work immediately from the saved/default start, which can be genesis. Configure and review a suitable bounded start in **Build a range** first; an existing saved start stays fixed. Stop after observing progress. The current batch, committed coverage and chain tip must be distinguishable; a completed batch is not necessarily complete catch-up.
- Open `activity.gateway` to inspect current/saved block work. Open `storage.gateway` to review source retention. Missing sources should be reported as missing, not treated as verified records.
- Test the retention choices with small ranges: **ephemeral** does not add new source blocks to cache, **cache** keeps a bounded working set, and **retain** keeps sources outside cache eviction. Existing cached/retained/external data must not be deleted by choosing ephemeral. Derived records/checkpoints and raw blocks have different lifetimes.
- In Settings, test cache enabled/disabled, its size limit and an optional retained-block directory using a test folder. Existing Core files must remain untouched.
- Confirm extra indexes stay locked: transaction locator, spender/address indexes, Sat, Satline, inscriptions, Bitmap and **Bitmap: Terrain Claim Chains**. Clicking a lock should explain it. The renamed Bitmap card introduces no new active feature.

## 6. Private cache and Bitcoin serving

- **Private cache** must be one On/Off switch in setup and Settings, keyboard-operable and persistent after Save/restart. There should be no competing private-cache preset buttons. All switches should be green **On**, red **Off**, and neutral grey when disabled, with readable state words and a moving thumb in both themes. Test keyboard Space and visible focus.
- Turning it on makes newly cached objects private. Turning it off must not make older private objects public.
- **Serve Bitcoin data to Bitcoin peers** must be available independently of the locked **Serve data to Gateway peers**. Test Save/restart, On and Off; Gateway sharing must remain locked throughout.
- Serving permission is not proof that the node has complete history or is reachable from the Internet. Inspect listener/readiness state. Full history and pruned/recent history must be labelled according to actual available data, never inferred from the toggle alone.
- Advanced, with a second local Bitcoin-protocol test peer: disable Core, mounted and archive providers for a cache-only test and enable **Allow serving public cached blocks**. Request a verified public cached block, then a missing block and one cached privately. Public eligible data should be returned; missing/private data should be refused without triggering a download on the requester's behalf. A separately enabled ready Core may serve its own copy of the same public chain block without changing a private cache object's classification. Turning serving off must prevent further service. Record listening port and local firewall effects without opening router ports just for this test.
- Optional Core matrix: repeat with a fully synced unpruned Core node, a ready pruned node with sufficient recent coverage, and an unavailable/syncing provider. Announced full/limited service must track actual readiness. Gateway remains a block/header server, not a replacement for full Core consensus validation.

## 7. Peers and everyday UI

- Open **Peers**. Bitcoin connections should initially show friendly aliases rather than raw addresses. Click an alias to reveal the address, then hide it. Test Tab plus Enter/Space too.
- Wait through automatic refresh. Aliases should remain stable during the current app page/session and a deliberate reveal should stay usable. Leaving and reopening Peers should conceal addresses again. A new app page/session may assign new aliases.
- Disconnect one peer and confirm it is the intended connection, even after a refresh or row reordering. The connection manager may replace it with another connection.
- Technical diagnostics may show addresses when deliberately expanded. Aliases do not hide your IP or requests from network peers.
- Test navigation among Home, Explorer, Indexes, Peers and Settings, light/dark/system appearance, keyboard focus, a narrow window and readable errors. No horizontal overflow, lost controls or unrequested downloads should occur.
- On Windows, test the native notification-area icon: Open Gateway, Settings, Bitcoin serving and Quit. Closing Gateway or finishing an update should remove the old icon; relaunch should not leave duplicate tray processes. Optional DNS routing must still request Windows approval, report cancellation honestly, and change only Gateway's own routing rules.

## 8. Updates, shutdown and removal

- On a fresh installation, confirm the bundled Gateway hosted publisher (Render) is configured from first launch and **Check automatically; let me install** is the default. No GitHub credential, signing key or manual publisher setup should be needed.
- Test each installer choice on a disposable profile: manual updates, automatic checks with user-controlled installation, and automatic download/installation. On an upgrade, **Keep existing update preferences** must retain the current publisher and mode. An explicitly selected new mode must apply once; later Settings changes must survive subsequent restarts.
- Expand the installer's source settings. Keeping the source must preserve a saved source; choosing Gateway's hosted service must use the bundled verified channel. With a disposable independently trusted test publisher, enter its HTTPS base URL and public key, explicitly confirm the key, and check the final review. Editing URL/key must clear confirmation. A missing/invalid URL, malformed key or absent confirmation must block installation before files are written. No private signing key or GitHub credential should be requested.
- Check the chosen mode and source on first launch and after restart. Later Settings changes must not be overwritten by a consumed installer request. If an update is already staged, a conflicting installer source change should remain pending without an unattended install; completing or cancelling that staged update must preserve its original verification authority.
- Open **Settings → Updates** and check. Allow the hosted service time to wake up after inactivity; a failed check must show a clear network error. A successful check must identify the installed version and the signed publisher version. Match those against the approved release being tested; Gateway must not downgrade itself or call a failed network check “up to date”.
- After an explicit source change on a disposable profile, confirm an old restricted-distribution credential is not sent to the new publisher. Previously consumed installer choices must not reapply after a later Settings save. Record any staged/recovery work that caused a source request to remain pending.
- In manual mode, checks/downloads/installation require user actions. In automatic-check mode, startup and scheduled checks can offer an update, but do not download or restart by themselves.
- Test automatic-install mode only with an approved newer signed test package. Gateway should check, verify, download, install and restart automatically, preserving settings, headers and local data. Withdraw automatic-install permission while download/preparation is pending and confirm it does not restart automatically. Once installation has committed, the screen should explain that it is completing the restart.
- A failed signature, altered download or failed installation must not cause an unattended retry/restart loop. Manual retry remains a deliberate tester action. App updates must not download the full header snapshot again. Test outbound-network disable during a pending check/download; a previously verified stage may still be installed offline while its signed authorization remains valid.
- **Check for updates** also checks signed header delivery in the background; there is no separate header-update button. Observe saved header/bootstrap progress when newer signed data is available. No newer headers is a valid result. Only missing chunks should be requested; ahead or divergent history must remain intact. Normal Bitcoin header synchronization should still work independently.
- Maintainer acceptance: verify application/header metadata expiration and renewal against the unchanged approved artifact identities, and test a renewal failure's visible reporting. Do not mark automation complete from the presence of a workflow file alone.
- Close the app normally, then relaunch through its shortcut/portable launcher and browser activation. Test uninstall on a disposable installed profile, reviewing data-retention choices first. Do not remove a working profile just to satisfy a checklist.

## Report a problem

Include the check number, exact steps, expected/actual result, package name, version, OS/browser/profile, Core or standalone mode, network/serving/private-cache settings and whether it reproduces after restart. For a block lookup include the public resource address and displayed verification/coverage state. For installation include the exact error or antivirus detection.

Screenshots and short logs help; redact local usernames, IPs you do not want to share, credentials and tokens. Never attach an entire data profile. Send ordinary feedback through [GitHub Issues](https://github.com/Blockamoto/gateway/issues); use [security reporting](../SECURITY.md) for sensitive findings.
