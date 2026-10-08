(function (root) {
  'use strict';

  function oneToken(value) {
    const q = String(value || '').trim();
    if (!q || /\s/.test(q) || q.length > 253) return '';
    if (/[\/?#]/.test(q)) return '';
    if (!q.includes('.')) return '';
    return q.toLowerCase();
  }

  function extractCandidate(href) {
    let u;
    try { u = new URL(href); } catch (_) { return ''; }
    const host = u.hostname.toLowerCase();
    const path = u.pathname;
    let q = '';

    if ((host === 'www.google.com' || host === 'www.google.co.uk') && path === '/search') {
      q = u.searchParams.get('q') || '';
    } else if (host === 'www.bing.com' && path === '/search') {
      q = u.searchParams.get('q') || '';
    } else if (host === 'duckduckgo.com' && (path === '/' || path === '/html/' || path === '/html')) {
      q = u.searchParams.get('q') || '';
    } else if (host === 'search.brave.com' && path === '/search') {
      q = u.searchParams.get('q') || '';
    } else {
      return '';
    }
    return oneToken(q);
  }

  function matchesNamespace(address, namespaces) {
    const q = oneToken(address);
    if (!q) return false;
    return (namespaces || []).some(n => {
      const suffix = typeof n === 'string' ? n : n && n.suffix;
      return typeof suffix === 'string' && /^\.[a-z][a-z0-9-]*$/.test(suffix) &&
        (q === suffix || q.endsWith(suffix));
    });
  }
  const api = { extractCandidate, oneToken, matchesNamespace };
  root.GatewaySearch = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
