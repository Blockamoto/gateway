'use strict';

(async () => {
  const candidate = globalThis.GatewaySearch && GatewaySearch.extractCandidate(location.href);
  if (!candidate) return;
  try {
    const result = await chrome.runtime.sendMessage({ type: 'validateGatewayAddress', address: candidate });
    if (!result || !result.valid || !result.web_url) return;
    // Record recovery before leaving the search page. This is local-only telemetry
    // used by Gateway Client diagnostics; no ordinary search history is sent.
    try { await chrome.runtime.sendMessage({ type: 'recordRecovery', address: result.address }); } catch (_) {}
    location.replace(result.web_url);
  } catch (_) {
    // Gateway Client may simply not be running. In that case the search remains
    // untouched rather than degrading normal browser behaviour.
  }
})();
