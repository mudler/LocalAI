/* Small helpers: theme toggle, install tabs, copy buttons, slow reveal. No tracking. */
(function () {
  var root = document.documentElement;
  var reduce = window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches;
  var t = document.getElementById('theme');
  if (t) t.addEventListener('click', function () {
    var dark = root.getAttribute('data-theme') === 'dark' ||
      (!root.getAttribute('data-theme') && matchMedia('(prefers-color-scheme: dark)').matches);
    var next = dark ? 'light' : 'dark';
    root.setAttribute('data-theme', next);
    try { localStorage.setItem('theme', next); } catch (e) {}
  });
  document.querySelectorAll('.tab').forEach(function (b) {
    b.addEventListener('click', function () {
      var wrap = b.closest('.install');
      wrap.querySelectorAll('.tab').forEach(function (x) { x.setAttribute('aria-selected', x === b ? 'true' : 'false'); });
      wrap.querySelectorAll('.pane').forEach(function (p) { p.hidden = p.id !== b.dataset.pane; });
      var c = wrap.querySelector('.cpy');
      if (c) { c.dataset.copy = b.dataset.copy; c.textContent = 'copy'; }
    });
  });
  document.querySelectorAll('.cpy').forEach(function (b) {
    b.addEventListener('click', function () {
      if (!navigator.clipboard) return;
      navigator.clipboard.writeText(b.dataset.copy).then(function () {
        b.textContent = 'copied'; setTimeout(function () { b.textContent = 'copy'; }, 1600);
      });
    });
  });
  var rv = document.querySelectorAll('.rv');
  if (reduce || !('IntersectionObserver' in window)) { rv.forEach(function (e) { e.classList.add('in'); }); return; }
  var io = new IntersectionObserver(function (es) {
    es.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); } });
  }, { threshold: 0.12 });
  rv.forEach(function (e) { io.observe(e); });
})();
