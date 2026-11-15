/* Small helpers: theme toggle, burger menu, install tabs, copy buttons, slow reveal,
   demo loops that play only on screen, the engines filter. No tracking. */
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

  // Burger menu below 860 px. The button toggles the panel; Escape closes it and gives
  // the focus back to the button; a click on a link or a wide screen closes it too.
  var menu = document.getElementById('menu'), nav = document.getElementById('site-nav');
  if (menu && nav) {
    var wide = matchMedia('(min-width: 860px)');
    var setMenu = function (open, focusFirst) {
      nav.classList.toggle('open', open);
      menu.setAttribute('aria-expanded', open ? 'true' : 'false');
      menu.setAttribute('aria-label', open ? 'Close menu' : 'Menu');
      if (open && focusFirst) { var a = nav.querySelector('a'); if (a) a.focus(); }
    };
    menu.addEventListener('click', function () {
      setMenu(menu.getAttribute('aria-expanded') !== 'true', true);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && nav.classList.contains('open')) { setMenu(false); menu.focus(); }
    });
    nav.addEventListener('click', function (e) { if (e.target.closest('a')) setMenu(false); });
    var onWide = function () { if (wide.matches) setMenu(false); };
    if (wide.addEventListener) wide.addEventListener('change', onWide); else wide.addListener(onWide);
  }

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

  // Reveal on scroll. The hidden state is switched on here, so a page whose script does not
  // run, or a visitor who asks for less motion, always sees all the content.
  var rv = [].slice.call(document.querySelectorAll('.rv'));
  if (rv.length && !reduce && 'IntersectionObserver' in window) {
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); } });
    }, { threshold: 0.1 });
    rv.forEach(function (e) {
      var r = e.getBoundingClientRect();
      if (r.top < innerHeight && r.bottom > 0) e.classList.add('in');
    });
    root.classList.add('rv-on');
    rv.forEach(function (e) { if (!e.classList.contains('in')) io.observe(e); });
  }

  // Demo loops: muted, looping, played only while on screen and only if motion is allowed.
  // With reduced motion the poster stays and the loop does not start.
  var vids = document.querySelectorAll('video[data-lazy]');
  if (vids.length && !reduce && 'IntersectionObserver' in window) {
    var vio = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        if (e.isIntersecting) { var p = e.target.play(); if (p && p.catch) p.catch(function () {}); }
        else e.target.pause();
      });
    }, { threshold: 0.25 });
    vids.forEach(function (v) { vio.observe(v); });
  }

  // Engines page filter.
  var chips = [].slice.call(document.querySelectorAll('.engf__c'));
  if (chips.length) {
    var cards = [].slice.call(document.querySelectorAll('.eng'));
    var groups = [].slice.call(document.querySelectorAll('.engg'));
    var none = document.querySelector('.engn');
    var apply = function (cat) {
      var shown = 0;
      cards.forEach(function (c) { var on = cat === 'all' || c.dataset.cat === cat; c.hidden = !on; if (on) shown++; });
      groups.forEach(function (g) { g.hidden = !(cat === 'all' || g.dataset.group === cat); });
      chips.forEach(function (c) { c.setAttribute('aria-pressed', String(c.dataset.cat === cat)); });
      if (none) none.hidden = shown > 0;
      try { history.replaceState(null, '', cat === 'all' ? location.pathname : location.pathname + '#' + cat); } catch (e) {}
    };
    chips.forEach(function (c) { c.addEventListener('click', function () { apply(c.dataset.cat); }); });
    var start = (location.hash || '').replace('#', '');
    if (start && chips.some(function (c) { return c.dataset.cat === start; })) apply(start);
  }
})();
