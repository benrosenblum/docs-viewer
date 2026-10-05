// Graph view: a canvas force-directed layout of the vault's links.
// Exposes window.DocsGraph.mount(container), unmount(), and reload(). The
// script mounts itself on #graph-view when it loads, so it works both as a
// page script and when viewer.js injects it after client-side navigation.
'use strict';
(function () {
  const STORE_KEY = 'docsview:graph';
  const LINK_DISTANCE = 60;
  const CHARGE = -260;
  const GRAVITY = 0.045;
  const VELOCITY_KEEP = 0.6;
  const ALPHA_MIN = 0.002;
  const ALPHA_DECAY = 1 - Math.pow(ALPHA_MIN, 1 / 300);
  const MIN_ZOOM = 0.08;
  const MAX_ZOOM = 6;
  // Labels fade in between LABEL_ZOOM and LABEL_ZOOM + LABEL_FADE.
  const LABEL_ZOOM = 0.65;
  const LABEL_FADE = 0.55;
  const CLICK_SLOP = 4;
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

  function readSettings() {
    const defaults = {outside: true, unresolved: true};
    try {
      const saved = JSON.parse(localStorage.getItem(STORE_KEY) || 'null');
      if (saved && typeof saved === 'object') return Object.assign(defaults, saved);
    } catch (err) {
      // Storage is optional.
    }
    return defaults;
  }

  function writeSettings(settings) {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify(settings));
    } catch (err) {
      // Storage is optional.
    }
  }

  function nodeURL(id) {
    return '/' + id.split('/').map(encodeURIComponent).join('/');
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text != null) node.textContent = text;
    return node;
  }

  function createGraph(container) {
    const cleanups = [];
    const controller = new AbortController();
    const settings = readSettings();
    let canvas = container.querySelector('canvas');
    if (!canvas) {
      canvas = document.createElement('canvas');
      canvas.id = 'graph-canvas';
      container.prepend(canvas);
    }
    canvas.setAttribute('role', 'img');
    canvas.setAttribute('aria-label', 'Graph of linked notes');
    canvas.tabIndex = 0;
    const ctx = canvas.getContext('2d');
    let controls = container.querySelector('.graph-controls');
    if (!controls) {
      controls = el('div', 'graph-controls');
      container.append(controls);
    }

    let nodes = [];
    let links = [];
    let byId = new Map();
    let visibleNodes = [];
    let visibleLinks = [];
    const view = {x: 0, y: 0, k: 1};
    let width = 0;
    let height = 0;
    let dpr = 1;
    let colors = {};
    let alpha = 1;
    let alphaTarget = 0;
    let frame = 0;
    let hover = null;
    let userMoved = false;
    let destroyed = false;
    const pointers = new Map();
    let gesture = null;
    let message = null;
    let stats = null;

    function on(target, type, handler, options) {
      target.addEventListener(type, handler, options);
      cleanups.push(() => target.removeEventListener(type, handler, options));
    }

    // ----- Colours and size -------------------------------------------------

    function readColors() {
      const style = getComputedStyle(container);
      const pick = (name, fallback) => style.getPropertyValue(name).trim() || fallback;
      colors = {
        node: pick('--graph-node', '#8c8c8c'),
        outside: pick('--graph-node-outside', '#00bfbc'),
        unresolved: pick('--graph-node-unresolved', '#c4c4c4'),
        focused: pick('--graph-node-focused', '#8b6cef'),
        line: pick('--graph-line', 'rgba(0,0,0,0.16)'),
        lineHighlight: pick('--graph-line-highlight', '#8b6cef'),
        text: pick('--graph-text', '#333'),
        background: pick('--bg-primary', '#fff'),
        font: style.fontFamily || 'sans-serif',
      };
    }

    function resize() {
      const rect = container.getBoundingClientRect();
      const nextWidth = Math.max(1, Math.round(rect.width));
      const nextHeight = Math.max(1, Math.round(rect.height));
      const nextDpr = window.devicePixelRatio || 1;
      if (nextWidth === width && nextHeight === height && nextDpr === dpr) return;
      if (width && height) {
        // Keep the world point at the centre of the view in place.
        view.x += (nextWidth - width) / 2;
        view.y += (nextHeight - height) / 2;
      }
      width = nextWidth;
      height = nextHeight;
      dpr = nextDpr;
      canvas.width = Math.round(width * dpr);
      canvas.height = Math.round(height * dpr);
      canvas.style.width = width + 'px';
      canvas.style.height = height + 'px';
      draw();
    }

    // ----- Data ---------------------------------------------------------------

    function radius(node) {
      return Math.min(16, 3.5 + Math.sqrt(node.degree) * 1.7);
    }

    function setData(data, previous) {
      const rawNodes = Array.isArray(data && data.nodes) ? data.nodes : [];
      const rawLinks = Array.isArray(data && data.links) ? data.links : [];
      byId = new Map();
      nodes = rawNodes.map((raw, i) => {
        const old = previous && previous.get(raw.id);
        // Phyllotaxis start positions spread nodes evenly and deterministically.
        const r = 12 * Math.sqrt(0.5 + i);
        const angle = i * Math.PI * (3 - Math.sqrt(5));
        const node = {
          id: String(raw.id),
          name: String(raw.name || raw.id),
          kind: raw.kind === 'outside' || raw.kind === 'unresolved' ? raw.kind : 'note',
          degree: 0,
          x: old ? old.x : r * Math.cos(angle),
          y: old ? old.y : r * Math.sin(angle),
          vx: 0,
          vy: 0,
          fx: null,
          fy: null,
          neighbors: new Set(),
          links: [],
          r: 4,
        };
        byId.set(node.id, node);
        return node;
      });
      links = [];
      const seen = new Set();
      for (const raw of rawLinks) {
        const source = byId.get(String(raw.source));
        const target = byId.get(String(raw.target));
        if (!source || !target || source === target) continue;
        const key = source.id + '\u0000' + target.id;
        if (seen.has(key)) continue;
        seen.add(key);
        const link = {source, target};
        links.push(link);
        source.neighbors.add(target);
        target.neighbors.add(source);
        source.links.push(link);
        target.links.push(link);
      }
      for (const [i, node] of nodes.entries()) {
        const given = Number(rawNodes[i].degree);
        node.degree = Number.isFinite(given) && given > 0 ? given : node.neighbors.size;
        node.r = radius(node);
      }
      applyFilters(previous ? 0.3 : 1);
    }

    function isVisible(node) {
      if (node.kind === 'outside') return settings.outside;
      if (node.kind === 'unresolved') return settings.unresolved;
      return true;
    }

    function applyFilters(heat) {
      visibleNodes = nodes.filter(isVisible);
      visibleLinks = links.filter((link) => isVisible(link.source) && isVisible(link.target));
      for (const node of visibleNodes) node.visibleDegree = 0;
      for (const link of visibleLinks) {
        link.source.visibleDegree++;
        link.target.visibleDegree++;
      }
      if (hover && !isVisible(hover)) hover = null;
      updateStats();
      reheat(heat);
    }

    function updateStats() {
      if (!stats) return;
      const noteCount = visibleNodes.length;
      stats.textContent = noteCount + (noteCount === 1 ? ' node, ' : ' nodes, ') + visibleLinks.length + (visibleLinks.length === 1 ? ' link' : ' links');
      canvas.setAttribute('aria-label', 'Graph of ' + stats.textContent);
    }

    // ----- Simulation ---------------------------------------------------------

    function tick() {
      alpha += (alphaTarget - alpha) * ALPHA_DECAY;
      const list = visibleNodes;
      const n = list.length;
      // Repulsion between every pair (the vault is small enough for O(n²)).
      for (let i = 0; i < n; i++) {
        const a = list[i];
        for (let j = i + 1; j < n; j++) {
          const b = list[j];
          let dx = b.x - a.x;
          let dy = b.y - a.y;
          let d2 = dx * dx + dy * dy;
          if (d2 < 1e-6) {
            dx = (Math.random() - 0.5) * 1e-3;
            dy = (Math.random() - 0.5) * 1e-3;
            d2 = dx * dx + dy * dy;
          }
          if (d2 > 250000) continue;
          d2 = Math.max(d2, 25);
          const w = (CHARGE * alpha) / d2;
          a.vx += dx * w;
          a.vy += dy * w;
          b.vx -= dx * w;
          b.vy -= dy * w;
        }
      }
      // Springs pull linked nodes towards the rest length; hubs move less.
      for (const link of visibleLinks) {
        const s = link.source;
        const t = link.target;
        let dx = t.x + t.vx - s.x - s.vx;
        let dy = t.y + t.vy - s.y - s.vy;
        const distance = Math.sqrt(dx * dx + dy * dy) || 1e-3;
        const strength = 1 / Math.min(s.visibleDegree || 1, t.visibleDegree || 1);
        const force = ((distance - LINK_DISTANCE) / distance) * alpha * strength;
        dx *= force;
        dy *= force;
        const bias = (s.visibleDegree || 1) / ((s.visibleDegree || 1) + (t.visibleDegree || 1));
        t.vx -= dx * bias;
        t.vy -= dy * bias;
        s.vx += dx * (1 - bias);
        s.vy += dy * (1 - bias);
      }
      // Gentle gravity keeps disconnected clusters on screen.
      for (const node of list) {
        node.vx -= node.x * GRAVITY * alpha;
        node.vy -= node.y * GRAVITY * alpha;
        if (node.fx != null) {
          node.x = node.fx;
          node.y = node.fy;
          node.vx = 0;
          node.vy = 0;
        } else {
          node.vx *= VELOCITY_KEEP;
          node.vy *= VELOCITY_KEEP;
          node.x += node.vx;
          node.y += node.vy;
        }
      }
    }

    function settled() {
      return alpha < ALPHA_MIN && alphaTarget === 0;
    }

    function loop() {
      frame = 0;
      if (destroyed) return;
      tick();
      if (!userMoved && alpha > 0.05) fit(false);
      draw();
      if (!settled()) frame = requestAnimationFrame(loop);
    }

    function reheat(value) {
      alpha = Math.max(alpha, value);
      if (reducedMotion.matches && alphaTarget === 0) {
        // Settle instantly instead of animating.
        for (let i = 0; i < 400 && !settled(); i++) tick();
        if (!userMoved) fit(false);
        draw();
        return;
      }
      if (!frame && !destroyed) frame = requestAnimationFrame(loop);
    }

    // ----- View transform ------------------------------------------------------

    function toWorld(sx, sy) {
      return {x: (sx - view.x) / view.k, y: (sy - view.y) / view.k};
    }

    function graphBounds(withLabels) {
      const b = {minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity};
      ctx.font = '12px ' + colors.font;
      for (const node of visibleNodes) {
        const half = withLabels ? Math.max(node.r, ctx.measureText(node.name).width / 2) : node.r;
        b.minX = Math.min(b.minX, node.x - half);
        b.minY = Math.min(b.minY, node.y - node.r);
        b.maxX = Math.max(b.maxX, node.x + half);
        b.maxY = Math.max(b.maxY, node.y + node.r + (withLabels ? 18 : 0));
      }
      return b;
    }

    function fit(animateOnly) {
      if (!visibleNodes.length || !width || !height) return;
      // Keep clear of the filter panel: beside it on wide screens, below it on narrow ones.
      const wide = width > 640;
      const reserveX = wide && controls.offsetWidth ? controls.offsetWidth + 16 : 0;
      const reserveY = !wide && controls.offsetHeight ? controls.offsetHeight + 16 : 0;
      const areaWidth = width - reserveX;
      const areaHeight = height - reserveY;
      const pad = 40;
      const scale = (b) => Math.min(MAX_ZOOM, 1.6, (areaWidth - pad * 2) / Math.max(1, b.maxX - b.minX), (areaHeight - pad * 2) / Math.max(1, b.maxY - b.minY));
      // Make room for labels only at zoom levels where they are drawn.
      let bounds = graphBounds(true);
      let k = scale(bounds);
      if (k < LABEL_ZOOM) {
        bounds = graphBounds(false);
        k = Math.min(scale(bounds), LABEL_ZOOM);
      }
      view.k = Math.max(MIN_ZOOM, k);
      view.x = areaWidth / 2 - ((bounds.minX + bounds.maxX) / 2) * view.k;
      view.y = reserveY + areaHeight / 2 - ((bounds.minY + bounds.maxY) / 2) * view.k;
      if (animateOnly !== false) draw();
    }

    function zoomAt(sx, sy, factor) {
      const k = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, view.k * factor));
      const world = toWorld(sx, sy);
      view.k = k;
      view.x = sx - world.x * k;
      view.y = sy - world.y * k;
      userMoved = true;
      draw();
    }

    // ----- Drawing ---------------------------------------------------------------

    function nodeColor(node) {
      if (node.kind === 'outside') return colors.outside;
      if (node.kind === 'unresolved') return colors.unresolved;
      return colors.node;
    }

    function draw() {
      if (destroyed || !width) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, width, height);
      ctx.save();
      ctx.translate(view.x, view.y);
      ctx.scale(view.k, view.k);
      const k = view.k;
      const focus = hover;
      const lineWidth = Math.max(0.5 / k, Math.min(1.2, 1 / Math.sqrt(k)));

      ctx.lineWidth = lineWidth;
      ctx.strokeStyle = colors.line;
      ctx.globalAlpha = focus ? 0.35 : 1;
      ctx.beginPath();
      for (const link of visibleLinks) {
        if (focus && (link.source === focus || link.target === focus)) continue;
        ctx.moveTo(link.source.x, link.source.y);
        ctx.lineTo(link.target.x, link.target.y);
      }
      ctx.stroke();
      if (focus) {
        ctx.globalAlpha = 1;
        ctx.strokeStyle = colors.lineHighlight;
        ctx.lineWidth = lineWidth * 1.6;
        ctx.beginPath();
        for (const link of focus.links) {
          if (!isVisible(link.source) || !isVisible(link.target)) continue;
          ctx.moveTo(link.source.x, link.source.y);
          ctx.lineTo(link.target.x, link.target.y);
        }
        ctx.stroke();
      }

      for (const node of visibleNodes) {
        const lit = !focus || node === focus || focus.neighbors.has(node);
        ctx.globalAlpha = lit ? 1 : 0.22;
        ctx.beginPath();
        ctx.arc(node.x, node.y, node.r, 0, Math.PI * 2);
        if (node.kind === 'unresolved') {
          ctx.fillStyle = colors.background;
          ctx.fill();
          ctx.lineWidth = Math.max(1.2, 1.5 / k);
          ctx.strokeStyle = node === focus ? colors.focused : colors.unresolved;
          ctx.stroke();
        } else {
          ctx.fillStyle = node === focus ? colors.focused : nodeColor(node);
          ctx.fill();
          if (node === focus) {
            ctx.lineWidth = 2 / k;
            ctx.strokeStyle = colors.focused;
            ctx.globalAlpha = 0.35;
            ctx.beginPath();
            ctx.arc(node.x, node.y, node.r + 3 / k, 0, Math.PI * 2);
            ctx.stroke();
          }
        }
      }

      // Labels fade in with zoom; hovered nodes and neighbours always show.
      const baseAlpha = Math.max(0, Math.min(1, (k - LABEL_ZOOM) / LABEL_FADE));
      const fontSize = 12 / Math.max(0.75, Math.min(k, 1.6));
      ctx.textAlign = 'center';
      ctx.textBaseline = 'top';
      ctx.fillStyle = colors.text;
      // A halo in the background colour keeps labels readable over links and nodes.
      ctx.strokeStyle = colors.background;
      ctx.lineWidth = 3 / k;
      ctx.lineJoin = 'round';
      for (const node of visibleNodes) {
        const lit = focus && (node === focus || focus.neighbors.has(node));
        const labelAlpha = focus ? (lit ? 1 : baseAlpha * 0.2) : baseAlpha;
        if (labelAlpha <= 0.02) continue;
        ctx.globalAlpha = labelAlpha;
        ctx.font = (node === focus ? '600 ' : '') + fontSize + 'px ' + colors.font;
        const y = node.y + node.r + 3 / Math.min(k, 1);
        ctx.strokeText(node.name, node.x, y);
        ctx.fillText(node.name, node.x, y);
      }
      ctx.restore();
      ctx.globalAlpha = 1;
    }

    // ----- Interaction -------------------------------------------------------------

    function pointerPosition(event) {
      const rect = canvas.getBoundingClientRect();
      return {x: event.clientX - rect.left, y: event.clientY - rect.top};
    }

    function hitTest(sx, sy) {
      const p = toWorld(sx, sy);
      const slop = 4 / view.k;
      for (let i = visibleNodes.length - 1; i >= 0; i--) {
        const node = visibleNodes[i];
        const dx = node.x - p.x;
        const dy = node.y - p.y;
        const r = node.r + slop;
        if (dx * dx + dy * dy <= r * r) return node;
      }
      return null;
    }

    function setHover(node) {
      if (node === hover) return;
      hover = node;
      container.classList.toggle('is-over-node', !!node && node.kind !== 'unresolved');
      canvas.title = node ? (node.kind === 'unresolved' ? node.name + ' (no file yet)' : node.id) : '';
      draw();
    }

    function openNode(node) {
      if (!node || node.kind === 'unresolved') return;
      location.assign(nodeURL(node.id));
    }

    function onPointerDown(event) {
      if (event.button !== 0 && event.pointerType === 'mouse') return;
      canvas.setPointerCapture(event.pointerId);
      const p = pointerPosition(event);
      pointers.set(event.pointerId, p);
      if (pointers.size === 2) {
        const [a, b] = Array.from(pointers.values());
        if (gesture && gesture.node) {
          gesture.node.fx = null;
          gesture.node.fy = null;
          alphaTarget = 0;
        }
        gesture = {pinch: true, distance: Math.hypot(a.x - b.x, a.y - b.y) || 1, k: view.k, moved: true};
        return;
      }
      if (pointers.size > 2) return;
      const node = hitTest(p.x, p.y);
      if (node) {
        gesture = {node, start: p, moved: false};
        node.fx = node.x;
        node.fy = node.y;
        setHover(node);
      } else {
        gesture = {pan: true, start: p, viewX: view.x, viewY: view.y, moved: false};
        container.classList.add('is-panning');
      }
    }

    function onPointerMove(event) {
      const p = pointerPosition(event);
      if (pointers.has(event.pointerId)) pointers.set(event.pointerId, p);
      if (!gesture) {
        if (event.pointerType === 'mouse') setHover(hitTest(p.x, p.y));
        return;
      }
      if (gesture.pinch && pointers.size >= 2) {
        const [a, b] = Array.from(pointers.values());
        const distance = Math.hypot(a.x - b.x, a.y - b.y) || 1;
        const cx = (a.x + b.x) / 2;
        const cy = (a.y + b.y) / 2;
        zoomAt(cx, cy, (gesture.k * (distance / gesture.distance)) / view.k);
        return;
      }
      if (!gesture.start) return;
      const dx = p.x - gesture.start.x;
      const dy = p.y - gesture.start.y;
      if (!gesture.moved && Math.hypot(dx, dy) > CLICK_SLOP) {
        gesture.moved = true;
        if (gesture.node) {
          alphaTarget = 0.3;
          reheat(0.3);
        }
      }
      if (!gesture.moved) return;
      if (gesture.node) {
        const w = toWorld(p.x, p.y);
        gesture.node.fx = w.x;
        gesture.node.fy = w.y;
        if (!frame) draw();
      } else if (gesture.pan) {
        view.x = gesture.viewX + dx;
        view.y = gesture.viewY + dy;
        userMoved = true;
        draw();
      }
    }

    function onPointerUp(event) {
      pointers.delete(event.pointerId);
      if (canvas.hasPointerCapture(event.pointerId)) canvas.releasePointerCapture(event.pointerId);
      const current = gesture;
      if (pointers.size > 0) {
        if (current && current.pinch) gesture = null;
        return;
      }
      gesture = null;
      container.classList.remove('is-panning');
      if (!current) return;
      if (current.node) {
        current.node.fx = null;
        current.node.fy = null;
        alphaTarget = 0;
        if (!current.moved && event.type === 'pointerup') openNode(current.node);
        else reheat(0.05);
      }
      if (event.pointerType !== 'mouse') setHover(null);
    }

    function onWheel(event) {
      event.preventDefault();
      const p = pointerPosition(event);
      const scale = event.deltaMode === 1 ? 0.05 : event.deltaMode === 2 ? 0.5 : 0.0022;
      const factor = Math.exp(-event.deltaY * scale * (event.ctrlKey ? 2.5 : 1));
      zoomAt(p.x, p.y, factor);
    }

    function onKey(event) {
      if (event.key === '+' || event.key === '=') zoomAt(width / 2, height / 2, 1.25);
      else if (event.key === '-' || event.key === '_') zoomAt(width / 2, height / 2, 0.8);
      else if (event.key === '0') {
        userMoved = false;
        fit();
      } else return;
      event.preventDefault();
    }

    // ----- Controls ------------------------------------------------------------------

    function buildControls() {
      controls.replaceChildren();
      controls.append(el('p', 'graph-controls-title', 'Filters'));
      const toggle = (key, label, swatchClass) => {
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.checked = !!settings[key];
        input.dataset.filter = key;
        on(input, 'change', () => {
          settings[key] = input.checked;
          writeSettings(settings);
          applyFilters(0.4);
        });
        const row = el('label', 'graph-toggle');
        row.append(input, el('span', 'graph-toggle-label', label), el('span', 'graph-swatch ' + swatchClass));
        controls.append(row);
      };
      toggle('outside', 'Outside vault', 'is-outside');
      toggle('unresolved', 'Unresolved links', 'is-unresolved');
      const footer = el('div', 'graph-controls-footer');
      stats = el('span', 'graph-stats', '');
      const fitButton = el('button', null, 'Fit');
      fitButton.type = 'button';
      fitButton.title = 'Fit the graph to the view (0)';
      on(fitButton, 'click', () => {
        userMoved = false;
        fit();
      });
      footer.append(stats, fitButton);
      controls.append(footer);
      updateStats();
    }

    function showMessage(text) {
      if (!message) {
        message = el('p', 'graph-message');
        container.append(message);
      }
      message.textContent = text;
      message.hidden = !text;
    }

    // ----- Loading ----------------------------------------------------------------------

    async function load(previous) {
      let data;
      try {
        const response = await fetch('/_/api/graph', {headers: {Accept: 'application/json'}, credentials: 'same-origin', signal: controller.signal});
        if (!response.ok) throw new Error('graph ' + response.status);
        data = await response.json();
      } catch (err) {
        if (!destroyed && !controller.signal.aborted) showMessage('The link graph could not be loaded. Check that the docs server is running.');
        return;
      }
      if (destroyed) return;
      setData(data, previous);
      showMessage(nodes.length ? '' : 'There are no notes to show yet.');
      if (!previous) {
        // Warm up off-screen so the first frame is already readable.
        for (let i = 0; i < 80 && alpha > 0.2; i++) tick();
        fit();
      }
    }

    // ----- Lifecycle --------------------------------------------------------------------

    readColors();
    buildControls();
    resize();
    on(canvas, 'pointerdown', onPointerDown);
    on(canvas, 'pointermove', onPointerMove);
    on(canvas, 'pointerup', onPointerUp);
    on(canvas, 'pointercancel', onPointerUp);
    on(canvas, 'pointerleave', (event) => {
      if (!gesture && event.pointerType === 'mouse') setHover(null);
    });
    on(canvas, 'wheel', onWheel, {passive: false});
    on(canvas, 'keydown', onKey);
    on(canvas, 'dblclick', (event) => {
      const p = pointerPosition(event);
      if (!hitTest(p.x, p.y)) zoomAt(p.x, p.y, 1.6);
    });
    on(window, 'resize', resize);
    const resizeObserver = new ResizeObserver(() => resize());
    resizeObserver.observe(container);
    cleanups.push(() => resizeObserver.disconnect());
    const themeObserver = new MutationObserver(() => {
      readColors();
      draw();
    });
    themeObserver.observe(document.documentElement, {attributes: true, attributeFilter: ['data-theme']});
    cleanups.push(() => themeObserver.disconnect());
    load(null);

    return {
      container,
      reload() {
        const previous = new Map(nodes.map((node) => [node.id, node]));
        return load(previous);
      },
      destroy() {
        destroyed = true;
        controller.abort();
        if (frame) cancelAnimationFrame(frame);
        frame = 0;
        for (const cleanup of cleanups.splice(0)) cleanup();
        container.classList.remove('is-panning', 'is-over-node');
      },
      // Test hooks: screen position of a node and the simulation state.
      debug() {
        return {
          alpha,
          running: !!frame,
          view: Object.assign({}, view),
          nodes: visibleNodes.map((node) => ({id: node.id, kind: node.kind, x: node.x * view.k + view.x, y: node.y * view.k + view.y, r: node.r * view.k})),
        };
      },
    };
  }

  let mounted = null;

  function mount(container) {
    if (!container) return null;
    if (mounted && mounted.container === container) return mounted;
    unmount();
    mounted = createGraph(container);
    return mounted;
  }

  function unmount() {
    if (!mounted) return;
    mounted.destroy();
    mounted = null;
  }

  function reload() {
    if (mounted && mounted.container.isConnected) return mounted.reload();
    return Promise.resolve();
  }

  window.DocsGraph = {
    mount,
    unmount,
    reload,
    debug: () => (mounted ? mounted.debug() : null),
  };

  const initial = document.getElementById('graph-view');
  if (initial) mount(initial);
})();
