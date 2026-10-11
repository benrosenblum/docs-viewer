// Applies the saved theme, sidebar layout, and word wrap before first paint.
'use strict';
(function () {
  var root = document.documentElement;
  var theme = null;
  var layout = null;
  try {
    theme = localStorage.getItem('docsview:theme');
    layout = JSON.parse(localStorage.getItem('docsview:layout') || 'null');
  } catch (err) {
    // Storage can be unavailable (private mode, blocked site data).
  }
  if (theme !== 'light' && theme !== 'dark') {
    var dark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
    theme = dark ? 'dark' : 'light';
  }
  root.dataset.theme = theme;
  if (layout && typeof layout === 'object') {
    if (layout.left > 0) root.style.setProperty('--left-width', layout.left + 'px');
    if (layout.right > 0) root.style.setProperty('--right-width', layout.right + 'px');
    if (layout.leftHidden) root.dataset.left = 'hidden';
    if (layout.rightHidden) root.dataset.right = 'hidden';
    if (layout.wrap) root.dataset.wrap = 'on';
  }
})();
