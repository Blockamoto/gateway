# Getting started with Gateway

Gateway lets you inspect Bitcoin blocks on demand. You do not need a wallet, Bitcoin Core or a full blockchain download.

## 1. Choose a package

- **Windows installer:** run `gateway-client-v0.7.0-installer.exe`. Choose an application directory, review the Updates page, then install. Launch **Gateway On Demand** from its shortcut.
- **Windows portable:** extract the entire Windows **fresh** ZIP and run `GatewayOnDemand.exe`. Keep the extracted files together. `GatewayClient.exe` runs the application; `GatewayNativeHost.exe` is used by the browser companion.
- **Linux:** extract the Linux **fresh** ZIP, make `gateway-client` and `gateway-update-helper` executable, and run `./gateway-client`.

An application-only ZIP is intended for updating an existing installation and does not include the initial header snapshot. Use a fresh package for your first run. See the [release downloads](https://github.com/Blockamoto/gateway/releases) and [known issues](KNOWN-ISSUES.md).

The Windows installer stores application data under `%LOCALAPPDATA%\Gateway\data`. A portable Windows copy uses its own local data. Gateway shows the actual path in Setup and Settings; use that displayed path when backing up or diagnosing a profile. Close Gateway before copying a profile.

## 2. Finish setup

Keep the default update mode if you want automatic checks while choosing when to download and install. Automatic installation and manual checks are alternatives. The expandable source section is only needed to deliberately change publisher trust; ordinary setup needs no key, token or account.

Core is optional. Start with ordinary Bitcoin peers, allow the bundled headers to validate and let catch-up continue. Headers describe the chain; they do not mean every block has been downloaded. You can reopen guided setup later.

Review **Private cache** and **Serve Bitcoin data to Bitcoin peers**. Serving is enabled for fresh profiles and is separate from the locked Gateway sharing option. Private cache controls newly cached objects; switching it off later does not publish older private objects. See [Privacy](PRIVACY.md).

## 3. Open your first block

Type `0.bitcoin` in Gateway's address field. Open its transaction and output links. You can also try:

| Address | Meaning |
| --- | --- |
| `0.0.bitcoin` | First transaction in the genesis block |
| `0.0.0.bitcoin` | Its first output |
| `0.0.0.0.bitcoin` | Offset zero within that output |
| `1.170.bitcoin` | Transaction one in block 170 |
| `i0.1.170.bitcoin` | Its first input |

Positions are zero-based and suffix addresses put the block height on the right. A height follows Gateway's selected chain and may identify a different block after a reorganization. For an unknown transaction ID, provide its containing block when asked. Some peers cannot supply older blocks; retrying another source may help.

## 4. Optional browser address entry on Windows

1. Open **Settings → Set up browser access** and enable Gateway browser routing. Approve the Windows permission prompt if you want the local routing rules installed.
2. Select your browser and profile. Use **Copy extensions address & open browser**, then press **Ctrl+L, Ctrl+V, Enter** in that browser. This reaches its Extensions page, such as `chrome://extensions/`; another app cannot reliably open that internal page directly.
3. Enable **Developer mode**, choose **Load unpacked** and select the `browser-companion` folder shown by Gateway. This build is not installed from the Chrome Web Store.
4. Open the companion popup, choose **Open Gateway** and confirm Gateway reports an authenticated native exchange. A registered host alone is not a complete connection.
5. Enter `0.bitcoin` in the browser address bar, then `0.0.bitcoin`. Also try `http://0.bitcoin/` to distinguish local routing from the browser treating bare text as a search.

Approval applies to the browser profile you use. A search provider may receive a bare address before the companion redirects it; the popup and explicit local HTTP entry avoid that search fallback. This is not anonymity. See the [browser companion instructions](../browser-companion/README.txt) for details. Windows browser routing is unavailable on Linux in this release.

## 5. Explore further

Open **Indexes** to inspect Headers and Blocks. Merely viewing a card does not start a build. Use a small range such as blocks 0–2 before experimenting with Live catch-up, which can begin from the saved/default start and do substantial work.

Use **Settings → Updates** for the configured source, mode and a manual check. App updates preserve the profile and do not download the full header snapshot again. [Updates](UPDATES.md) explains trust and recovery.

The [QA guide](TESTING-0.7.0.md) covers every available feature. Report ordinary problems through [GitHub Issues](https://github.com/Blockamoto/gateway/issues), with version and redacted reproduction details.
