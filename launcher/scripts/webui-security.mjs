// Drives the built launcher through the devtools protocol for
// scripts/webui-security.sh: runs each attempt of the hostile page in the
// WebUI view, then a few shell calls, and prints one JSON line per result.
//   node webui-security.mjs <devtools port> <server origin>
const [port, origin] = process.argv.slice(2)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function targets() {
  return (await fetch(`http://127.0.0.1:${port}/json`)).json()
}

async function connect(match) {
  for (let i = 0; i < 100; i++) {
    const t = (await targets()).find((x) => x.type === 'page' && match(x.url))
    if (t) {
      const ws = new WebSocket(t.webSocketDebuggerUrl)
      await new Promise((r, j) => ((ws.onopen = r), (ws.onerror = j)))
      let id = 0
      const pending = new Map()
      ws.onmessage = (m) => {
        const d = JSON.parse(m.data)
        if (d.id && pending.has(d.id)) (pending.get(d.id)(d), pending.delete(d.id))
      }
      const call = (method, params = {}) =>
        new Promise((r) => {
          const n = ++id
          pending.set(n, r)
          ws.send(JSON.stringify({ id: n, method, params }))
        })
      const evaluate = async (expression) => {
        const r = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
        return r.result?.exceptionDetails ? `exception: ${r.result.exceptionDetails.exception?.description ?? r.result.exceptionDetails.text}` : r.result?.result?.value
      }
      return { call, evaluate, close: () => ws.close() }
    }
    await sleep(100)
  }
  throw new Error('no matching devtools target')
}

const out = (name, value) => console.log(JSON.stringify({ name, value }))

const webui = await connect((u) => u.startsWith(`${origin}/app`))
const shell = await connect((u) => u.startsWith('file:'))
await webui.evaluate("window.__marker = 42; localStorage.setItem('who', 'local')")

for (const name of [
  'bridge', 'windowOpen', 'windowOpenJs', 'targetBlank', 'fileNav', 'dataNav', 'customProto', 'jsLink', 'externalNav',
  'redirect', 'iframe', 'dataIframe', 'micNoGesture', 'clipboardRead', 'notifications', 'geolocation', 'displayCapture',
  'midi', 'usb', 'fetchFile', 'download', 'blobDownload',
]) {
  out(name, await webui.evaluate(`window.attempts.${name}()`))
}

// A real click on the page's buttons: a user action, so the permission rule may ask.
async function clickOn(id) {
  const box = await webui.evaluate(`(() => { const r = document.getElementById('${id}').getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2] })()`)
  for (const type of ['mousePressed', 'mouseReleased']) {
    await webui.call('Input.dispatchMouseEvent', { type, x: box[0], y: box[1], button: 'left', clickCount: 1 })
  }
}
await clickOn('clip')
await sleep(500)
await clickOn('mic')
await sleep(1500)
out('gesture results', await webui.evaluate('window.results'))

// The shell side: the WebUI state, screen switches without reload, remote and back, the dot.
out('shell bridge', await shell.evaluate('typeof window.launcher'))
out('webui state', await shell.evaluate('window.launcher.getWebui().then(s => [s.layout, s.target.kind, s.loaded, s.barVisible, s.dot])'))
out('logs screen', await shell.evaluate("window.launcher.setShellScreen('logs').then(s => s.layout)"))
out('back to webui', await shell.evaluate('window.launcher.setShellScreen(null).then(s => s.layout)'))
out('page kept (no reload)', await webui.evaluate('window.__marker'))
out('webui viewport with the bar', await webui.evaluate('[innerWidth, innerHeight]'))
out('shell viewport', await shell.evaluate('[innerWidth, innerHeight]'))
// The sandbox notice forces the bar while it is unanswered (it is shown under --no-sandbox).
out('dismiss sandbox notice', await shell.evaluate('window.launcher.dismissSandboxNotice().then(s => s.notice.kind)'))
out('hide bar', await shell.evaluate('window.launcher.setStatusBarVisible(false)'))
await sleep(800)
out('dot view', (await targets()).filter((t) => t.url.startsWith('data:text/html')).length)
out('webui viewport, bar hidden', await webui.evaluate('[innerWidth, innerHeight]'))
out('dot state', await shell.evaluate('window.launcher.getWebui().then(s => [s.barVisible, s.dot])'))
// A click on the dot shows the bar again (the dot page has no script; this is a real mouse click).
const dot = await connect((u) => u.startsWith('data:text/html'))
for (const type of ['mousePressed', 'mouseReleased']) await dot.call('Input.dispatchMouseEvent', { type, x: 14, y: 14, button: 'left', clickCount: 1 })
dot.close()
await sleep(800)
out('after dot click', await shell.evaluate('window.launcher.getWebui().then(s => [s.barVisible, s.dot])'))
out('show bar', await shell.evaluate('window.launcher.setStatusBarVisible(true)'))
// The same server under another origin name is a "remote" server: its own view and session.
const remoteOrigin = origin.replace('127.0.0.1', 'localhost')
out('connect remote', await shell.evaluate(`window.launcher.connectRemote('${remoteOrigin}/app').then(r => r.ok ? [r.state.target.kind, r.state.target.host, r.state.barVisible] : r.reason)`))
const remote = await connect((u) => u.startsWith(`${remoteOrigin}/app`))
await sleep(500)
out('remote: local storage of the local session', await remote.evaluate("localStorage.getItem('who')"))
out('remote: navigation back to the local origin', await remote.evaluate(`(async () => { location.href = '${origin}/app'; await new Promise(r => setTimeout(r, 500)); return location.href })()`))
remote.close()
out('back to local', await shell.evaluate('window.launcher.backToLocal().then(s => [s.target.kind, s.layout])'))
out('connect public http', await shell.evaluate("window.launcher.connectRemote('http://remote.example.test:8080').then(r => r.ok ? 'ok' : r.reason)"))
out('connect file', await shell.evaluate("window.launcher.connectRemote('file:///etc/passwd').then(r => r.ok ? 'ok' : r.reason)"))
webui.close()
shell.close()
