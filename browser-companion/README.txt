Gateway Browser Companion 0.5.0

Install Gateway Client first. The installer registers a narrowly allowed native
messaging host. This no longer depends on gateway.bitcoin or working local DNS.

ALPHA CHROME / EDGE / BRAVE SETUP
1. Open the browser profile you actually use, then its Extensions page.
2. Enable Developer mode. Remove the older unpacked Gateway companion, if any.
3. Load unpacked and select the installed browser-companion folder.
4. Pin the extension. Open its popup. It must say Connected to Gateway 0.5.0.
5. Type 0.bitcoin, or open .gateway from the popup.

The manifest includes a stable PUBLIC key to keep its alpha extension ID stable.
Native messaging only permits that extension ID. Installation is not considered
complete until Gateway receives a real native-host handshake.

This development build is not in the Chrome Web Store. Approval in each browser
profile is required. No browser policy or default search setting is changed.

Privacy: a search engine may receive an exact-address query before recovery.
Use the popup or god:// activation to avoid that search fallback.
