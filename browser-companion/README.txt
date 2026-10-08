Gateway Browser Companion

Browser integration is optional. Gateway's own address bar works without it.
Native browser registration is supported on Windows in this release.

CHROME / EDGE / BRAVE / CHROMIUM SETUP
1. Start Gateway and open the Browser access step in setup.
2. Choose Enable Gateway browser routing. This registers the native messaging
   host and Gateway links. Optional Windows namespace routing may ask for
   administrator approval. Installing Gateway alone does not finish this step.
3. Choose the browser and profile you actually use. Select Copy extensions
   address & open browser, then paste the displayed address into that browser's
   address bar and press Enter. Chrome uses chrome://extensions/; other browsers
   have their own Extensions page. External links cannot open that page directly.
4. Enable Developer mode, choose Load unpacked, and select the browser-companion
   folder shown in Gateway. Pin the extension to the browser toolbar.
5. Open its popup. Check that it says Connected to Gateway, choose Open Gateway,
   and open 0.bitcoin. Return to setup and choose Check readiness.
6. Try 0.bitcoin in the browser's address bar and confirm the block opens in
   Gateway. Also try 0.0.bitcoin to open transaction zero within block zero.

The companion is supplied as an unpacked extension, not through a browser store.
Approval in each browser profile is required. No browser policy or default search
setting is changed. Keep the companion and native host from the same Gateway
installation together; portable users should repeat setup after moving the folder.

The manifest contains a stable PUBLIC key to preserve the extension identity.
Native messaging permits only that extension ID. A real native-host handshake
and a displayed resource establish readiness, not merely loading the extension.

Privacy: when a browser treats an address as a search, the search provider may
receive it before the companion recovers it. Use Gateway's own address bar or
the companion popup to avoid that search fallback. Gateway must still retrieve
missing block data from its configured sources.
