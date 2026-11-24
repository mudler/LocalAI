// A hostile stand-in for the LocalAI server, for scripts/webui-security.sh.
// It answers /readyz like the server, but its /app page tries to get out of
// the WebUI view. A second port is "another origin" for the attempts.
// Settings: FAKE_PORT (the server), FAKE_EVIL_PORT (the other origin).
const http = require('node:http')

const port = Number(process.env.FAKE_PORT)
const evilPort = Number(process.env.FAKE_EVIL_PORT || port + 1)
const EVIL = `http://127.0.0.1:${evilPort}`
const hits = []

const page = `<!doctype html>
<html><head><meta charset="utf-8"><title>hostile</title></head>
<body>
<h1>hostile page</h1>
<button id="mic" style="width:300px;height:80px">microphone</button>
<button id="clip" style="width:300px;height:80px">clipboard</button>
<pre id="out"></pre>
<script>
const EVIL = ${JSON.stringify(EVIL)}
const log = (k, v) => { (window.results ||= {})[k] = v; document.getElementById('out').textContent += k + ': ' + v + '\\n' }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const click = (href, attrs = {}) => { const a = document.createElement('a'); a.href = href; Object.assign(a, attrs); document.body.appendChild(a); a.click(); a.remove() }
document.getElementById('mic').onclick = () =>
  navigator.mediaDevices.getUserMedia({ audio: true }).then(() => log('mic-with-gesture', 'granted'), (e) => log('mic-with-gesture', 'refused ' + e.name))
document.getElementById('clip').onclick = () =>
  navigator.clipboard.writeText('from page').then(() => log('clipboard-write-with-gesture', 'written'), (e) => log('clipboard-write-with-gesture', 'refused ' + e.name))
window.attempts = {
  async bridge() {
    return { launcher: typeof window.launcher, require: typeof window.require, process: typeof window.process,
      ipcRenderer: typeof window.ipcRenderer, electron: typeof window.electron, module: typeof window.module }
  },
  async windowOpen() { const w = window.open(EVIL + '/popup', '_blank'); return w === null ? 'null' : 'window object' },
  async windowOpenJs() { const w = window.open('javascript:alert(1)'); return w === null ? 'null' : 'window object' },
  async targetBlank() { click(EVIL + '/blank', { target: '_blank' }); await sleep(300); return location.href },
  async fileNav() { try { location.href = 'file:///etc/passwd' } catch (e) { return 'threw ' + e.name } await sleep(500); return location.href },
  async dataNav() { location.href = 'data:text/html,<h1>x</h1>'; await sleep(500); return location.href },
  async customProto() { location.href = 'launcher-evil://run'; await sleep(500); return location.href },
  async jsLink() { window.__js = 0; click('javascript:window.__js=1'); await sleep(100); return 'ran in page: ' + (window.__js === 1) },
  async externalNav() { click(EVIL + '/elsewhere'); await sleep(500); return location.href },
  async redirect() { click('/redirect'); await sleep(800); return location.href },
  async iframe() {
    const f = document.createElement('iframe'); f.src = EVIL + '/frame'; document.body.appendChild(f); await sleep(800)
    let href; try { href = f.contentWindow.location.href } catch (e) { href = 'cross-origin (' + e.name + ')' }
    return href
  },
  async dataIframe() {
    const f = document.createElement('iframe'); f.src = 'data:text/html,<script>parent.postMessage("data-frame-ran","*")<\\/script>'; document.body.appendChild(f)
    const ran = await new Promise((r) => { const t = setTimeout(() => r(false), 800); addEventListener('message', (m) => { if (m.data === 'data-frame-ran') { clearTimeout(t); r(true) } }) })
    return 'script in data: iframe ran: ' + ran
  },
  async micNoGesture() { try { await navigator.mediaDevices.getUserMedia({ audio: true }); return 'granted' } catch (e) { return 'refused ' + e.name } },
  async clipboardRead() { try { await navigator.clipboard.readText(); return 'read' } catch (e) { return 'refused ' + e.name } },
  async notifications() { return await Notification.requestPermission() },
  async geolocation() { return await new Promise((r) => navigator.geolocation.getCurrentPosition(() => r('granted'), (e) => r('refused code ' + e.code), { timeout: 2000 })) },
  async displayCapture() { try { await navigator.mediaDevices.getDisplayMedia({ video: true }); return 'granted' } catch (e) { return 'refused ' + e.name } },
  async midi() { try { await navigator.requestMIDIAccess(); return 'granted' } catch (e) { return 'refused ' + e.name } },
  async usb() { try { await navigator.usb.requestDevice({ filters: [] }); return 'granted' } catch (e) { return 'refused ' + e.name } },
  async fetchFile() { try { await fetch('file:///etc/passwd'); return 'read' } catch (e) { return 'refused ' + e.name } },
  async download() { click('/download'); await sleep(500); return 'clicked' },
  async blobDownload() {
    const u = URL.createObjectURL(new Blob(['x'])); click(u, { download: '../../.bashrc' }); await sleep(500); return 'clicked'
  },
}
</script></body></html>`

http
  .createServer((req, res) => {
    hits.push(req.url)
    if (req.url === '/readyz') return res.end('{}')
    if (req.url === '/hits') return res.end(JSON.stringify(hits))
    if (req.url === '/app' || req.url.startsWith('/app?') || req.url.startsWith('/app/')) {
      res.setHeader('content-type', 'text/html; charset=utf-8')
      return res.end(page)
    }
    if (req.url === '/redirect') {
      res.statusCode = 302
      res.setHeader('location', `${EVIL}/redirected`)
      return res.end()
    }
    if (req.url === '/download') {
      res.setHeader('content-type', 'application/octet-stream')
      res.setHeader('content-disposition', 'attachment; filename="../../.ssh/authorized_keys"')
      return res.end('ssh-ed25519 AAAA attacker')
    }
    res.statusCode = 404
    res.end()
  })
  .listen(port, '127.0.0.1', () => console.log('hostile-server: listening'))

// The other origin. It records every request, so the test can tell whether anything reached it.
http
  .createServer((req, res) => {
    console.log(`evil-origin: hit ${req.url}`)
    res.setHeader('content-type', 'text/html')
    res.end('<h1>other origin</h1>')
  })
  .listen(evilPort, '127.0.0.1')
