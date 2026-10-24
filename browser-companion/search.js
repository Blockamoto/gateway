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
  function resourceCandidate(address) {
    const q = oneToken(address);
    if (!matchesNamespace(q, ['.bitcoin', '.gateway', '.bitmap', '.sat'])) return '';
    if (q.endsWith('.bitmap')) {
      if (!/^(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*))?\.bitmap$/.test(q)) return '';
      if (q.slice(0, -7).split('.').some(n => BigInt(n) > 9007199254740991n)) return '';
    }
    if (q.endsWith('.sat') && !/^(?:0|[1-9][0-9]*|[a-z]+)\.sat$/.test(q)) return '';
    return q;
  }

  function directCandidate(href) {
    // Inspect the literal authority as URL normalizes explicit default ports.
    // Root resource hosts only: no credentials, port, path, query or fragment.
    if (typeof href !== 'string' || !/^https?:\/\/[^\/?#:@]+\/?$/i.test(href)) return '';
    let u;
    try { u = new URL(href); } catch (_) { return ''; }
    if (!['http:', 'https:'].includes(u.protocol) || u.username || u.password || u.port || u.search || u.hash || u.pathname !== '/') return '';
    return resourceCandidate(u.hostname);
  }
  const api = { extractCandidate, oneToken, matchesNamespace, resourceCandidate, directCandidate };
  root.GatewaySearch = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
