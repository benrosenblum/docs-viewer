// Docs viewer client: navigation, live reload, rendering, and panels.
// Plain browser script (no modules, no build step). The server renders every
// page; this script swaps page parts in place and adds behavior on top.
'use strict';
(function () {
  const STORE_THEME = 'docsview:theme';
  const STORE_OPEN = 'docsview:open';
  const STORE_LAYOUT = 'docsview:layout';
  const STORE_RECENT = 'docsview:recent';
  const DEFAULT_ICONS = '/_/static/icons.svg';
  const GRAPH_SCRIPT = '/_/static/graph.js';
  const HOVER_DELAY = 350;
  const MIN_SIDEBAR = 180;
  const MAX_SIDEBAR = 640;
  const ZOOM_STEP = 1.25;
  const IMAGE_EXT = /\.(png|jpe?g|gif|webp|svg|avif|bmp|ico|pdf)$/i;
  const RESERVED = /^\/_\/(assets|api|static)\/|^\/_\/events(\/|$)/;
  // Tab icon for pages without one, so the browser does not request /favicon.ico.
  const FAVICON = 'data:image/svg+xml,' + encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#7c5cf0"/>' +
      '<path d="M10 7h8l5 5v13H10z" fill="#fff"/><path d="M18 7v5h5" fill="#d9cffd"/>' +
      '<path d="M13 16h7M13 19.5h7M13 23h4" stroke="#7c5cf0" stroke-width="1.6" stroke-linecap="round"/></svg>',
  );

  const root = document.documentElement;
  const narrowQuery = window.matchMedia('(max-width: 899.98px)');
  const hoverQuery = window.matchMedia('(hover: hover) and (pointer: fine)');
  const darkQuery = window.matchMedia('(prefers-color-scheme: dark)');

  // ---------------------------------------------------------------------------
  // Small helpers

  function byId(id) {
    return document.getElementById(id);
  }

  function all(selector, scope) {
    return Array.from((scope || document).querySelectorAll(selector));
  }

  function storageGet(key) {
    try {
      return localStorage.getItem(key);
    } catch (err) {
      return null;
    }
  }

  function storageSet(key, value) {
    try {
      if (value == null) localStorage.removeItem(key);
      else localStorage.setItem(key, value);
    } catch (err) {
      // Storage is optional; the viewer works without it.
    }
  }

  function storageGetJSON(key, fallback) {
    const raw = storageGet(key);
    if (raw == null) return fallback;
    try {
      return JSON.parse(raw);
    } catch (err) {
      return fallback;
    }
  }

  function storageSetJSON(key, value) {
    storageSet(key, JSON.stringify(value));
  }

  /**
   * Creates an element. `props` sets `className`, `text`, and attributes;
   * children may be nodes or strings (added as text).
   */
  function h(tag, props, children) {
    const node = document.createElement(tag);
    if (props) {
      for (const key of Object.keys(props)) {
        const value = props[key];
        if (value == null || value === false) continue;
        if (key === 'className') node.className = value;
        else if (key === 'text') node.textContent = value;
        else node.setAttribute(key, value === true ? '' : String(value));
      }
    }
    if (children) {
      for (const child of children) {
        if (child == null) continue;
        node.append(child);
      }
    }
    return node;
  }

  function iconHref(name) {
    const existing = document.querySelector('svg.icon use');
    const href = existing && (existing.getAttribute('href') || existing.getAttribute('xlink:href'));
    const base = href && href.includes('#') ? href.slice(0, href.indexOf('#')) : DEFAULT_ICONS;
    return base + '#' + name;
  }

  function icon(name) {
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('class', 'icon');
    svg.setAttribute('aria-hidden', 'true');
    const use = document.createElementNS(ns, 'use');
    use.setAttribute('href', iconHref(name));
    svg.append(use);
    return svg;
  }

  function debounce(fn, ms) {
    let timer = 0;
    return function (...args) {
      clearTimeout(timer);
      timer = setTimeout(() => fn.apply(this, args), ms);
    };
  }

  function clamp(value, min, max) {
    return Math.min(max, Math.max(min, value));
  }

  /** Returns the zoom factor of a wheel event. */
  function wheelZoomFactor(event) {
    const scale = event.deltaMode === 1 ? 0.05 : event.deltaMode === 2 ? 0.5 : 0.0022;
    return Math.exp(-event.deltaY * scale * (event.ctrlKey ? 2.5 : 1));
  }

  /** Scales `view` ({x, y, k}) by `factor`. The point (sx, sy) stays in place. */
  function zoomView(view, sx, sy, factor, min, max) {
    const k = clamp(view.k * factor, min, max);
    view.x = sx - ((sx - view.x) / view.k) * k;
    view.y = sy - ((sy - view.y) / view.k) * k;
    view.k = k;
  }

  /** Handles the zoom keys: `zoom(factor)` for + and -, `fit()` for 0. */
  function zoomKey(event, zoom, fit) {
    if (event.key === '+' || event.key === '=') zoom(ZOOM_STEP);
    else if (event.key === '-' || event.key === '_') zoom(1 / ZOOM_STEP);
    else if (event.key === '0') fit();
    else return;
    event.preventDefault();
  }

  /** Returns the viewer URL for a repository path, like the server's URL(). */
  function pathURL(path, fragment) {
    const encoded = '/' + String(path).split('/').map(encodeURIComponent).join('/');
    return fragment ? encoded + '#' + fragment : encoded;
  }

  /** Encodes text for a `#:~:text=` directive (dash, comma, ampersand too). */
  function textDirective(text) {
    return ':~:text=' + encodeURIComponent(text).replace(/-/g, '%2D');
  }

  function escapeRegExp(text) {
    return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  /** Appends `text` to `parent`, wrapping case-insensitive `terms` in <mark>. */
  function appendHighlighted(parent, text, terms) {
    const words = (terms || []).filter(Boolean).sort((a, b) => b.length - a.length);
    if (!words.length) {
      parent.append(text);
      return;
    }
    const pattern = new RegExp(words.map(escapeRegExp).join('|'), 'gi');
    let last = 0;
    let match;
    while ((match = pattern.exec(text)) !== null) {
      if (match[0] === '') {
        pattern.lastIndex++;
        continue;
      }
      if (match.index > last) parent.append(text.slice(last, match.index));
      parent.append(h('mark', {text: match[0]}));
      last = match.index + match[0].length;
    }
    if (last < text.length) parent.append(text.slice(last));
  }

  /** Appends `text` to `parent`, marking the characters at `positions`. */
  function appendMarkedPositions(parent, text, positions) {
    if (!positions || !positions.length) {
      parent.append(text);
      return;
    }
    const set = new Set(positions);
    let run = '';
    let marked = false;
    const flush = () => {
      if (!run) return;
      parent.append(marked ? h('mark', {text: run}) : run);
      run = '';
    };
    for (let i = 0; i < text.length; i++) {
      const isMarked = set.has(i);
      if (isMarked !== marked) {
        flush();
        marked = isMarked;
      }
      run += text[i];
    }
    flush();
  }

  const scriptLoads = new Map();

  /** Loads a classic script once; later calls share the same promise. */
  function loadScript(src) {
    if (scriptLoads.has(src)) return scriptLoads.get(src);
    const promise = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = src;
      script.async = true;
      script.onload = () => resolve();
      script.onerror = () => {
        scriptLoads.delete(src);
        script.remove();
        reject(new Error('Could not load ' + src));
      };
      document.head.append(script);
    });
    scriptLoads.set(src, promise);
    return promise;
  }

  function assetBase() {
    return document.body.dataset.assetBase || '';
  }

  function currentTheme() {
    return root.dataset.theme === 'dark' ? 'dark' : 'light';
  }

  function isPlainLeftClick(event) {
    return event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey;
  }

  /** True when `url` is the page on screen (not the address bar, which popstate has already moved). */
  function samePage(url) {
    return url.pathname + url.search === loadedLocation;
  }

  // ---------------------------------------------------------------------------
  // Fetching pages

  class NotViewerPage extends Error {}

  /** Fetches a viewer page and parses it. Rejects for non-HTML responses. */
  async function fetchDocument(url, signal) {
    const response = await fetch(url, {
      headers: {Accept: 'text/html'},
      credentials: 'same-origin',
      cache: 'no-store',
      signal,
    });
    const type = response.headers.get('Content-Type') || '';
    if (!type.includes('text/html')) throw new NotViewerPage('not HTML');
    const html = await response.text();
    const doc = new DOMParser().parseFromString(html, 'text/html');
    if (!doc.getElementById('main')) throw new NotViewerPage('not a viewer page');
    return {doc, url: response.url || url, status: response.status};
  }

  // Parsed pages for hover previews, keyed by URL without fragment.
  const previewCache = new Map();

  function cachedDocument(url) {
    // Previews show the normal page, also for a file with uncommitted changes.
    const target = new URL(url.split('#')[0], location.href);
    target.searchParams.set('diff', 'off');
    const key = target.href;
    if (!previewCache.has(key)) {
      const promise = fetchDocument(key).then((result) => result.doc);
      promise.catch(() => previewCache.delete(key));
      previewCache.set(key, promise);
    }
    return previewCache.get(key);
  }

  // ---------------------------------------------------------------------------
  // Mermaid

  let mermaidQueue = Promise.resolve();
  let mermaidConfiguredTheme = null;

  function loadMermaid() {
    return loadScript(assetBase() + '/mermaid/mermaid.min.js').then(() => {
      if (!window.mermaid) throw new Error('Mermaid did not initialise.');
      return window.mermaid;
    });
  }

  function mermaidSource(node) {
    if (node.dataset.source == null) node.dataset.source = node.textContent;
    return node.dataset.source;
  }

  function resetMermaid(node) {
    const source = mermaidSource(node);
    node.removeAttribute('data-processed');
    node.classList.remove('is-error', 'is-rendered');
    node.textContent = source;
  }

  function showMermaidError(node, err) {
    const source = mermaidSource(node);
    const message = String((err && (err.message || err.str)) || err || 'Unknown error').trim();
    node.setAttribute('data-processed', 'true');
    node.classList.remove('is-rendered');
    node.classList.add('is-error');
    node.replaceChildren(
      h('div', {className: 'mermaid-error', role: 'note'}, [
        h('p', {className: 'mermaid-error-title', text: 'This Mermaid diagram could not be rendered.'}),
        h('p', {className: 'mermaid-error-message', text: message}),
        h('code', {className: 'mermaid-error-source', text: source}),
      ]),
    );
  }

  /** Returns the natural width of a rendered diagram, or 0. */
  function mermaidWidth(svg) {
    const box = svg.viewBox && svg.viewBox.baseVal;
    return (box && box.width) || parseFloat(svg.style.maxWidth) || 0;
  }

  /** Lets wide diagrams shrink to two thirds, then scroll horizontally. */
  function sizeMermaid(node) {
    const svg = node.querySelector(':scope > svg');
    if (!svg) return;
    const width = mermaidWidth(svg);
    if (!width) return;
    svg.removeAttribute('height');
    svg.style.width = '100%';
    svg.style.maxWidth = width + 'px';
    svg.style.minWidth = Math.round(width * 0.66) + 'px';
    svg.style.height = 'auto';
    // viewer.css lets diagrams wider than the text column use the view's width.
    node.style.setProperty('--diagram-natural-width', Math.ceil(width) + 'px');
  }

  /** Removes the scratch containers Mermaid leaves in <body> when a render fails. */
  function removeMermaidLeftovers() {
    for (const stray of all('body > [id^="dmermaid"], body > svg[id^="mermaid"]')) stray.remove();
  }

  // Firefox on Windows (seen in version 156) keeps stale boxes for SVG content
  // that holds a laid-out <foreignObject>: getBBox ignores later size and
  // transform changes. Mermaid sizes its HTML labels and its layout this way,
  // so the diagram spreads over thousands of pixels and shows as blank space.
  // When a probe finds the fault, the viewer computes those boxes from the
  // child geometry, as the SVG specification defines them, while Mermaid runs.
  let staleBBoxes = null;

  function hasStaleBBoxes() {
    if (staleBBoxes != null) return staleBBoxes;
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    const group = document.createElementNS(ns, 'g');
    const object = document.createElementNS(ns, 'foreignObject');
    const label = h('div', {text: 'x'});
    svg.setAttribute('style', 'position:absolute;left:-9999px;top:0;width:1px;height:1px');
    object.append(label);
    group.append(object);
    svg.append(group);
    document.body.append(svg);
    try {
      label.getBoundingClientRect();
      object.setAttribute('width', '10');
      object.setAttribute('height', '10');
      group.setAttribute('transform', 'translate(50 60)');
      const box = svg.getBBox();
      staleBBoxes = box.x !== 50 || box.y !== 60 || box.width !== 10 || box.height !== 10;
    } catch (err) {
      staleBBoxes = false;
    } finally {
      svg.remove();
    }
    return staleBBoxes;
  }

  /** Returns the transform attribute of `element` as one matrix, without changing it. */
  function transformMatrix(element) {
    const matrix = new DOMMatrix();
    const list = element.transform && element.transform.baseVal;
    for (let i = 0; list && i < list.numberOfItems; i++) matrix.multiplySelf(list.getItem(i).matrix);
    return matrix;
  }

  /** Adds `box` (an SVGRect), mapped through `matrix`, to `union`. */
  function unionBox(union, box, matrix) {
    let left = union ? union.left : Infinity;
    let top = union ? union.top : Infinity;
    let right = union ? union.right : -Infinity;
    let bottom = union ? union.bottom : -Infinity;
    const corners = [[box.x, box.y], [box.x + box.width, box.y], [box.x, box.y + box.height], [box.x + box.width, box.y + box.height]];
    for (const [x, y] of corners) {
      const point = new DOMPoint(x, y).matrixTransform(matrix);
      left = Math.min(left, point.x);
      top = Math.min(top, point.y);
      right = Math.max(right, point.x);
      bottom = Math.max(bottom, point.y);
    }
    return new DOMRect(left, top, right - left, bottom - top);
  }

  function foreignObjectBBox(object) {
    return new DOMRect(object.x.baseVal.value, object.y.baseVal.value, object.width.baseVal.value, object.height.baseVal.value);
  }

  /** Returns the union of the child boxes, or null when no child has geometry. */
  function childGeometryBBox(element, nativeBBox) {
    let union = null;
    for (const child of element.children) {
      if (!(child instanceof SVGGraphicsElement) || child instanceof SVGDefsElement || child instanceof SVGSymbolElement) continue;
      if (getComputedStyle(child).display === 'none') continue;
      let box;
      if (child instanceof SVGForeignObjectElement) box = foreignObjectBBox(child);
      else if (child instanceof SVGGElement || child instanceof SVGAElement) box = childGeometryBBox(child, nativeBBox);
      else box = nativeBBox.call(child);
      // Like Firefox, skip empty geometry, such as labels without text.
      if (box && (box.width || box.height)) union = unionBox(union, box, transformMatrix(child));
    }
    return union;
  }

  /** Runs `render` with the getBBox fault corrected, when this browser has it. */
  async function withCorrectBBoxes(render) {
    if (!hasStaleBBoxes()) return render();
    const proto = SVGGraphicsElement.prototype;
    const nativeBBox = proto.getBBox;
    proto.getBBox = function getBBox(options) {
      if (options) return nativeBBox.call(this, options);
      if (this instanceof SVGForeignObjectElement) return foreignObjectBBox(this);
      if (!this.querySelector('foreignObject')) return nativeBBox.call(this);
      return childGeometryBBox(this, nativeBBox) || new DOMRect();
    };
    try {
      return await render();
    } finally {
      proto.getBBox = nativeBBox;
    }
  }

  function deferUntilOpen(details) {
    if (details.dataset.mermaidDeferred) return;
    details.dataset.mermaidDeferred = 'true';
    details.addEventListener('toggle', function onToggle() {
      if (!details.open) return;
      details.removeEventListener('toggle', onToggle);
      delete details.dataset.mermaidDeferred;
      renderMermaid(details);
    });
  }

  /** Renders pending `pre.mermaid` diagrams inside `scope`, one at a time. */
  function renderMermaid(scope) {
    const pending = all('pre.mermaid', scope).filter((node) => !node.hasAttribute('data-processed'));
    if (!pending.length) return Promise.resolve();
    const visible = [];
    for (const node of pending) {
      mermaidSource(node);
      // Mermaid measures text, so hidden diagrams would lay out at zero size.
      const closed = node.parentElement && node.parentElement.closest('details:not([open])');
      if (closed) deferUntilOpen(closed);
      else visible.push(node);
    }
    if (!visible.length) return Promise.resolve();
    mermaidQueue = mermaidQueue.then(async () => {
      let mermaid;
      try {
        mermaid = await loadMermaid();
      } catch (err) {
        for (const node of visible) showMermaidError(node, err);
        return;
      }
      const theme = currentTheme();
      if (mermaidConfiguredTheme !== theme) {
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict',
          theme: theme === 'dark' ? 'dark' : 'default',
          suppressErrorRendering: true,
          fontFamily: getComputedStyle(document.body).fontFamily,
        });
        mermaidConfiguredTheme = theme;
      }
      await withCorrectBBoxes(async () => {
        for (const node of visible) {
          if (!node.isConnected || node.hasAttribute('data-processed')) continue;
          try {
            await mermaid.run({nodes: [node]});
            if (!node.querySelector(':scope > svg')) throw new Error('Mermaid produced no diagram.');
            node.classList.add('is-rendered');
            sizeMermaid(node);
          } catch (err) {
            showMermaidError(node, err);
          }
          removeMermaidLeftovers();
        }
      });
    });
    return mermaidQueue;
  }

  function rerenderAllMermaid() {
    const nodes = all('pre.mermaid[data-source]');
    if (!nodes.length) return Promise.resolve();
    mermaidConfiguredTheme = null;
    for (const node of nodes) resetMermaid(node);
    const scopes = new Set(nodes.map((node) => node.closest('.hover-popover') || byId('main') || document.body));
    return Promise.all(Array.from(scopes, (scope) => renderMermaid(scope)));
  }

  // ---------------------------------------------------------------------------
  // KaTeX

  function renderMath(scope) {
    const nodes = all('.math', scope).filter((node) => !node.classList.contains('is-rendered'));
    if (!nodes.length) return Promise.resolve();
    return loadScript(assetBase() + '/katex/katex.min.js').then(
      () => {
        for (const node of nodes) {
          if (node.dataset.tex == null) node.dataset.tex = node.textContent;
          try {
            window.katex.render(node.dataset.tex, node, {
              displayMode: node.classList.contains('math-block'),
              throwOnError: false,
            });
            node.classList.add('is-rendered');
          } catch (err) {
            node.title = String(err && err.message);
          }
        }
      },
      () => {
        // The TeX source stays visible when KaTeX cannot load.
      },
    );
  }

  // ---------------------------------------------------------------------------
  // Content enhancers

  function addHeadingAnchors(scope) {
    for (const heading of all('.markdown-body :is(h1, h2, h3, h4, h5, h6)[id]', scope)) {
      if (heading.closest('.markdown-embed') || heading.querySelector(':scope > .heading-anchor')) continue;
      const label = 'Link to section: ' + heading.textContent.trim();
      heading.prepend(h('a', {className: 'heading-anchor', href: '#' + heading.id, 'aria-label': label, text: '#'}));
    }
  }

  function codeText(pre) {
    const lines = pre.querySelectorAll('.line > .cl');
    if (lines.length) return Array.from(lines, (line) => line.textContent).join('');
    const code = pre.querySelector('code');
    return (code || pre).textContent;
  }

  function addCopyButtons(scope) {
    for (const pre of all('pre.code-block', scope)) {
      if (pre.classList.contains('source') || pre.parentElement.classList.contains('code-block-wrapper')) continue;
      const wrapper = h('div', {className: 'code-block-wrapper', 'data-lang': pre.dataset.lang || ''});
      pre.replaceWith(wrapper);
      const button = h('button', {type: 'button', className: 'copy-code-button', 'aria-label': 'Copy code', title: 'Copy code'}, [icon('i-copy')]);
      wrapper.append(pre, button);
    }
    for (const toolbar of all('.source-toolbar', scope)) {
      if (toolbar.querySelector('.copy-code-button, [data-action="copy-source"]')) continue;
      toolbar.append(h('button', {type: 'button', 'data-action': 'copy-source', title: 'Copy file contents'}, [icon('i-copy'), h('span', {text: 'Copy'})]));
    }
  }

  async function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return;
    }
    const area = h('textarea', {readonly: true, 'aria-hidden': 'true'});
    area.value = text;
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.append(area);
    area.select();
    const ok = document.execCommand('copy');
    area.remove();
    if (!ok) throw new Error('Copy failed');
  }

  function flashCopied(button) {
    const use = button.querySelector('use');
    button.classList.add('is-copied');
    if (use) use.setAttribute('href', iconHref('i-check'));
    clearTimeout(button._copyTimer);
    button._copyTimer = setTimeout(() => {
      button.classList.remove('is-copied');
      if (use) use.setAttribute('href', iconHref('i-copy'));
    }, 1500);
  }

  function markExternalLinks(scope) {
    for (const link of all('a[href]', scope)) {
      if (link.closest('.mermaid')) continue;
      let url;
      try {
        url = new URL(link.href, location.href);
      } catch (err) {
        continue;
      }
      const external = /^https?:$/.test(url.protocol) && url.origin !== location.origin;
      if (!external && !link.classList.contains('external-link')) continue;
      if (external && !link.closest('.properties, .markdown-body')) continue;
      link.classList.add('external-link');
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
    }
  }

  /** Runs every enhancer on freshly inserted content; resolves when done. */
  /** The words a search query matches, as the search API reports its terms. */
  function queryTerms(query) {
    const terms = [];
    for (const m of String(query || '').matchAll(/(\S+?:)?"([^"]*)"|(\S+)/g)) {
      const token = m[3] || (m[1] || '') + m[2];
      const lower = token.toLowerCase();
      if (lower.startsWith('tag:')) terms.push('#' + token.slice(4).replace(/^#/, ''));
      else if (!/^(path|file):/.test(lower)) terms.push(token);
    }
    return terms.filter((term) => term && term !== '#');
  }

  /** Marks the query's terms in the server-rendered /_/search results. */
  function markSearchResults(scope) {
    const input = scope.querySelector('.search-form input[name="q"]');
    const terms = input ? queryTerms(input.value) : [];
    if (!terms.length) return;
    for (const link of all('.search-match > a', scope)) {
      if (link.querySelector('mark')) continue;
      for (const node of Array.from(link.childNodes)) {
        if (node.nodeType !== Node.TEXT_NODE) continue;
        const marked = document.createDocumentFragment();
        appendHighlighted(marked, node.textContent, terms);
        node.replaceWith(marked);
      }
    }
  }

  function enhance(scope, options) {
    if (!scope) return Promise.resolve();
    const preview = options && options.preview;
    markExternalLinks(scope);
    if (!preview) {
      addHeadingAnchors(scope);
      addCopyButtons(scope);
      markSearchResults(scope);
    }
    return Promise.all([renderMath(scope), renderMermaid(scope), preview ? null : renderSpec(scope)]).then(() => undefined);
  }

  // ---------------------------------------------------------------------------
  // API reference (Redoc)

  let colorProbe = null;

  /** Resolves a CSS color to rgb() or rgba(), which Redoc's color math parses. */
  function rgb(value) {
    colorProbe = colorProbe || document.createElement('canvas').getContext('2d', {willReadFrequently: true});
    colorProbe.clearRect(0, 0, 1, 1);
    colorProbe.fillStyle = value;
    colorProbe.fillRect(0, 0, 1, 1);
    const [r, g, b, a] = colorProbe.getImageData(0, 0, 1, 1).data;
    return a === 255 ? `rgb(${r}, ${g}, ${b})` : `rgba(${r}, ${g}, ${b}, ${(a / 255).toFixed(3)})`;
  }

  /** Builds a Redoc theme from the viewer tokens of the current theme. */
  function redocTheme() {
    const style = getComputedStyle(document.body);
    const token = (name) => style.getPropertyValue(name).trim();
    const color = (name) => rgb(token(name));
    // Redoc's breakpoints are window widths; add the space the sidebars take.
    const container = view();
    const sides = container ? window.innerWidth - container.clientWidth : 0;
    const width = (rem) => `${rem * 16 + sides}px`;
    return {
      breakpoints: {small: width(50), medium: width(75), large: width(105)},
      colors: {
        primary: {main: color('--accent')},
        success: {main: color('--c-green')},
        warning: {main: color('--c-orange')},
        error: {main: color('--c-red')},
        text: {primary: color('--text-normal'), secondary: color('--text-muted')},
        border: {dark: color('--border-strong'), light: color('--border')},
        http: {get: color('--c-blue'), post: color('--c-green'), put: color('--c-orange'), patch: color('--c-orange'), delete: color('--c-red')},
      },
      typography: {
        fontFamily: token('--font-text'),
        headings: {fontFamily: token('--font-text')},
        code: {fontFamily: token('--font-mono'), color: color('--code-text'), backgroundColor: color('--code-bg')},
        links: {color: color('--accent'), visited: color('--accent'), hover: color('--text-normal')},
      },
      sidebar: {backgroundColor: color('--bg-secondary'), textColor: color('--text-normal'), activeTextColor: color('--accent')},
      rightPanel: {backgroundColor: color('--code-bg'), textColor: color('--text-normal')},
      codeBlock: {backgroundColor: color('--bg-primary')},
      schema: {nestedBackground: color('--bg-secondary'), linesColor: color('--border-strong'), typeNameColor: color('--text-muted')},
      // Redoc hard-codes a dark gray for its section headers.
      extensionsHook: (name) => (name === 'UnderlinedHeader' ? `color: ${color('--text-muted')}; border-bottom-color: ${color('--border')};` : ''),
    };
  }

  let redocLogoBlocked = false;

  /**
   * Redoc's menu footer always loads a logo from its CDN, which the content
   * policy blocks with a console error. Drop that src, which Redoc sets as a
   * property or an attribute.
   */
  function blockRedocLogo() {
    if (redocLogoBlocked) return;
    redocLogoBlocked = true;
    const blocked = (value) => String(value).startsWith('https://cdn.redoc.ly/');
    const src = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, 'src');
    Object.defineProperty(HTMLImageElement.prototype, 'src', {
      ...src,
      set(value) {
        if (!blocked(value)) src.set.call(this, value);
      },
    });
    const setAttribute = Element.prototype.setAttribute;
    Element.prototype.setAttribute = function (name, value) {
      if (!(this instanceof HTMLImageElement && name === 'src' && blocked(value))) setAttribute.call(this, name, value);
    };
  }

  /** Renders the spec of the page's #redoc container, if any, with Redoc. */
  function renderSpec(scope) {
    const container = scope.querySelector('#redoc');
    if (!container || !window.Redoc) return Promise.resolve();
    blockRedocLogo();
    const header = document.querySelector('.view-header');
    const options = {theme: redocTheme(), scrollYOffset: () => (header ? header.offsetHeight : 0), nativeScrollbars: true};
    return new Promise((resolve) => window.Redoc.init(container.dataset.spec, options, container, resolve));
  }

  /** Renders the spec again and keeps the section at the top of the view in place. */
  function rerenderSpec() {
    const container = view();
    const main = byId('main');
    if (!container || !main || !byId('redoc')) return Promise.resolve();
    const top = container.getBoundingClientRect().top;
    const anchor = all('[data-section-id]', container).find((el) => el.getBoundingClientRect().bottom > top);
    const id = anchor && anchor.dataset.sectionId;
    const offset = anchor ? anchor.getBoundingClientRect().top - top : 0;
    const scroll = container.scrollTop;
    return renderSpec(main).then(() => {
      const again = id && container.querySelector(`[data-section-id="${CSS.escape(id)}"]`);
      if (again) container.scrollTop += again.getBoundingClientRect().top - top - offset;
      else container.scrollTop = scroll;
    });
  }

  /**
   * Loads `url` as a full page. Spec pages have their own content policy and
   * Redoc keeps global state, so no client-side swap enters or leaves them.
   */
  function loadFully(url) {
    if (url.href === location.href) location.reload();
    else location.assign(url.href);
  }

  // ---------------------------------------------------------------------------
  // Scrolling to fragments

  let userScrolledAt = 0;
  let fragmentHighlight = null;

  function view() {
    return byId('view');
  }

  function scrollViewTo(target, offset) {
    const container = view();
    if (!container || !target) return;
    const top = target.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop;
    container.scrollTop = Math.max(0, top - (offset == null ? 16 : offset));
  }

  function flash(element) {
    if (!element) return;
    element.classList.remove('is-flashing');
    void element.offsetWidth;
    element.classList.add('is-flashing');
    setTimeout(() => element.classList.remove('is-flashing'), 1700);
  }

  function clearFragmentHighlight() {
    if (fragmentHighlight && window.CSS && CSS.highlights) CSS.highlights.delete('docsview-fragment');
    fragmentHighlight = null;
  }

  /** Parses the first `text=` directive of a `:~:` fragment directive. */
  function parseTextDirective(directive) {
    for (const part of directive.split('&')) {
      if (!part.startsWith('text=')) continue;
      const pieces = part.slice(5).split(',').map((piece) => {
        try {
          return decodeURIComponent(piece);
        } catch (err) {
          return piece;
        }
      });
      if (pieces.length > 1 && pieces[0].endsWith('-')) pieces.shift();
      if (pieces.length > 1 && pieces[pieces.length - 1].startsWith('-')) pieces.pop();
      return pieces[0] || '';
    }
    return '';
  }

  /** Finds text in the page (case-insensitive) and highlights the match. */
  function findText(text, scope) {
    const needle = text.trim().toLowerCase();
    if (!needle || !scope) return null;
    const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (node.parentElement.closest('.heading-anchor, script, style')) continue;
      const index = node.data.toLowerCase().indexOf(needle);
      if (index < 0) continue;
      if (window.Highlight && window.CSS && CSS.highlights) {
        const range = document.createRange();
        range.setStart(node, index);
        range.setEnd(node, index + needle.length);
        fragmentHighlight = new Highlight(range);
        CSS.highlights.set('docsview-fragment', fragmentHighlight);
      }
      return node.parentElement;
    }
    // The text may span inline elements: fall back to the enclosing block.
    for (const block of all('p, li, td, th, h1, h2, h3, h4, h5, h6, blockquote, pre', scope)) {
      if (block.textContent.replace(/\s+/g, ' ').toLowerCase().includes(needle)) return block;
    }
    return null;
  }

  /** Scrolls #view to a URL fragment: an element id or a text directive. */
  function scrollToHash(hash, options) {
    clearFragmentHighlight();
    const container = view();
    if (!container || !hash || hash === '#') return false;
    const raw = hash.replace(/^#/, '');
    const at = raw.indexOf(':~:');
    const idPart = at >= 0 ? raw.slice(0, at) : raw;
    let target = null;
    if (idPart) {
      let id = idPart;
      try {
        id = decodeURIComponent(idPart);
      } catch (err) {
        // Keep the raw fragment.
      }
      const candidate = document.getElementById(id);
      if (candidate && container.contains(candidate)) target = candidate;
    }
    let textTarget = null;
    if (at >= 0) {
      const text = parseTextDirective(raw.slice(at + 3));
      textTarget = findText(text, byId('content') || container);
    }
    const scrollTarget = textTarget || target;
    if (!scrollTarget) return false;
    if (textTarget) scrollViewTo(textTarget, container.clientHeight / 3);
    else scrollViewTo(target);
    if (options && options.flash) {
      const line = scrollTarget.closest('.line');
      if (line) {
        all('.line.is-target').forEach((el) => el.classList.remove('is-target'));
        line.classList.add('is-target');
      } else if (!textTarget && !/^H[1-6]$/.test(scrollTarget.tagName)) {
        flash(scrollTarget);
      }
    }
    return true;
  }

  /** Applies a scroll now and again once async rendering has settled. */
  function settleScroll(apply, rendering) {
    const started = Date.now();
    apply();
    rendering.then(() => {
      if (userScrolledAt < started) apply();
    });
  }

  function noteUserScroll() {
    userScrolledAt = Date.now();
  }

  // ---------------------------------------------------------------------------
  // History state

  function saveScrollState() {
    const container = view();
    if (!container) return;
    const state = Object.assign({}, history.state, {scroll: container.scrollTop});
    try {
      history.replaceState(state, '');
    } catch (err) {
      // Some browsers throttle replaceState; the scroll is not important.
    }
  }

  const saveScrollSoon = debounce(saveScrollState, 200);

  // ---------------------------------------------------------------------------
  // Page swapping

  let navToken = 0;
  let loadedLocation = location.pathname + location.search;
  let lastRendering = Promise.resolve();

  function replacePart(id, doc) {
    const next = doc.getElementById(id);
    const current = byId(id);
    if (!next || !current) return false;
    current.replaceWith(document.adoptNode(next));
    return true;
  }

  function syncBodyAttributes(nextBody) {
    const body = document.body;
    for (const name of body.getAttributeNames()) {
      if (name.startsWith('data-') && !nextBody.hasAttribute(name)) body.removeAttribute(name);
    }
    for (const name of nextBody.getAttributeNames()) {
      if (name.startsWith('data-') || name === 'class') body.setAttribute(name, nextBody.getAttribute(name));
    }
  }

  function treeKey(item) {
    if (!item) return '';
    if (item.dataset.path) return item.dataset.path;
    const li = item.closest('li[data-path]');
    if (li) return li.dataset.path;
    return item.getAttribute('href') || '';
  }

  /** Moves the tree's active marker to match `doc`; returns false if absent. */
  function syncTreeActive(doc) {
    const tree = byId('file-tree');
    if (!tree) return true;
    const nextActive = doc.querySelector('#file-tree .tree-item.is-active');
    for (const item of all('.tree-item.is-active', tree)) item.classList.remove('is-active');
    if (!nextActive) return true;
    const key = treeKey(nextActive);
    const match = all('.tree-item', tree).find((item) => treeKey(item) === key);
    if (!match) return false;
    match.classList.add('is-active');
    if (match.tagName === 'A') match.setAttribute('aria-current', 'page');
    revealTreeItem(match);
    scrollTreeItemIntoView(match);
    return true;
  }

  /**
   * Replaces page parts with those of `doc`. `parts.main`, `parts.right`,
   * and `parts.tree` ('swap' or 'active') choose what changes.
   */
  function applyDocument(doc, parts) {
    const wasGraph = document.body.dataset.kind === 'graph';
    if (parts.main && wasGraph && window.DocsGraph) window.DocsGraph.unmount();
    closePreview();
    stopOutline();
    if (parts.main) {
      closePicker();
      lastChange = null;
      replacePart('main', doc);
      document.title = doc.title;
      syncBodyAttributes(doc.body);
    }
    if (parts.right) replacePart('right', doc);
    if (parts.tree === 'swap') {
      replacePart('file-tree', doc);
      applyTreeState();
    } else if (parts.tree === 'active' && !syncTreeActive(doc)) {
      replacePart('file-tree', doc);
      applyTreeState();
    }
    syncChrome();
    const main = byId('main');
    lastRendering = parts.main ? enhance(main) : Promise.resolve();
    if (parts.main || parts.right) startOutline();
    if (parts.main) {
      mountGraphIfNeeded();
      watchViewScroll();
      connectLive();
    }
    return lastRendering;
  }

  function mountGraphIfNeeded() {
    const container = byId('graph-view');
    if (!container) return;
    if (window.DocsGraph) window.DocsGraph.mount(container);
    else loadScript(GRAPH_SCRIPT).catch(() => location.reload());
  }

  function closeTransientUI() {
    closeModal();
    closePicker();
    closePreview();
    setOverlay('');
  }

  /**
   * Shows `href` without a full page load. `options.push` adds a history
   * entry; `options.scroll` restores a saved scroll position.
   */
  async function navigate(href, options) {
    const opts = options || {};
    const url = new URL(href, location.href);
    closeTransientUI();
    if (samePage(url) && url.hash && !opts.reload) {
      if (opts.push) {
        saveScrollState();
        history.pushState({}, '', url.href);
      }
      scrollToHash(url.hash, {flash: true});
      return;
    }
    if (document.body.dataset.kind === 'apispec') {
      loadFully(url);
      return;
    }
    const token = ++navToken;
    const slow = setTimeout(() => root.classList.add('is-navigating'), 120);
    let result;
    try {
      result = await fetchDocument(url.href);
    } catch (err) {
      if (token === navToken) location.assign(url.href);
      return;
    } finally {
      clearTimeout(slow);
      if (token === navToken) root.classList.remove('is-navigating');
    }
    if (token !== navToken) return;
    const finalURL = new URL(result.url, location.href);
    finalURL.hash = url.hash;
    if (result.doc.body.dataset.kind === 'apispec') {
      loadFully(finalURL);
      return;
    }
    if (opts.push) {
      saveScrollState();
      if (finalURL.href === location.href) history.replaceState({}, '', finalURL.href);
      else history.pushState({}, '', finalURL.href);
    } else if (finalURL.href !== location.href) {
      history.replaceState(Object.assign({}, history.state), '', finalURL.href);
    }
    loadedLocation = location.pathname + location.search;
    const rendering = applyDocument(result.doc, {main: true, right: true, tree: 'active'});
    const container = view();
    if (container) {
      if (typeof opts.scroll === 'number') {
        settleScroll(() => {
          const current = view();
          if (current) current.scrollTop = opts.scroll;
        }, rendering);
      } else if (url.hash) {
        settleScroll(() => scrollToHash(url.hash, {flash: false}), rendering);
        flashTarget(url.hash);
      } else {
        container.scrollTop = 0;
      }
    }
    recordRecent();
  }

  function flashTarget(hash) {
    const raw = hash.replace(/^#/, '');
    if (!raw || raw.includes(':~:')) return;
    let id = raw;
    try {
      id = decodeURIComponent(raw);
    } catch (err) {
      // Keep the raw fragment.
    }
    const target = document.getElementById(id);
    if (!target) return;
    const line = target.closest('.line');
    if (line) line.classList.add('is-target');
    else if (!/^H[1-6]$/.test(target.tagName)) flash(target);
  }

  function shouldIntercept(link, url) {
    if (!(link instanceof HTMLAnchorElement)) return false;
    if (link.target && link.target !== '_self') return false;
    if (link.hasAttribute('download')) return false;
    if (url.origin !== location.origin) return false;
    if (!/^https?:$/.test(url.protocol)) return false;
    if (RESERVED.test(url.pathname)) return false;
    if (url.searchParams.get('raw') === '1') return false;
    if (IMAGE_EXT.test(url.pathname)) return false;
    return true;
  }

  function onDocumentClick(event) {
    if (event.defaultPrevented) return;
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;

    const actionEl = target.closest('[data-action]');
    if (actionEl && handleAction(actionEl, event)) return;

    const copyButton = target.closest('.copy-code-button');
    if (copyButton) {
      const pre = copyButton.parentElement.querySelector('pre');
      if (pre) copyText(codeText(pre)).then(() => flashCopied(copyButton), () => {});
      return;
    }

    if (!isPlainLeftClick(event)) return;
    const link = target.closest('a[href]');
    if (!link) {
      const source = lightboxSource(target);
      if (source) openLightbox(source);
      return;
    }
    let url;
    try {
      url = new URL(link.href, location.href);
    } catch (err) {
      return;
    }
    if (!shouldIntercept(link, url)) return;
    event.preventDefault();
    navigate(url.href, {push: true});
  }

  function onSubmit(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || event.defaultPrevented) return;
    if ((form.getAttribute('method') || 'get').toLowerCase() !== 'get' || form.target) return;
    const url = new URL(form.getAttribute('action') || location.pathname, location.href);
    if (url.origin !== location.origin || RESERVED.test(url.pathname)) return;
    event.preventDefault();
    url.search = new URLSearchParams(new FormData(form)).toString();
    navigate(url.href, {push: true});
  }

  function onPopState(event) {
    const state = event.state || {};
    const here = location.pathname + location.search;
    if (here === loadedLocation) {
      closeTransientUI();
      if (typeof state.scroll === 'number' && view()) view().scrollTop = state.scroll;
      else if (location.hash) scrollToHash(location.hash, {flash: true});
      else if (view()) view().scrollTop = 0;
      return;
    }
    navigate(location.href, {push: false, scroll: typeof state.scroll === 'number' ? state.scroll : undefined});
  }

  // ---------------------------------------------------------------------------
  // Actions (buttons with data-action)

  function handleAction(element, event) {
    const action = element.dataset.action;
    switch (action) {
      case 'switcher':
        event.preventDefault();
        openSwitcher();
        return true;
      case 'search':
        event.preventDefault();
        openSearch();
        return true;
      case 'graph':
        if (!isPlainLeftClick(event)) return true;
        event.preventDefault();
        navigate(element.getAttribute('href') || '/_/graph', {push: true});
        return true;
      case 'collapse-all':
        event.preventDefault();
        collapseAllFolders();
        return true;
      case 'theme':
        event.preventDefault();
        setTheme(currentTheme() === 'dark' ? 'light' : 'dark', true);
        return true;
      case 'toggle-left':
        event.preventDefault();
        toggleSidebar('left');
        return true;
      case 'toggle-right':
        event.preventDefault();
        toggleSidebar('right');
        return true;
      case 'close-overlay':
        event.preventDefault();
        setOverlay('');
        return true;
      case 'diff-picker':
        event.preventDefault();
        togglePicker(element);
        return true;
      case 'diff-prev':
      case 'diff-next':
        event.preventDefault();
        jumpChange(action === 'diff-next' ? 1 : -1);
        return true;
      case 'diff-expand': {
        event.preventDefault();
        const fold = element.closest('tbody.diff-fold');
        const hidden = fold && fold.nextElementSibling;
        if (hidden && hidden.classList.contains('diff-hidden')) hidden.hidden = false;
        if (fold) fold.remove();
        return true;
      }
      case 'copy-source': {
        event.preventDefault();
        const pre = document.querySelector('.source-view pre');
        if (pre) copyText(codeText(pre)).then(() => flashCopied(element), () => {});
        return true;
      }
      default:
        return false;
    }
  }

  // ---------------------------------------------------------------------------
  // Diff change navigation

  // The last jump: the index of the change and the scroll position after it.
  let lastChange = null;

  /** Goes to the next (step 1) or the previous (step -1) change of a diff. */
  function jumpChange(step) {
    const container = view();
    const stops = all('#content [data-change]');
    if (!container || !stops.length) return;
    let index;
    if (lastChange && lastChange.scroll === container.scrollTop) {
      // No scroll since the last jump: go on from that change. This also
      // moves between changes in a view that cannot scroll.
      index = lastChange.index + step;
    } else {
      // The first change in the view or below it, else the last one above.
      // A change in a folded callout is at the position of the callout.
      const top = container.getBoundingClientRect().top;
      const below = stops.findIndex((stop) => (stop.closest('details:not([open])') || stop).getBoundingClientRect().top >= top);
      index = (below < 0 ? stops.length : below) - (step > 0 ? 0 : 1);
    }
    index = clamp(index, 0, stops.length - 1);
    const stop = stops[index];
    for (let fold = stop.closest('details:not([open])'); fold; fold = fold.parentElement.closest('details:not([open])')) fold.open = true;
    scrollViewTo(stop, container.clientHeight / 4);
    flash(stop);
    lastChange = {index, scroll: container.scrollTop};
  }

  // ---------------------------------------------------------------------------
  // Diff base picker

  let picker = null;

  function closePicker() {
    if (!picker) return false;
    const current = picker;
    picker = null;
    current.element.remove();
    current.button.setAttribute('aria-expanded', 'false');
    document.removeEventListener('mousedown', current.onOutside, true);
    return true;
  }

  /** The URL of this file page with a diff base, keeping the diff form. */
  function diffURL(base, as) {
    const url = new URL(location.pathname, location.href);
    url.searchParams.set('diff', base);
    if (as && base !== 'off') url.searchParams.set('as', as);
    return url.pathname + url.search;
  }

  function formatDate(iso) {
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return iso;
    return date.toLocaleDateString(undefined, {year: 'numeric', month: 'short', day: 'numeric'});
  }

  function pickerItem(href, title, note, current) {
    return h('a', {className: 'suggestion-item diff-picker-item' + (current ? ' is-current' : ''), href, role: 'option', 'aria-current': current ? 'true' : null}, [
      h('span', {className: 'suggestion-title'}, title),
      note ? h('span', {className: 'suggestion-note', text: note}) : null,
    ]);
  }

  function togglePicker(button) {
    const open = picker && picker.button === button;
    closePicker();
    if (!open) openPicker(button);
  }

  /**
   * Opens the base picker below `button`. The file history loads when the
   * picker opens. The keys are those of the quick switcher list.
   */
  function openPicker(button) {
    const path = button.dataset.path || '';
    const base = button.dataset.base || 'off';
    const as = button.dataset.as || '';
    const list = h('div', {className: 'diff-picker-list', id: 'diff-picker-list', role: 'listbox', tabindex: '-1', 'aria-label': 'Compare with'});
    const element = h('div', {className: 'diff-picker'}, [list]);
    button.after(element);
    button.setAttribute('aria-expanded', 'true');
    button.setAttribute('aria-controls', list.id);
    const selection = createSelection(list, '.suggestion-item', list);
    list.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        closePicker();
        button.focus();
      } else if (event.key === 'Tab') {
        closePicker();
      } else {
        promptKeys(event, selection);
      }
    });
    list.addEventListener('mousemove', (event) => {
      const item = event.target.closest('.suggestion-item');
      if (item) selection.track(item);
    });
    const onOutside = (event) => {
      if (!element.contains(event.target) && !button.contains(event.target)) closePicker();
    };
    document.addEventListener('mousedown', onOutside, true);
    picker = {element, button, onOutside};
    list.replaceChildren(h('p', {className: 'prompt-empty', text: 'Loading history…'}));
    list.focus();
    const isBase = (hash) => /^[0-9a-f]{7,64}$/i.test(base) && hash.toLowerCase().startsWith(base.toLowerCase());
    fetch('/_/api/history?path=' + encodeURIComponent(path), {headers: {Accept: 'application/json'}, credentials: 'same-origin', cache: 'no-store'})
      .then((response) => {
        if (!response.ok) throw new Error('history ' + response.status);
        return response.json();
      })
      .then(
        (data) => {
          if (!picker || picker.element !== element) return;
          const items = [pickerItem(diffURL('HEAD', as), [h('span', {text: 'Uncommitted changes'})], 'Compare with HEAD', base === 'HEAD')];
          if (data.branchBase) {
            items.push(pickerItem(diffURL('branch', as), [h('span', {text: 'Branch changes'})], 'Compare with the merge base of ' + (data.branch || 'HEAD') + ' and ' + data.main, base === 'branch'));
          }
          const commits = Array.isArray(data.commits) ? data.commits : [];
          if (commits.length) items.push(h('div', {className: 'diff-picker-heading', role: 'presentation', text: 'History'}));
          for (const c of commits) {
            const note = c.author + ' · ' + formatDate(c.date) + (c.path && c.path !== path ? ' · ' + c.path : '');
            items.push(pickerItem(diffURL(c.hash, as), [h('span', {text: c.subject}), h('code', {className: 'diff-picker-hash', text: c.short})], note, isBase(c.hash)));
          }
          items.push(h('div', {className: 'diff-picker-heading', role: 'presentation', text: 'Without changes'}));
          items.push(pickerItem(diffURL('off'), [h('span', {text: 'Normal page'})], 'Show the file as it is now', base === 'off'));
          list.replaceChildren(...items);
          selection.reset();
          const current = list.querySelector('.is-current');
          if (current) selection.track(current);
        },
        () => {
          if (picker && picker.element === element) list.replaceChildren(h('p', {className: 'prompt-empty', text: 'The file history could not be loaded.'}));
        },
      );
  }

  // ---------------------------------------------------------------------------
  // Theme

  function setTheme(theme, persist) {
    root.dataset.theme = theme;
    if (persist) storageSet(STORE_THEME, theme);
    syncThemeButtons();
    rerenderAllMermaid();
    rerenderSpec();
    document.dispatchEvent(new CustomEvent('docsview:themechange', {detail: {theme}}));
  }

  function syncThemeButtons() {
    const dark = currentTheme() === 'dark';
    for (const button of all('[data-action="theme"]')) {
      const label = dark ? 'Switch to light theme' : 'Switch to dark theme';
      button.setAttribute('aria-label', label);
      button.title = label;
      // A button with both a sun and a moon icon is switched by CSS instead.
      const uses = button.querySelectorAll('use');
      if (uses.length === 1) uses[0].setAttribute('href', iconHref(dark ? 'i-sun' : 'i-moon'));
    }
  }

  function onSystemThemeChange(event) {
    const stored = storageGet(STORE_THEME);
    if (stored === 'light' || stored === 'dark') return;
    setTheme(event.matches ? 'dark' : 'light', false);
  }

  // ---------------------------------------------------------------------------
  // File tree

  function folderDetails(li) {
    return li.querySelector(':scope > details');
  }

  function revealTreeItem(item) {
    let node = item.tagName === 'SUMMARY' ? item.parentElement.parentElement : item.parentElement;
    while (node) {
      const details = node.closest('#file-tree details');
      if (!details) break;
      details.open = true;
      node = details.parentElement;
    }
  }

  function scrollTreeItemIntoView(item) {
    const tree = byId('file-tree');
    if (!tree || !item) return;
    const itemRect = item.getBoundingClientRect();
    const treeRect = tree.getBoundingClientRect();
    if (itemRect.top >= treeRect.top && itemRect.bottom <= treeRect.bottom) return;
    tree.scrollTop += itemRect.top - treeRect.top - treeRect.height / 3;
  }

  function applyTreeState() {
    const tree = byId('file-tree');
    if (!tree) return;
    const saved = storageGetJSON(STORE_OPEN, null);
    if (Array.isArray(saved)) {
      const open = new Set(saved);
      for (const li of all('li.tree-folder[data-path]', tree)) {
        const details = folderDetails(li);
        if (details) details.open = open.has(li.dataset.path);
      }
    }
    const active = tree.querySelector('.tree-item.is-active');
    if (active) {
      revealTreeItem(active);
      if (active.tagName === 'A') active.setAttribute('aria-current', 'page');
    }
  }

  function saveTreeState() {
    const tree = byId('file-tree');
    if (!tree) return;
    const open = all('li.tree-folder[data-path]', tree)
      .filter((li) => {
        const details = folderDetails(li);
        return details && details.open;
      })
      .map((li) => li.dataset.path);
    storageSetJSON(STORE_OPEN, open);
  }

  const saveTreeStateSoon = debounce(saveTreeState, 50);

  function collapseAllFolders() {
    for (const details of all('#file-tree details')) details.open = false;
    storageSetJSON(STORE_OPEN, []);
  }

  function onToggle(event) {
    const target = event.target;
    if (target instanceof HTMLDetailsElement && target.closest('#file-tree')) saveTreeStateSoon();
  }

  // ---------------------------------------------------------------------------
  // Sidebars and layout

  function readLayout() {
    const layout = storageGetJSON(STORE_LAYOUT, null);
    return layout && typeof layout === 'object' ? layout : {};
  }

  function writeLayout(changes) {
    storageSetJSON(STORE_LAYOUT, Object.assign(readLayout(), changes));
  }

  function setOverlay(side) {
    if (side) root.dataset.overlay = side;
    else delete root.dataset.overlay;
    syncSidebarButtons();
  }

  function toggleSidebar(side) {
    if (narrowQuery.matches) {
      setOverlay(root.dataset.overlay === side ? '' : side);
      return;
    }
    const key = side === 'left' ? 'left' : 'right';
    const hidden = root.dataset[key] !== 'hidden';
    if (hidden) root.dataset[key] = 'hidden';
    else delete root.dataset[key];
    writeLayout(side === 'left' ? {leftHidden: hidden} : {rightHidden: hidden});
    syncSidebarButtons();
  }

  function syncSidebarButtons() {
    for (const side of ['left', 'right']) {
      const open = narrowQuery.matches ? root.dataset.overlay === side : root.dataset[side] !== 'hidden';
      for (const button of all('[data-action="toggle-' + side + '"]')) {
        button.setAttribute('aria-pressed', open ? 'true' : 'false');
        button.setAttribute('aria-controls', side);
      }
    }
  }

  function ensureBackdrop() {
    if (document.querySelector('.sidebar-backdrop')) return;
    const backdrop = h('div', {className: 'sidebar-backdrop', 'data-action': 'close-overlay', 'aria-hidden': 'true'});
    document.body.append(backdrop);
  }

  function startResize(event) {
    const resizer = event.target.closest('.resizer');
    if (!resizer || event.button !== 0 || narrowQuery.matches) return;
    const side = resizer.id === 'right-resizer' ? 'right' : 'left';
    const panel = byId(side);
    if (!panel) return;
    event.preventDefault();
    resizer.setPointerCapture(event.pointerId);
    resizer.classList.add('is-dragging');
    root.classList.add('is-resizing');
    const rect = panel.getBoundingClientRect();
    const variable = side === 'left' ? '--left-width' : '--right-width';
    let width = rect.width;
    const onMove = (move) => {
      const raw = side === 'left' ? move.clientX - rect.left : rect.right - move.clientX;
      width = Math.round(clamp(raw, MIN_SIDEBAR, Math.min(MAX_SIDEBAR, window.innerWidth * 0.45)));
      root.style.setProperty(variable, width + 'px');
    };
    const onEnd = () => {
      resizer.removeEventListener('pointermove', onMove);
      resizer.removeEventListener('pointerup', onEnd);
      resizer.removeEventListener('pointercancel', onEnd);
      resizer.classList.remove('is-dragging');
      root.classList.remove('is-resizing');
      writeLayout(side === 'left' ? {left: width} : {right: width});
    };
    resizer.addEventListener('pointermove', onMove);
    resizer.addEventListener('pointerup', onEnd);
    resizer.addEventListener('pointercancel', onEnd);
  }

  function resetResize(event) {
    const resizer = event.target.closest('.resizer');
    if (!resizer) return;
    const side = resizer.id === 'right-resizer' ? 'right' : 'left';
    root.style.removeProperty(side === 'left' ? '--left-width' : '--right-width');
    writeLayout(side === 'left' ? {left: 0} : {right: 0});
  }

  // ---------------------------------------------------------------------------
  // Chrome that lives inside swapped parts

  let liveState = '';

  function setLiveStatus(state) {
    liveState = state;
    const status = byId('live-status');
    if (!status) return;
    status.classList.toggle('is-connected', state === 'connected');
    status.classList.toggle('is-disconnected', state === 'disconnected');
    status.title = state === 'connected' ? 'Live reload connected' : state === 'disconnected' ? 'Live reload disconnected, retrying' : 'Live reload';
  }

  function syncChrome() {
    syncThemeButtons();
    syncSidebarButtons();
    setLiveStatus(liveState);
  }

  function watchViewScroll() {
    const container = view();
    const main = byId('main');
    if (!container || container.dataset.watched) return;
    container.dataset.watched = 'true';
    const onScroll = () => {
      if (main) main.classList.toggle('is-scrolled', container.scrollTop > 2);
      saveScrollSoon();
    };
    container.addEventListener('scroll', onScroll, {passive: true});
    container.addEventListener('wheel', noteUserScroll, {passive: true});
    container.addEventListener('touchmove', noteUserScroll, {passive: true});
    container.addEventListener('keydown', noteUserScroll);
    onScroll();
  }

  // ---------------------------------------------------------------------------
  // Outline scroll-spy

  let outlineScroll = null;

  function stopOutline() {
    if (!outlineScroll) return;
    outlineScroll.target.removeEventListener('scroll', outlineScroll.handler);
    cancelAnimationFrame(outlineScroll.frame);
    outlineScroll = null;
  }

  function startOutline() {
    stopOutline();
    const container = view();
    const links = all('#right .outline-list a[href^="#"]');
    if (!container || !links.length) return;
    const entries = [];
    for (const link of links) {
      let id = link.getAttribute('href').slice(1);
      try {
        id = decodeURIComponent(id);
      } catch (err) {
        // Keep the raw id.
      }
      const heading = document.getElementById(id);
      if (heading && container.contains(heading)) entries.push({heading, item: link.closest('li') || link});
    }
    if (!entries.length) return;
    let current = null;
    // The active section is the last heading at or above a line just below
    // the top of the view; at the end of the page, the last visible heading.
    const update = () => {
      const top = container.getBoundingClientRect().top;
      const line = top + Math.max(64, container.clientHeight * 0.12);
      let active = null;
      for (const entry of entries) {
        if (entry.heading.getBoundingClientRect().top <= line) active = entry;
        else break;
      }
      const atBottom = container.scrollTop + container.clientHeight >= container.scrollHeight - 4;
      if (atBottom && container.scrollTop > 0) {
        const bottom = top + container.clientHeight;
        for (const entry of entries) {
          if (entry.heading.getBoundingClientRect().top < bottom) active = entry;
        }
      }
      if (active === current) return;
      if (current) current.item.classList.remove('is-active');
      current = active;
      if (!current) return;
      current.item.classList.add('is-active');
      const pane = byId('right');
      if (pane && pane.scrollHeight > pane.clientHeight) {
        const itemRect = current.item.getBoundingClientRect();
        const paneRect = pane.getBoundingClientRect();
        if (itemRect.top < paneRect.top || itemRect.bottom > paneRect.bottom) {
          pane.scrollTop += itemRect.top - paneRect.top - paneRect.height / 3;
        }
      }
    };
    const state = {target: container, frame: 0, handler: null};
    state.handler = () => {
      if (state.frame) return;
      state.frame = requestAnimationFrame(() => {
        state.frame = 0;
        update();
      });
    };
    container.addEventListener('scroll', state.handler, {passive: true});
    outlineScroll = state;
    update();
  }

  // ---------------------------------------------------------------------------
  // Recent notes (for the quick switcher's empty state)

  function recordRecent() {
    const kind = document.body.dataset.kind;
    const path = document.body.dataset.path;
    if (!path || !(kind === 'note' || (kind === 'diff' && /\.(md|markdown)$/i.test(path)))) return;
    const recent = storageGetJSON(STORE_RECENT, []);
    const list = Array.isArray(recent) ? recent.filter((item) => item !== path) : [];
    list.unshift(path);
    storageSetJSON(STORE_RECENT, list.slice(0, 30));
  }

  // ---------------------------------------------------------------------------
  // Modal: the frame of the prompt and of the lightbox

  let modal = null;

  /** Shows `dialog` as the one open modal. */
  function showModal(id, dialog, onClose) {
    closeModal();
    const backdrop = h('div', {className: 'modal-backdrop', 'data-modal': id}, [dialog]);
    backdrop.addEventListener('mousedown', (event) => {
      if (event.target === backdrop) closeModal();
    });
    modal = {backdrop, dialog, returnFocus: document.activeElement, onClose};
    document.body.append(backdrop);
    return modal;
  }

  function closeModal() {
    if (!modal) return false;
    const current = modal;
    modal = null;
    current.backdrop.remove();
    if (current.onClose) current.onClose();
    if (current.returnFocus && current.returnFocus.isConnected) current.returnFocus.focus({preventScroll: true});
    return true;
  }

  // ---------------------------------------------------------------------------
  // Lightbox: a diagram, an image, or block math at the size of the window

  const LIGHTBOX_SOURCE = 'pre.mermaid.is-rendered, img:not(.is-unresolved), .math-block.is-rendered';
  const LIGHTBOX_MARGIN = 24;
  const LIGHTBOX_MIN_ZOOM = 0.1;
  const LIGHTBOX_MAX_ZOOM = 8;

  /** Returns the insert that a click on `target` opens, or null. A link wins. */
  function lightboxSource(target) {
    const source = target.closest(LIGHTBOX_SOURCE);
    if (!source || !source.closest('.markdown-rendered') || target.closest('a')) return null;
    // A drag that selects text also ends with a click.
    return getSelection().isCollapsed ? source : null;
  }

  /** Returns a copy of `source` at its natural size, or null for an image with no size. */
  function lightboxCopy(source) {
    if (source instanceof HTMLImageElement) {
      // An SVG file can have no size of its own. Then the size on the page applies.
      const width = source.naturalWidth || source.width;
      const height = source.naturalHeight || source.height;
      return width && height ? h('img', {src: source.currentSrc || source.src, alt: source.alt, width, height}) : null;
    }
    const svg = source.querySelector(':scope > svg');
    if (!svg) return source.cloneNode(true);
    // Only the SVG: a copy of the pre.mermaid would be reset on a theme change.
    const copy = svg.cloneNode(true);
    const width = mermaidWidth(svg);
    if (width) Object.assign(copy.style, {width: width + 'px', minWidth: '', maxWidth: ''});
    return copy;
  }

  function openLightbox(source) {
    const copy = lightboxCopy(source);
    if (!copy) return;
    closePreview();
    const content = h('div', {className: 'lightbox-content'}, [copy]);
    const stage = h('div', {className: 'lightbox-stage'}, [content]);
    const view = {x: 0, y: 0, k: 1};
    const apply = () => {
      content.style.transform = `translate(${view.x}px, ${view.y}px) scale(${view.k})`;
    };
    const fitScale = () =>
      Math.min(1, (stage.clientWidth - 2 * LIGHTBOX_MARGIN) / content.offsetWidth, (stage.clientHeight - 2 * LIGHTBOX_MARGIN) / content.offsetHeight);
    const fit = () => {
      view.k = fitScale();
      view.x = (stage.clientWidth - content.offsetWidth * view.k) / 2;
      view.y = (stage.clientHeight - content.offsetHeight * view.k) / 2;
      apply();
    };
    const zoomAt = (sx, sy, factor) => {
      zoomView(view, sx, sy, factor, Math.min(LIGHTBOX_MIN_ZOOM, fitScale()), LIGHTBOX_MAX_ZOOM);
      apply();
    };
    const zoom = (factor) => zoomAt(stage.clientWidth / 2, stage.clientHeight / 2, factor);
    const button = (name, label, run) => {
      const node = h('button', {type: 'button', className: 'icon-button', 'aria-label': label, title: label}, [icon(name)]);
      node.addEventListener('click', run);
      return node;
    };
    const dialog = h('div', {className: 'lightbox', role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Lightbox', tabindex: '-1'}, [
      stage,
      h('div', {className: 'lightbox-toolbar'}, [
        button('i-zoom-out', 'Zoom out (-)', () => zoom(1 / ZOOM_STEP)),
        button('i-zoom-in', 'Zoom in (+)', () => zoom(ZOOM_STEP)),
        button('i-fit', 'Fit to the window (0)', fit),
        button('i-close', 'Close (Escape)', closeModal),
      ]),
    ]);
    dialog.addEventListener('keydown', (event) => zoomKey(event, zoom, fit));
    stage.addEventListener(
      'wheel',
      (event) => {
        event.preventDefault();
        const rect = stage.getBoundingClientRect();
        zoomAt(event.clientX - rect.left, event.clientY - rect.top, wheelZoomFactor(event));
      },
      {passive: false},
    );
    // Pan: the offset of the view from the pointer stays the same during a drag.
    let grab = null;
    stage.addEventListener('pointerdown', (event) => {
      if (event.button !== 0) return;
      stage.setPointerCapture(event.pointerId);
      grab = {x: view.x - event.clientX, y: view.y - event.clientY};
    });
    stage.addEventListener('pointermove', (event) => {
      if (!stage.hasPointerCapture(event.pointerId)) return;
      view.x = grab.x + event.clientX;
      view.y = grab.y + event.clientY;
      apply();
    });

    const app = document.querySelector('.app');
    showModal('lightbox', dialog, () => {
      app.inert = false;
      document.removeEventListener('docsview:themechange', closeModal);
    });
    // The page behind the lightbox gets no focus.
    app.inert = true;
    // The copy has the colors of the theme at the time of the click.
    document.addEventListener('docsview:themechange', closeModal);
    dialog.focus();
    fit();
  }

  // ---------------------------------------------------------------------------
  // Modal prompt shared by the quick switcher and search

  /**
   * Opens a prompt modal. `config.onInput(value)` renders results as the user
   * types; the caller renders the initial state once it has set up.
   * `config.onKeyDown(event)` may handle keys first.
   */
  function openPrompt(config) {
    const input = h('input', {
      className: 'prompt-input',
      type: 'search',
      placeholder: config.placeholder,
      'aria-label': config.label,
      autocomplete: 'off',
      spellcheck: 'false',
      role: 'combobox',
      'aria-expanded': 'true',
      'aria-controls': config.id + '-results',
      'aria-autocomplete': 'list',
    });
    const results = h(config.listTag || 'div', {className: config.listClass || 'prompt-body', id: config.id + '-results', role: 'listbox', 'aria-label': config.label});
    const instructions = h('div', {className: 'prompt-instructions'});
    for (const [key, text] of config.instructions || []) {
      instructions.append(h('span', null, [h('kbd', {text: key}), text]));
    }
    const dialog = h('div', {className: 'prompt ' + (config.className || ''), role: 'dialog', 'aria-modal': 'true', 'aria-label': config.label}, [
      h('div', {className: 'prompt-input-container'}, [icon(config.icon || 'i-search'), input]),
      results,
      instructions,
    ]);
    Object.assign(showModal(config.id, dialog, config.onClose), {input, results, instructions});
    input.addEventListener('input', () => config.onInput(input.value));
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        closeModal();
        return;
      }
      if (config.onKeyDown) config.onKeyDown(event);
    });
    dialog.addEventListener('keydown', (event) => {
      if (event.key === 'Tab') {
        // Keep focus in the prompt: the input drives the list.
        event.preventDefault();
        input.focus();
      }
    });
    if (config.initial) {
      input.value = config.initial;
      input.select();
    }
    input.focus();
    return modal;
  }

  /** Keyboard selection over the `selector` items inside a prompt. */
  function createSelection(container, selector, input) {
    let index = -1;
    const items = () => all(selector, container);
    const select = (next) => {
      const list = items();
      if (!list.length) {
        index = -1;
        input.removeAttribute('aria-activedescendant');
        return;
      }
      index = (next + list.length) % list.length;
      list.forEach((item, i) => {
        item.classList.toggle('is-selected', i === index);
        item.setAttribute('aria-selected', i === index ? 'true' : 'false');
        if (!item.id) item.id = container.id + '-opt-' + i;
      });
      input.setAttribute('aria-activedescendant', list[index].id);
      list[index].scrollIntoView({block: 'nearest'});
    };
    return {
      reset() {
        index = -1;
        select(0);
      },
      move(delta) {
        select(index < 0 ? 0 : index + delta);
      },
      current() {
        return items()[index] || null;
      },
      track(item) {
        const i = items().indexOf(item);
        if (i >= 0 && i !== index) select(i);
      },
    };
  }

  function openLink(link, newTab) {
    if (!link) return;
    const href = link.getAttribute('href');
    if (newTab) {
      window.open(new URL(href, location.href).href, '_blank', 'noopener');
      return;
    }
    closeModal();
    navigate(href, {push: true});
  }

  function promptKeys(event, selection) {
    if (event.key === 'ArrowDown' || (event.ctrlKey && event.key === 'n')) {
      event.preventDefault();
      selection.move(1);
    } else if (event.key === 'ArrowUp' || (event.ctrlKey && event.key === 'p')) {
      event.preventDefault();
      selection.move(-1);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      openLink(selection.current(), event.ctrlKey || event.metaKey);
    }
  }

  // ---------------------------------------------------------------------------
  // Quick switcher

  let indexPromise = null;

  function loadIndex() {
    if (!indexPromise) {
      indexPromise = fetch('/_/api/index', {headers: {Accept: 'application/json'}, credentials: 'same-origin'})
        .then((response) => {
          if (!response.ok) throw new Error('index ' + response.status);
          return response.json();
        })
        .then((data) => (Array.isArray(data && data.notes) ? data.notes : []));
      indexPromise.catch(() => {
        indexPromise = null;
      });
    }
    return indexPromise;
  }

  const WORD_BOUNDARY = /[\s/_\-.()[\]#]/;

  /**
   * Fuzzy-matches `query` against `text`. Returns {score, positions} or null.
   * Contiguous and word-start matches score higher.
   */
  function fuzzyMatch(query, text) {
    if (!query) return {score: 0, positions: []};
    if (!text) return null;
    const lower = text.toLowerCase();
    const q = query.toLowerCase();
    const at = lower.indexOf(q);
    if (at >= 0) {
      let best = at;
      for (let i = at; i >= 0; i = lower.indexOf(q, i + 1)) {
        if (i === 0 || WORD_BOUNDARY.test(lower[i - 1])) {
          best = i;
          break;
        }
      }
      const boundary = best === 0 || WORD_BOUNDARY.test(lower[best - 1]);
      const positions = [];
      for (let i = 0; i < q.length; i++) positions.push(best + i);
      const score = 100 + q.length * 4 + (boundary ? 30 : 0) + (best === 0 ? 20 : 0) - best * 0.2 - (lower.length - q.length) * 0.05;
      return {score, positions};
    }
    let score = 0;
    let from = 0;
    let previous = -2;
    const positions = [];
    for (const ch of q) {
      if (ch === ' ') continue;
      let found = -1;
      // Prefer the next word-start occurrence when the next character is not adjacent.
      if (lower[from] === ch) {
        found = from;
      } else {
        const next = lower.indexOf(ch, from);
        if (next < 0) return null;
        found = next;
        for (let i = next; i >= 0 && i < lower.length; i = lower.indexOf(ch, i + 1)) {
          if (WORD_BOUNDARY.test(lower[i - 1] || ' ')) {
            found = i;
            break;
          }
        }
      }
      if (found === previous + 1) score += 6;
      else score -= Math.min(found - from, 12) * 0.4;
      if (found === 0 || WORD_BOUNDARY.test(lower[found - 1])) score += 8;
      score += 1;
      positions.push(found);
      previous = found;
      from = found + 1;
    }
    return {score, positions};
  }

  function displayTitle(note) {
    return note.title && note.title.trim() ? note.title : note.name;
  }

  function matchNote(note, query) {
    const tokens = query.split(/\s+/).filter(Boolean);
    const fields = [
      {key: 'title', text: displayTitle(note), weight: 1},
      {key: 'name', text: note.name, weight: 1},
      {key: 'path', text: note.path, weight: 0.55},
    ];
    for (const alias of note.aliases || []) fields.push({key: 'alias', text: alias, weight: 0.95});
    let total = 0;
    const marks = {title: [], path: []};
    let alias = null;
    for (const token of tokens) {
      let best = null;
      for (const field of fields) {
        const match = fuzzyMatch(token, field.text);
        if (!match) continue;
        const score = match.score * field.weight;
        if (!best || score > best.score) best = {score, field, match};
      }
      if (!best) return null;
      total += best.score;
      if (best.field.key === 'title') marks.title.push(...best.match.positions);
      else if (best.field.key === 'path') marks.path.push(...best.match.positions);
      else if (best.field.key === 'alias') alias = best.field.text;
      else if (best.field.key === 'name') {
        const offset = note.path.length - note.name.length - (note.path.endsWith('.md') ? 3 : 0);
        marks.path.push(...best.match.positions.map((p) => p + offset));
        if (displayTitle(note) === note.name) marks.title.push(...best.match.positions);
      }
    }
    return {note, score: total, marks, alias};
  }

  function switcherItem(href, title, titleMarks, note, noteMarks, aux) {
    const titleLine = h('span', {className: 'suggestion-title'});
    const titleText = h('span');
    appendMarkedPositions(titleText, title, titleMarks);
    titleLine.append(titleText);
    if (aux) titleLine.append(h('span', {className: 'suggestion-aux' + (aux.heading ? ' is-heading' : ''), text: aux.text}));
    const noteLine = h('span', {className: 'suggestion-note'});
    appendMarkedPositions(noteLine, note, noteMarks);
    return h('a', {className: 'suggestion-item', href, role: 'option', tabindex: '-1'}, [titleLine, noteLine]);
  }

  function switcherResults(notes, query) {
    const trimmed = query.trim();
    const items = [];
    const hashAt = trimmed.indexOf('#');
    if (hashAt >= 0) {
      // "note#heading" searches headings; "#heading" searches the current note.
      const noteQuery = trimmed.slice(0, hashAt).trim();
      const headingQuery = trimmed.slice(hashAt + 1).trim();
      const currentPath = document.body.dataset.path;
      const candidates = noteQuery
        ? notes.map((note) => matchNote(note, noteQuery)).filter(Boolean).sort((a, b) => b.score - a.score).slice(0, 8).map((m) => m.note)
        : notes.filter((note) => note.path === currentPath);
      const headings = [];
      for (const note of candidates) {
        for (const heading of note.headings || []) {
          const match = fuzzyMatch(headingQuery, heading.text);
          if (match) headings.push({note, heading, match});
        }
      }
      headings.sort((a, b) => b.match.score - a.match.score);
      for (const entry of headings.slice(0, 50)) {
        items.push(switcherItem(pathURL(entry.note.path, entry.heading.id), entry.heading.text, entry.match.positions, displayTitle(entry.note) + ' › ' + entry.note.path, [], {text: 'H' + entry.heading.level, heading: true}));
      }
      return items;
    }
    if (!trimmed) {
      const recent = storageGetJSON(STORE_RECENT, []);
      const order = new Map((Array.isArray(recent) ? recent : []).map((path, i) => [path, i]));
      const sorted = notes.slice().sort((a, b) => {
        const ra = order.has(a.path) ? order.get(a.path) : Infinity;
        const rb = order.has(b.path) ? order.get(b.path) : Infinity;
        return ra - rb || a.path.localeCompare(b.path);
      });
      for (const note of sorted.slice(0, 50)) items.push(switcherItem(pathURL(note.path), displayTitle(note), [], note.path, []));
      return items;
    }
    const matches = notes.map((note) => matchNote(note, trimmed)).filter(Boolean);
    matches.sort((a, b) => b.score - a.score || a.note.path.localeCompare(b.note.path));
    for (const match of matches.slice(0, 50)) {
      const aux = match.alias ? {text: match.alias} : null;
      items.push(switcherItem(pathURL(match.note.path), displayTitle(match.note), match.marks.title, match.note.path, match.marks.path, aux));
    }
    return items;
  }

  function openSwitcher() {
    let notes = null;
    let failed = false;
    let selection = null;
    const prompt = openPrompt({
      id: 'switcher',
      label: 'Quick switcher',
      placeholder: 'Find a note or #heading…',
      icon: 'i-switcher',
      listTag: 'div',
      listClass: 'prompt-results',
      instructions: [
        ['↑↓', 'to navigate'],
        ['↵', 'to open'],
        ['ctrl ↵', 'to open in new tab'],
        ['esc', 'to dismiss'],
      ],
      onInput: (value) => render(value),
      onKeyDown: (event) => selection && promptKeys(event, selection),
    });
    selection = createSelection(prompt.results, '.suggestion-item', prompt.input);
    prompt.results.addEventListener('mousemove', (event) => {
      const item = event.target.closest('.suggestion-item');
      if (item) selection.track(item);
    });
    function render(value) {
      if (!modal || modal.input !== prompt.input) return;
      if (failed) {
        prompt.results.replaceChildren(h('p', {className: 'prompt-empty', text: 'The note index could not be loaded. Check that the docs server is running.'}));
        return;
      }
      if (!notes) {
        prompt.results.replaceChildren(h('p', {className: 'prompt-empty', text: 'Loading notes…'}));
        return;
      }
      const items = switcherResults(notes, value);
      if (!items.length) {
        prompt.results.replaceChildren(h('p', {className: 'prompt-empty', text: 'No notes match “' + value.trim() + '”.'}));
      } else {
        prompt.results.replaceChildren(...items);
      }
      selection.reset();
    }
    render(prompt.input.value);
    loadIndex().then(
      (list) => {
        notes = list;
        render(prompt.input.value);
      },
      () => {
        failed = true;
        render(prompt.input.value);
      },
    );
  }

  // ---------------------------------------------------------------------------
  // Search

  let lastSearch = '';

  function snippet(text, terms) {
    const max = 180;
    if (text.length <= max) return text;
    const lower = text.toLowerCase();
    let first = -1;
    for (const term of terms || []) {
      const i = term ? lower.indexOf(term.toLowerCase()) : -1;
      if (i >= 0 && (first < 0 || i < first)) first = i;
    }
    const start = Math.max(0, first - 60);
    const end = Math.min(text.length, start + max);
    return (start > 0 ? '…' : '') + text.slice(start, end) + (end < text.length ? '…' : '');
  }

  function firstTermIn(text, terms) {
    const lower = text.toLowerCase();
    let best = null;
    let bestAt = Infinity;
    for (const term of terms || []) {
      if (!term) continue;
      const at = lower.indexOf(term.toLowerCase());
      if (at >= 0 && at < bestAt) {
        best = text.substr(at, term.length);
        bestAt = at;
      }
    }
    return best;
  }

  /**
   * Builds search results in the markup of the server's /_/search page:
   * ol.search-results > li.search-result > a.search-result-title +
   * span.search-result-path + ul.search-matches > li.search-match > a >
   * span.search-line + text (with <mark> around the terms here).
   */
  function buildSearchResults(data) {
    const terms = Array.isArray(data.terms) ? data.terms : [];
    const list = h('ol', {className: 'search-results'});
    for (const result of data.results || []) {
      const matches = h('ul', {className: 'search-matches'});
      for (const match of result.matches || []) {
        const text = String(match.text == null ? '' : match.text);
        const term = firstTermIn(text, terms);
        const href = pathURL(result.path) + (term ? '#' + textDirective(term) : '');
        const link = h('a', {href}, [h('span', {className: 'search-line', text: String(match.line)})]);
        appendHighlighted(link, snippet(text.trim(), terms), terms);
        matches.append(h('li', {className: 'search-match'}, [link]));
      }
      list.append(h('li', {className: 'search-result', 'data-path': result.path}, [
        h('a', {className: 'search-result-title', href: pathURL(result.path), 'data-path': result.path, text: result.title || result.name}),
        h('span', {className: 'search-result-path', text: result.path}),
        matches.childElementCount ? matches : null,
      ]));
    }
    return list;
  }

  function searchHint() {
    const rows = [
      ['word other', 'notes with both words'],
      ['"exact phrase"', 'the phrase as written'],
      ['tag:#name', 'notes with a tag'],
      ['path:ref/', 'files under a path'],
      ['file:name', 'files by name'],
    ];
    const list = h('dl');
    for (const [syntax, meaning] of rows) list.append(h('dt', {text: syntax}), h('dd', {text: meaning}));
    return h('div', {className: 'search-hint'}, [h('span', {text: 'Search every note in the vault. Terms are case-insensitive.'}), list]);
  }

  function openSearch(initial) {
    let controller = null;
    let selection = null;
    const pageLink = h('a', {href: '/_/search', text: 'Open results as a page'});
    const status = h('span', {className: 'prompt-status'});
    const prompt = openPrompt({
      id: 'search',
      label: 'Search',
      placeholder: 'Search notes…',
      icon: 'i-search',
      initial: initial != null ? initial : lastSearch,
      instructions: [
        ['↑↓', 'to navigate'],
        ['↵', 'to open'],
        ['esc', 'to dismiss'],
      ],
      onInput: (value) => handleInput(value),
      onKeyDown: (event) => selection && promptKeys(event, selection),
      onClose: () => controller && controller.abort(),
    });
    prompt.instructions.append(status, pageLink);
    selection = createSelection(prompt.results, '.search-result-title, .search-match > a', prompt.input);
    prompt.results.addEventListener('mousemove', (event) => {
      const item = event.target.closest('.search-result-title, .search-match > a');
      if (item) selection.track(item);
    });
    const run = async (value) => {
      const query = value.trim();
      if (controller) controller.abort();
      if (!query) {
        status.textContent = '';
        prompt.results.replaceChildren(searchHint());
        return;
      }
      controller = new AbortController();
      const signal = controller.signal;
      status.textContent = 'Searching…';
      let data;
      try {
        const response = await fetch('/_/api/search?q=' + encodeURIComponent(query), {headers: {Accept: 'application/json'}, credentials: 'same-origin', signal});
        if (!response.ok) throw new Error('search ' + response.status);
        data = await response.json();
      } catch (err) {
        if (signal.aborted) return;
        status.textContent = '';
        prompt.results.replaceChildren(h('p', {className: 'search-empty', text: 'Search failed. Check that the docs server is running.'}));
        return;
      }
      if (signal.aborted || !modal || modal.input !== prompt.input) return;
      const results = data.results || [];
      const total = typeof data.total === 'number' ? data.total : results.length;
      status.textContent = total === 1 ? '1 file' : total + ' files' + (data.truncated ? ' (showing the first ' + results.length + ')' : '');
      if (!results.length) {
        prompt.results.replaceChildren(h('p', {className: 'search-empty', text: 'No notes match “' + query + '”.'}));
      } else {
        prompt.results.replaceChildren(buildSearchResults(data));
      }
      selection.reset();
    };
    const debounced = debounce(run, 150);
    function handleInput(value) {
      lastSearch = value;
      pageLink.setAttribute('href', '/_/search?q=' + encodeURIComponent(value.trim()));
      if (!value.trim()) run(value);
      else debounced(value);
    }
    handleInput(prompt.input.value);
  }

  // ---------------------------------------------------------------------------
  // Hover previews

  let hoverTimer = 0;
  let hideTimer = 0;
  let hoverLink = null;
  let popover = null;
  let previewToken = 0;

  function closePreview() {
    clearTimeout(hoverTimer);
    clearTimeout(hideTimer);
    hoverLink = null;
    previewToken++;
    if (!popover) return false;
    popover.remove();
    popover = null;
    return true;
  }

  function scheduleHide() {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(closePreview, 250);
  }

  function positionPopover(link) {
    if (!popover || !link.isConnected) return;
    const rect = link.getBoundingClientRect();
    const width = popover.offsetWidth;
    const height = popover.offsetHeight;
    const margin = 8;
    let top = rect.bottom + 6;
    if (top + height > window.innerHeight - margin && rect.top - 6 - height >= margin) top = rect.top - 6 - height;
    top = clamp(top, margin, Math.max(margin, window.innerHeight - height - margin));
    const left = clamp(rect.left, margin, Math.max(margin, window.innerWidth - width - margin));
    popover.style.top = Math.round(top) + 'px';
    popover.style.left = Math.round(left) + 'px';
  }

  async function showPreview(link) {
    const token = ++previewToken;
    const url = new URL(link.href, location.href);
    let doc;
    try {
      doc = await cachedDocument(url.href);
    } catch (err) {
      return;
    }
    if (token !== previewToken || hoverLink !== link || !link.isConnected) return;
    const source = doc.querySelector('#content .markdown-body') || doc.querySelector('#content') || doc.querySelector('#view');
    if (!source) return;
    if (popover) popover.remove();
    const content = h('div', {className: 'popover-content markdown-rendered'});
    const title = doc.querySelector('#content .inline-title');
    if (title) content.append(h('div', {className: 'popover-title', text: title.textContent}));
    const properties = source.classList.contains('markdown-body') && doc.querySelector('#content > .properties');
    if (properties) content.append(document.importNode(properties, true));
    content.append(document.importNode(source, true));
    popover = h('div', {className: 'hover-popover', role: 'tooltip'}, [content]);
    popover.addEventListener('mouseenter', () => clearTimeout(hideTimer));
    popover.addEventListener('mouseleave', scheduleHide);
    document.body.append(popover);
    positionPopover(link);
    const current = popover;
    const scrollToFragment = () => {
      if (current !== popover || !url.hash) return;
      const raw = url.hash.slice(1);
      let id = raw.split(':~:')[0];
      try {
        id = decodeURIComponent(id);
      } catch (err) {
        // Keep the raw id.
      }
      const target = id ? current.querySelector('[id="' + CSS.escape(id) + '"]') : null;
      if (target) current.scrollTop = target.getBoundingClientRect().top - current.getBoundingClientRect().top + current.scrollTop - 8;
    };
    scrollToFragment();
    await enhance(content, {preview: true});
    if (current !== popover) return;
    positionPopover(link);
    scrollToFragment();
  }

  function onMouseOver(event) {
    if (!hoverQuery.matches || modal) return;
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    const link = target.closest('a.internal-link:not(.is-unresolved)');
    if (!link || link.closest('.hover-popover')) return;
    if (link === hoverLink) {
      clearTimeout(hideTimer);
      return;
    }
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => {
      clearTimeout(hideTimer);
      hoverLink = link;
      showPreview(link);
    }, HOVER_DELAY);
  }

  function onMouseOut(event) {
    const target = event.target instanceof Element ? event.target : null;
    const link = target && target.closest('a.internal-link');
    if (!link || (event.relatedTarget instanceof Node && link.contains(event.relatedTarget))) return;
    clearTimeout(hoverTimer);
    if (popover && event.relatedTarget instanceof Node && popover.contains(event.relatedTarget)) return;
    if (hoverLink) scheduleHide();
  }

  // ---------------------------------------------------------------------------
  // Live reload

  let events = null;
  let eventsPath = null;
  let lastVersion = null;
  let droppedConnection = false;
  let refreshing = null;
  let pendingRefresh = null;
  let toastTimer = 0;

  function parseEventData(event) {
    try {
      return JSON.parse(event.data) || {};
    } catch (err) {
      return {};
    }
  }

  function connectLive() {
    if (!window.EventSource) return;
    const path = document.body.dataset.path || '';
    if (events && eventsPath === path) return;
    if (events) events.close();
    eventsPath = path;
    lastVersion = null;
    droppedConnection = false;
    events = new EventSource('/_/events?path=' + encodeURIComponent(path));
    events.addEventListener('hello', (event) => {
      const data = parseEventData(event);
      setLiveStatus('connected');
      if (droppedConnection && lastVersion && data.version && data.version !== lastVersion) {
        refreshPage({page: true, tree: true});
      }
      droppedConnection = false;
      if (data.version) lastVersion = data.version;
    });
    events.addEventListener('change', (event) => {
      const data = parseEventData(event);
      if (data.version) lastVersion = data.version;
      refreshPage({page: !!data.page, tree: !!data.tree});
    });
    events.addEventListener('open', () => setLiveStatus('connected'));
    events.addEventListener('error', () => {
      droppedConnection = true;
      setLiveStatus('disconnected');
    });
  }

  function showToast(text) {
    let toast = document.querySelector('.toast');
    if (!toast) {
      toast = h('div', {className: 'toast', role: 'status', 'aria-live': 'polite'});
      document.body.append(toast);
    }
    toast.textContent = text;
    toast.classList.add('is-visible');
    const status = byId('live-status');
    if (status) status.classList.add('is-updated');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      toast.classList.remove('is-visible');
      const current = byId('live-status');
      if (current) current.classList.remove('is-updated');
    }, 1800);
  }

  function detailsState() {
    return all('#content details').map((details) => details.open);
  }

  function restoreDetailsState(states) {
    const list = all('#content details');
    if (list.length !== states.length) return;
    list.forEach((details, i) => {
      details.open = states[i];
    });
  }

  /** Refetches the current page after a change event and swaps parts. */
  function refreshPage(change) {
    if (!change.page && !change.tree) return Promise.resolve();
    if (refreshing) {
      pendingRefresh = {
        page: change.page || (pendingRefresh && pendingRefresh.page),
        tree: change.tree || (pendingRefresh && pendingRefresh.tree),
      };
      return refreshing;
    }
    refreshing = (async () => {
      if (change.tree) indexPromise = null;
      previewCache.clear();
      const token = navToken;
      let result;
      try {
        result = await fetchDocument(location.href);
      } catch (err) {
        return;
      }
      if (token !== navToken) return;
      const container = view();
      const scroll = container ? container.scrollTop : 0;
      const openStates = detailsState();
      if (change.page && document.body.dataset.kind === 'apispec' && result.doc.body.dataset.kind === 'apispec') {
        // Keep Redoc's container: swap the header, render the spec again.
        const header = result.doc.querySelector('.view-header');
        if (header) document.querySelector('.view-header').replaceWith(document.adoptNode(header));
        applyDocument(result.doc, {main: false, right: true, tree: change.tree ? 'swap' : 'active'});
        restoreDetailsState(openStates);
        await rerenderSpec();
      } else if (change.page && (document.body.dataset.kind === 'apispec' || result.doc.body.dataset.kind === 'apispec')) {
        loadFully(new URL(location.href));
        return;
      } else if (change.page) {
        const rendering = applyDocument(result.doc, {main: true, right: true, tree: change.tree ? 'swap' : 'active'});
        restoreDetailsState(openStates);
        settleScroll(() => {
          const current = view();
          if (current) current.scrollTop = scroll;
        }, rendering);
      } else {
        applyDocument(result.doc, {main: false, right: true, tree: 'swap'});
        if (document.body.dataset.kind === 'graph' && window.DocsGraph && window.DocsGraph.reload) window.DocsGraph.reload();
      }
      showToast(change.page ? 'Page updated' : 'Files updated');
    })().finally(() => {
      refreshing = null;
      if (pendingRefresh) {
        const next = pendingRefresh;
        pendingRefresh = null;
        refreshPage(next);
      }
    });
    return refreshing;
  }

  // ---------------------------------------------------------------------------
  // Keyboard shortcuts

  function onKeyDown(event) {
    const mod = event.ctrlKey || event.metaKey;
    const key = event.key.length === 1 ? event.key.toLowerCase() : event.key;
    if (mod && !event.altKey && !event.shiftKey && key === 'o') {
      event.preventDefault();
      openSwitcher();
    } else if (mod && !event.altKey && event.shiftKey && key === 'f') {
      event.preventDefault();
      openSearch();
    } else if (mod && !event.altKey && !event.shiftKey && key === 'g') {
      event.preventDefault();
      navigate('/_/graph', {push: true});
    } else if (!mod && !event.altKey && (key === 'n' || key === 'p') && !modal && !picker && document.querySelector('.diff-nav') && !event.target.closest('input, textarea, select, [contenteditable]')) {
      event.preventDefault();
      jumpChange(key === 'n' ? 1 : -1);
    } else if (key === 'Escape') {
      if (closeModal() || closePicker() || closePreview()) {
        event.preventDefault();
      } else if (root.dataset.overlay) {
        event.preventDefault();
        setOverlay('');
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Start-up

  function init() {
    if ('scrollRestoration' in history) history.scrollRestoration = 'manual';
    if (!document.querySelector('link[rel~="icon"]')) document.head.append(h('link', {rel: 'icon', type: 'image/svg+xml', href: FAVICON}));
    ensureBackdrop();
    document.body.append(h('div', {className: 'nav-progress', 'aria-hidden': 'true'}));

    document.addEventListener('click', onDocumentClick);
    document.addEventListener('submit', onSubmit);
    document.addEventListener('keydown', onKeyDown);
    document.addEventListener('toggle', onToggle, true);
    document.addEventListener('mouseover', onMouseOver);
    document.addEventListener('mouseout', onMouseOut);
    document.addEventListener('pointerdown', startResize);
    document.addEventListener('dblclick', resetResize);
    window.addEventListener('popstate', onPopState);
    window.addEventListener('pagehide', saveScrollState);
    window.addEventListener('blur', () => clearTimeout(hoverTimer));
    narrowQuery.addEventListener('change', () => setOverlay(''));
    darkQuery.addEventListener('change', onSystemThemeChange);

    applyTreeState();
    scrollTreeItemIntoView(document.querySelector('#file-tree .tree-item.is-active'));
    syncChrome();
    watchViewScroll();
    lastRendering = enhance(byId('main'));
    startOutline();
    connectLive();
    recordRecent();

    const saved = history.state && typeof history.state.scroll === 'number' ? history.state.scroll : null;
    if (saved != null) {
      settleScroll(() => {
        const container = view();
        if (container) container.scrollTop = saved;
      }, lastRendering);
    } else if (location.hash) {
      settleScroll(() => scrollToHash(location.hash, {flash: false}), lastRendering);
      flashTarget(location.hash);
    }
  }

  window.DocsViewer = {
    navigate: (href) => navigate(href, {push: true}),
    // refresh({page, tree}) re-fetches the current page; no argument refreshes both.
    refresh: (change) => refreshPage(change || {page: true, tree: true}),
    openSwitcher,
    openSearch,
    setTheme: (theme) => setTheme(theme === 'dark' ? 'dark' : 'light', true),
    enhance,
    // The zoom math that the graph shares with the lightbox.
    wheelZoomFactor,
    zoomView,
    zoomKey,
  };

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
