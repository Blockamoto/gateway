'use strict';
// Workspace geometry and draft range controls. Neither starts work or fetches data.
window.GatewayIndexWorkspace = (() => {
  const clamp = (value, min, max) => Math.max(min, Math.min(max, value));
  const height = value => Number.isSafeInteger(value) && value >= 0;
  const number = input => input.value.trim() !== '' && height(Number(input.value)) ? Number(input.value) : null;
  const format = value => value.toLocaleString('en-GB');

  function mountLayout(root, message) {
    const doc = root.ownerDocument, win = doc.defaultView;
    const inspector = root.querySelector('.timeline-inspector'), editor = root.querySelector('.timeline-editor');
    if (!inspector || !editor) return;
    if (message) inspector.prepend(message);
    editor.id = 'timeline-editor';
    const divider = doc.createElement('div');
    divider.id = 'timeline-divider'; divider.className = 'timeline-divider'; divider.tabIndex = 0;
    divider.setAttribute('role', 'separator'); divider.setAttribute('aria-orientation', 'horizontal');
    divider.setAttribute('aria-label', 'Resize inspector and timeline'); divider.setAttribute('aria-controls', 'timeline-inspector timeline-editor');
    divider.title = 'Drag to resize. Arrow keys adjust height; Home and End choose the limits. Double-click resets.';
    root.insertBefore(divider, editor);
    let ratio = .56, drag = null;
    try { const saved = Number(win.localStorage.getItem('gateway.timeline.panel-ratio')); if (saved > 0 && saved < 1) ratio = saved; } catch {}
    function bounds() {
      const available = Math.max(0, root.clientHeight - divider.offsetHeight);
      const minimum = Math.min(220, available * .52);
      return {available, minimum, maximum:Math.max(minimum, available - Math.min(130, available * .3))};
    }
    function paint(value, save = false) {
      const b = bounds(), size = clamp(value ?? b.available * ratio, b.minimum, b.maximum);
      root.style.setProperty('--timeline-panel-height', size + 'px');
      divider.setAttribute('aria-valuemin', String(Math.round(b.minimum)));
      divider.setAttribute('aria-valuemax', String(Math.round(b.maximum)));
      divider.setAttribute('aria-valuenow', String(Math.round(size)));
      divider.setAttribute('aria-valuetext', 'Timeline ' + Math.round(b.available ? size / b.available * 100 : 0) + ' percent of workspace');
      if (save && b.available) { ratio = size / b.available; try { win.localStorage.setItem('gateway.timeline.panel-ratio', String(ratio)); } catch {} }
    }
    divider.addEventListener('pointerdown', event => {
      if (event.button !== 0) return;
      event.preventDefault(); divider.focus();
      drag = {id:event.pointerId, y:event.clientY, size:editor.getBoundingClientRect().height};
      divider.setPointerCapture(event.pointerId); root.dataset.resizing = 'true';
    });
    divider.addEventListener('pointermove', event => { if (drag?.id === event.pointerId) paint(drag.size + drag.y - event.clientY, true); });
    function release(event) { if (drag?.id !== event.pointerId) return; drag = null; delete root.dataset.resizing; }
    for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) divider.addEventListener(type, release);
    divider.addEventListener('dblclick', () => { ratio = .56; paint(null, true); });
    divider.addEventListener('keydown', event => {
      const b = bounds(), current = editor.getBoundingClientRect().height, step = event.shiftKey ? 48 : 16;
      const value = {ArrowUp:current + step, ArrowDown:current - step, Home:b.minimum, End:b.maximum}[event.key];
      if (value === undefined) return; event.preventDefault(); paint(value, true);
    });
    if (win.ResizeObserver) new win.ResizeObserver(() => paint()).observe(root);
    paint();
    return {resize:() => paint()};
  }

  function mountRange({root, from, to, live}) {
    const doc = root.ownerDocument;
    const start = root.querySelector('#range-start'), end = root.querySelector('#range-end');
    const rail = root.querySelector('.build-range-rail'), summary = root.querySelector('#range-summary');
    let definition = null, snapshot = null, extent = 0, applyingHandle = false;
    function sync() {
      const d = definition, minimum = height(d?.start_height) ? d.start_height : 0;
      const tip = height(snapshot?.timeline?.tip_height) ? snapshot.timeline.tip_height : null;
      const a = number(from), b = number(to);
      const invalidFirst = from.value.trim() !== '' && a === null, invalidLast = !live.checked && to.value.trim() !== '' && b === null;
      // Keep a stable rail while editing, even before a header tip is available.
      extent = Math.max(extent, minimum, tip ?? 0, a ?? 0, b ?? 0);
      const maximum = extent;
      const first = clamp(a ?? minimum, minimum, maximum), last = clamp(b ?? tip ?? maximum, first, maximum);
      const locked = !d?.buildable || !!d.locked;
      from.min = String(minimum); to.min = String(Math.max(minimum, a ?? minimum));
      from.setCustomValidity(invalidFirst ? 'Enter a whole, non-negative block height.' : a !== null && a < minimum ? 'The first block must be at least ' + minimum + '.' : '');
      to.setCustomValidity(invalidLast ? 'Enter a whole, non-negative block height.' : !live.checked && b !== null && b < Math.max(minimum, a ?? minimum) ? 'The last block must not precede the first block.' : '');
      start.min = end.min = String(minimum); start.max = end.max = String(maximum);
      start.value = String(first); end.value = String(last);
      start.disabled = locked || from.readOnly || from.disabled || maximum === minimum;
      end.disabled = locked || live.checked || to.disabled || maximum === minimum;
      rail.hidden = live.checked;
      const span = Math.max(1, maximum - minimum);
      rail.style.setProperty('--range-start', ((first - minimum) / span * 100) + '%');
      rail.style.setProperty('--range-end', ((last - minimum) / span * 100) + '%');
      start.setAttribute('aria-valuetext', 'First block ' + format(first));
      end.setAttribute('aria-valuetext', 'Last block ' + format(last));
      if (invalidFirst || invalidLast) summary.textContent = 'Use whole, non-negative block heights for both endpoints.';
      else if (live.checked) summary.textContent = 'Follow the verified chain from block ' + format(first) + '. Review the plan before starting.';
      else if (a !== null && a < minimum || b !== null && b < Math.max(minimum, a ?? minimum)) summary.textContent = 'Choose an ordered range at or above block ' + format(minimum) + '.';
      else if (to.value === '') summary.textContent = tip === null ? 'Enter a last block or use the verified tip when work starts. The local header tip is not available yet.' : 'Last block follows the verified tip at start. Current tip: ' + format(tip) + '. Move the right handle to choose a fixed end.';
      else summary.textContent = format(last - first + 1) + ' blocks selected, including both endpoints.';
      root.dataset.start = String(first); root.dataset.end = String(last);
    }
    function change(input, target) {
      if (input.disabled) return;
      const minimum = Number(start.min), maximum = Number(start.max);
      target.value = String(input === start ? clamp(Number(input.value), minimum, number(to) ?? maximum) : clamp(Number(input.value), number(from) ?? minimum, maximum));
      applyingHandle = true;
      try { target.dispatchEvent(new doc.defaultView.Event('input', {bubbles:true})); } finally { applyingHandle = false; }
      sync();
    }
    start.addEventListener('input', () => change(start, from));
    end.addEventListener('input', () => change(end, to));
    function numberChanged() { if (!applyingHandle) extent = 0; sync(); }
    from.addEventListener('input', numberChanged); to.addEventListener('input', numberChanged); live.addEventListener('change', sync);
    return {update(d, s) { if (d?.id !== definition?.id) extent = 0; definition = d; snapshot = s; sync(); }, sync};
  }
  return {mountLayout, mountRange};
})();
