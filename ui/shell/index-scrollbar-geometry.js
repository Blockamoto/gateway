'use strict';
// Visual scrollbar geometry only. Actual ranges remain inclusive integer positions.
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GatewayIndexScrollbarGeometry = api;
})(typeof window === 'object' ? window : null, function () {
  const clamp = (n, low, high) => Math.max(low, Math.min(high, n));
  const finite = (n, fallback) => Number.isFinite(n) ? n : fallback;
  function metrics(total, length, options = {}) {
    total = Math.max(1, finite(total, 1));
    length = Math.max(0, finite(length, 0));
    // Always reserve travel for the continuous curve, even in a narrow bar.
    const minSize = Math.min(Math.max(1, finite(options.minThumb, 72)), length / 2);
    return {total, length, minSize};
  }
  function spanToSize(span, total, length, options) {
    const m = metrics(total, length, options);
    if (m.total === 1) return m.length;
    span = clamp(finite(span, 1), 1, m.total);
    return m.minSize + (m.length - m.minSize) * Math.log(span) / Math.log(m.total);
  }
  function sizeToSpan(size, total, length, options) {
    const m = metrics(total, length, options);
    if (m.total === 1 || m.length === 0) return 1;
    const fraction = clamp((finite(size, m.minSize) - m.minSize) / (m.length - m.minSize), 0, 1);
    return clamp(Math.exp(fraction * Math.log(m.total)), 1, m.total);
  }
  function bounds(view, extent) {
    extent = Number.isSafeInteger(extent) && extent >= 0 && extent < Number.MAX_SAFE_INTEGER ? extent : 0;
    const from = clamp(Number.isSafeInteger(view?.from) ? view.from : 0, 0, extent);
    const to = clamp(Number.isSafeInteger(view?.to) ? view.to : extent, from, extent);
    return {from, to, span: to - from + 1, total: extent + 1};
  }
  function geometry(view, extent, length, options) {
    const actual = bounds(view, extent), m = metrics(actual.total, length, options);
    const size = spanToSize(actual.span, actual.total, m.length, options);
    const travel = Math.max(0, m.length - size);
    const offset = actual.span < actual.total ? actual.from / (actual.total - actual.span) * travel : 0;
    return {...actual, length:m.length, minSize:m.minSize, size, travel, offset, start:offset, end:offset + size, handleSize:Math.min(28, m.minSize)};
  }
  function pan(view, extent, length, delta, options) {
    const g = geometry(view, extent, length, options);
    if (!g.travel || !Number.isFinite(delta)) return {from:g.from, to:g.to};
    const from = clamp(g.from + Math.round(delta / g.travel * (g.total - g.span)), 0, g.total - g.span);
    return {from, to:from + g.span - 1};
  }
  function resize(view, extent, length, edge, delta, options) {
    const g = geometry(view, extent, length, options);
    if (!g.length || !Number.isFinite(delta) || !['start', 'end'].includes(edge) || delta === 0) return {from:g.from, to:g.to};
    const atStart = edge === 'start';
    const maxSpan = atStart ? g.to + 1 : g.total - g.from;
    const position = span => {
      const size = spanToSize(span, g.total, g.length, options);
      const from = atStart ? g.to + 1 - span : g.from;
      const offset = span < g.total ? from / (g.total - span) * (g.length - size) : 0;
      return atStart ? offset : offset + size;
    };
    const target = (atStart ? g.start : g.end) + delta;
    const first = position(1), last = position(maxSpan);
    const wanted = clamp(target, Math.min(first, last), Math.max(first, last));
    let low = 1, high = maxSpan;
    // Invert the rendered edge, not a linear fraction of the Bitcoin extent.
    // This includes the thumb's positional shift and keeps the opposite actual
    // endpoint fixed. Integer rounding is applied only after this inverse.
    for (let i = 0; i < 64; i++) {
      const mid = (low + high) / 2, pixel = position(mid);
      if (atStart ? pixel > wanted : pixel < wanted) low = mid;
      else high = mid;
    }
    const span = clamp(Math.round((low + high) / 2), 1, maxSpan);
    return atStart ? {from:g.to - span + 1, to:g.to} : {from:g.from, to:g.from + span - 1};
  }
  return Object.freeze({spanToSize, sizeToSpan, geometry, pan, resize});
});
