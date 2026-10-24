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
7. In a Sats-enabled Gateway, try 0.sat or 5000000000.sat to open that sat's
   explorer view. Ordinal names also work explicitly, for example
   nvtdijuwxlp.sat for Sat 0. The native resolver validates each name. Bare-number
   or bare-word web searches and phrases such as "what is 0.sat" remain ordinary searches.
   Gateway's own address bar separately interprets a bare number against its
   selected chain tip; the companion does not apply that rule to web searches.
8. Bitmap Standard addresses include district 0.bitmap and parcel
   0.792435.bitmap. Leading-zero or malformed Bitmap tokens remain untouched.
   Native/browser coordinates are limited to JavaScript's exact integer range;
   this implementation bound is separate from the published Bitmap rules.

DIRECT RESOURCE URLS
The companion also recognizes exact http/https root resource hosts, including
https://0.792435.bitmap/. It never redirects a URL with a port, credentials,
path, query, or fragment. Only the main frame is eligible. A new navigation or
closed tab cancels a pending handoff. Namespace metadata comes from a fresh
authenticated native status handshake; an old cached list is not blindly used.
The webNavigation permission supports this handoff; no broad host permission,
browser policy, DNS rule, or default search setting is added by the extension.

ISOLATED REVIEW PACKAGES
If this folder contains the separate preview extension identity, follow
TESTING-PREVIEW.md instead of the production registration steps above.

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
