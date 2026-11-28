(function () {
  // Download block: pick the desktop app file for the visitor's system. The markup lists
  // all four direct links; this only chooses the primary button. macOS is always the
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
      if (/Windows|Win64|WOW64/i.test(ua)) return 'win';
      if (/Mac OS X|Macintosh/i.test(ua)) return 'mac';
      if (/CrOS/i.test(ua)) return 'other';
      if (/Linux|X11/i.test(ua)) {
        // The user agent is not trusted for the processor (Chrome always says x86_64).
        return 'linux';
      }
      return 'other';
    }
    function apply(kind) {
      if (kind === 'mac' || kind === 'win') show(kind);
      else if (kind === 'linux-x64' || kind === 'linux-arm64') show(kind);
      else if (kind === 'linux') { show(null, 'Choose your processor'); say('Your browser does not tell us the processor of this computer. Pick the file for it.'); }
      else { show(null, 'Desktop app downloads'); say('The desktop app is for macOS, Linux and Windows. On other systems, use the container or the install script.'); }
    }
    var uad = navigator.userAgentData;
    if (uad && uad.getHighEntropyValues) {
      uad.getHighEntropyValues(['architecture', 'bitness', 'platform']).then(function (v) {
        var plat = (v.platform || uad.platform || '').toLowerCase();
        if (/mac/.test(plat)) apply('mac');
        else if (/windows/.test(plat)) apply('win');
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
