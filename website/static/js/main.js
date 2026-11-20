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

  // Hero film: a play button over the poster. The video never starts on its own.
  // The button starts it with sound and the browser controls take over.
  [].slice.call(document.querySelectorAll('[data-film]')).forEach(function (f) {
    var v = f.querySelector('video'), b = f.querySelector('[data-film-play]');
    if (!v || !b) return;
    v.removeAttribute('controls');
    f.classList.add('film-hero--ready');
    b.addEventListener('click', function () {
      v.setAttribute('controls', '');
      f.classList.add('film-hero--on');
      var p = v.play(); if (p && p.catch) p.catch(function () {});
      v.focus();
    });
    v.addEventListener('ended', function () { f.classList.remove('film-hero--on'); v.removeAttribute('controls'); v.load(); });
  });

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
  // Download block: pick the desktop app file for the visitor's system. The markup lists
  // all three direct links; this only chooses the primary button. macOS is always the
  // Apple silicon DMG, since a browser cannot tell the two Mac chips apart. A Linux visitor
  // whose CPU the browser does not report gets the list and no guess. Other systems get
  // the list and a line that points to the container and the script.
  document.querySelectorAll('[data-dl]').forEach(function (box) {
    var main = box.querySelector('[data-dl-main]'), label = box.querySelector('[data-dl-label]');
    var msg = box.querySelector('[data-dl-msg]');
    var opts = {};
    box.querySelectorAll('[data-dl-os]').forEach(function (a) { opts[a.dataset.dlOs] = a; });
    var done = false;
    function show(os, labelText) {
      if (done) return; done = true;
      var o = opts[os];
      if (o && main) {
        main.href = o.href;
        main.textContent = '';
        var ic = o.querySelector('.dlb__ico');
        if (ic) main.appendChild(ic.cloneNode(true));
        main.appendChild(document.createTextNode('Download for ' + o.querySelector('.dlb__t').textContent));
        main.hidden = false;
        o.setAttribute('aria-current', 'true');
        if (label) label.textContent = 'Other systems';
      } else if (label && labelText) { label.textContent = labelText; }
    }
    function say(t) { if (msg) { msg.textContent = t; msg.hidden = false; } }
    function fromUA() {
      var ua = navigator.userAgent || '';
      if (/iPhone|iPad|iPod|Android/i.test(ua)) return 'mobile';
      if (/Windows|Win64|WOW64/i.test(ua)) return 'other';
      if (/Mac OS X|Macintosh/i.test(ua)) return 'mac';
      if (/CrOS/i.test(ua)) return 'other';
      if (/Linux|X11/i.test(ua)) {
        // The user agent is not trusted for the processor (Chrome always says x86_64).
        return 'linux';
      }
      return 'other';
    }
    function apply(kind) {
      if (kind === 'mac') show('mac');
      else if (kind === 'linux-x64' || kind === 'linux-arm64') show(kind);
      else if (kind === 'linux') { show(null, 'Choose your processor'); say('Your browser does not tell us the processor of this computer. Pick the file for it.'); }
      else { show(null, 'Desktop app downloads'); say('The desktop app is for macOS and Linux. On other systems, use the container or the install script.'); }
    }
    var uad = navigator.userAgentData;
    if (uad && uad.getHighEntropyValues) {
      uad.getHighEntropyValues(['architecture', 'bitness', 'platform']).then(function (v) {
        var plat = (v.platform || uad.platform || '').toLowerCase();
        if (/mac/.test(plat)) apply('mac');
        else if (/linux/.test(plat)) {
          var a = (v.architecture || '').toLowerCase();
          if (a === 'x86' && v.bitness !== '32') apply('linux-x64');
          else if (a === 'arm' && v.bitness !== '32') apply('linux-arm64');
          else apply('linux');
        } else apply(/android|ios/.test(plat) ? 'mobile' : fromUA() === 'mobile' ? 'mobile' : 'other');
      }, function () { apply(fromUA()); });
    } else apply(fromUA());
  });
})();
