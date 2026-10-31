import { deflateSync } from 'node:zlib'
import { test, expect } from './coverage-fixtures.js'

// A page split into several files, against the real test server: the viewer
// puts its script, style sheet, image and modules into the frame, and fetch()
// and XMLHttpRequest of the page read the files next to it. The network and
// the files outside the folder stay out of reach. PAGES_SHOTS names a folder
// for screenshots of the page in both themes.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

const stamp = Date.now().toString(36)
const NAME = `news-${stamp}`
const SECRET = `secret-${stamp}.json`
const shots = process.env.PAGES_SHOTS || ''

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: body, headers: { 'Content-Type': 'application/octet-stream' },
})

// A small PNG: a ring in the accent colour, so the screenshot shows it loaded.
function ringPNG(size = 40) {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    return c >>> 0
  })
  const crc = (buf) => {
    let c = 0xffffffff
    for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8)
    return (c ^ 0xffffffff) >>> 0
  }
  const chunk = (type, data) => {
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type), data])
    const sum = Buffer.alloc(4)
    sum.writeUInt32BE(crc(body))
    return Buffer.concat([len, body, sum])
  }
  const rows = []
  for (let y = 0; y < size; y++) {
    const row = [0]
    for (let x = 0; x < size; x++) {
      const d = Math.hypot(x - size / 2 + 0.5, y - size / 2 + 0.5)
      const on = d < size / 2 - 1 && d > size / 2 - 9
      row.push(...(on ? [31, 111, 235, 255] : [0, 0, 0, 0]))
    }
    rows.push(Buffer.from(row))
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(size, 0)
  ihdr.writeUInt32BE(size, 4)
  ihdr[8] = 8
  ihdr[9] = 6
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr), chunk('IDAT', deflateSync(Buffer.concat(rows))), chunk('IEND', Buffer.alloc(0)),
  ])
}

const INDEX = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>News</title>
<link rel="stylesheet" href="style.css">
<script src="lib/late.js" defer></script>
</head>
<body>
<header class="top"><img id="logo" src="img/logo.png" alt="logo" width="40" height="40"><h1>Morning news</h1><span id="mode" class="pill"></span></header>
<ul id="items" class="items"></ul>
<dl class="checks">
  <dt>script src</dt><dd id="classic"></dd>
  <dt>deferred script</dt><dd id="late"></dd>
  <dt>module import</dt><dd id="module"></dd>
  <dt>dynamic import</dt><dd id="dynamic"></dd>
  <dt>missing file</dt><dd id="missing"></dd>
  <dt>remote fetch</dt><dd id="remote"></dd>
  <dt>outside the folder</dt><dd id="outside"></dd>
  <dt>XMLHttpRequest</dt><dd id="xhr"></dd>
</dl>
<script src="app.js"></script>
<script type="module" src="main.js"></script>
</body>
</html>`

const STYLE = `@import "theme.css";
* { box-sizing: border-box; }
body { margin: 0; padding: 20px; background: var(--bg); color: var(--text); font: 14px/1.5 system-ui, sans-serif; }
.top { display: flex; align-items: center; gap: 12px; padding-bottom: 12px; border-bottom: 1px solid var(--line); background: url(img/stripe.svg) right center / 120px 40px no-repeat; }
.top h1 { margin: 0; font-size: 20px; flex: 1; }
.pill { padding: 2px 10px; border-radius: 999px; border: 1px solid var(--line); color: var(--muted); font-size: 12px; }
.items { list-style: none; padding: 0; margin: 16px 0; display: grid; gap: 8px; }
.items li { padding: 10px 12px; border: 1px solid var(--line); border-radius: 8px; background: var(--card); }
.items li b { display: block; }
.items li span { color: var(--muted); font-size: 12px; }
.checks { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 0; color: var(--muted); font-size: 12px; }
.checks dd { margin: 0; color: var(--text); font-family: ui-monospace, monospace; overflow-wrap: anywhere; }
#styled { color: var(--accent); }
`

const THEME = `:root { --bg: #f6f7f9; --card: #fff; --line: #d9dde3; --text: #121417; --muted: #5d6673; --accent: #1f6feb; }
:root[data-theme="dark"] { --bg: #0b0d10; --card: #171c22; --line: #29313b; --text: #e6e9ee; --muted: #8b95a3; --accent: #58a6ff; }
`

const STRIPE = '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="40"><path d="M0 40 L40 0 M30 40 L70 0 M60 40 L100 0 M90 40 L130 0" stroke="#1f6feb" stroke-opacity=".25" stroke-width="6"/></svg>'

const APP = `var out = function (id, v) { document.getElementById(id).textContent = v }
out('classic', 'ran before the module: ' + (typeof window.fromModule === 'undefined'))
out('mode', document.documentElement.getAttribute('data-theme'))
fetch('news.json?fresh=1').then(function (r) { return r.json() }).then(function (data) {
  var list = document.getElementById('items')
  data.items.forEach(function (it) {
    var li = document.createElement('li')
    var b = document.createElement('b'); b.textContent = it.title
    var s = document.createElement('span'); s.textContent = it.source
    li.appendChild(b); li.appendChild(s); list.appendChild(li)
  })
})
fetch('missing.json').then(function (r) { out('missing', r.status + ' ' + r.ok) })
fetch('https://example.com/news.json').then(function () { out('remote', 'LEAK') }, function (e) { out('remote', e.name + ': ' + e.message) })
fetch('../../${SECRET}').then(function (r) { return r.text() }).then(function (t) { out('outside', 'LEAK ' + t) }, function (e) { out('outside', 'refused: ' + e.message) })
var x = new XMLHttpRequest()
x.open('GET', 'news.json')
x.responseType = 'json'
x.onload = function () { out('xhr', x.status + ' ' + x.response.items.length + ' items') }
x.send()
`

const LATE = "document.getElementById('late').textContent = 'after parsing: ' + !!document.getElementById('xhr')\n"

const MAIN = `import { label } from './lib/format.js'
window.fromModule = true
document.getElementById('module').textContent = label('ok')
const mod = await import('./lib/dynamic.js')
document.getElementById('dynamic').textContent = mod.default
`
const FORMAT = "import { suffix } from './util.js'\nexport const label = (s) => s + suffix()\n"
const UTIL = "import { label } from './format.js'\nexport const suffix = () => ' (cycle ' + typeof label + ')'\n"
const DYNAMIC = "export default 'loaded on demand'\n"
const NEWS = JSON.stringify({ items: [
  { title: 'Local models get faster on small boards', source: 'news.json' },
  { title: 'A page can now read the files next to it', source: 'news.json' },
  { title: 'Split code into app.js and style.css', source: 'news.json' },
] })

const frame = (page) => page.frameLocator('[data-testid="page-frame"]')

test.beforeAll(async ({ request }) => {
  const files = {
    'index.html': INDEX, 'style.css': STYLE, 'theme.css': THEME, 'img/stripe.svg': STRIPE, 'img/logo.png': ringPNG(),
    'app.js': APP, 'lib/late.js': LATE, 'main.js': MAIN, 'lib/format.js': FORMAT, 'lib/util.js': UTIL,
    'lib/dynamic.js': DYNAMIC, 'news.json': NEWS, 'page.json': '{"title":"Morning news","icon":"news"}',
  }
  for (const [p, body] of Object.entries(files)) {
    expect((await put(request, `/pages/${NAME}/${p}`, body)).ok(), p).toBeTruthy()
  }
  expect((await put(request, `/${SECRET}`, '{"secret":true}')).ok()).toBeTruthy()
})

test('a page split into files runs: script src, css link, image, modules, fetch and XHR', async ({ page }) => {
  const errors = []
  page.on('console', (m) => { if (m.type() === 'error' && /LocalAI page/.test(m.text())) errors.push(m.text()) })
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'light') } catch { /* private mode */ } })
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await expect(f.locator('#items li')).toHaveCount(3, { timeout: 15_000 })
  await expect(f.locator('#items li').first()).toContainText('Local models get faster')
  await expect(f.locator('#classic')).toHaveText('ran before the module: true')
  await expect(f.locator('#late')).toHaveText('after parsing: true')
  await expect(f.locator('#module')).toHaveText('ok (cycle function)')
  await expect(f.locator('#dynamic')).toHaveText('loaded on demand')
  await expect(f.locator('#missing')).toHaveText('404 false')
  await expect(f.locator('#remote')).toHaveText(/^TypeError: network is off in pages; put the data in a file next to the page/)
  await expect(f.locator('#outside')).toHaveText(/^refused: .*outside the page's folder/)
  await expect(f.locator('#outside')).not.toContainText('LEAK')
  await expect(f.locator('#xhr')).toHaveText('200 3 items')
  // The style sheet and its @import went in: the theme colours apply.
  const bg = await f.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor)
  expect(bg).toBe('rgb(246, 247, 249)')
  const logo = await f.locator('#logo').evaluate((img) => ({ w: img.naturalWidth, src: img.src.slice(0, 22) }))
  expect(logo).toEqual({ w: 40, src: 'data:image/png;base64,' })
  const stripe = await f.locator('.top').evaluate((el) => getComputedStyle(el).backgroundImage.slice(0, 30))
  expect(stripe).toBe('url("data:image/svg+xml;base64')
  await expect(f.locator('localai-notice')).toHaveCount(0)
  expect(errors).toEqual([])
  if (shots) {
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/pages-files-light.png` })
  }
})

test('the page in the dark theme', async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'dark') } catch { /* private mode */ } })
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await expect(f.locator('#items li')).toHaveCount(3, { timeout: 15_000 })
  await expect(f.locator('#mode')).toHaveText('dark')
  expect(await f.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe('rgb(11, 13, 16)')
  if (shots) {
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/pages-files-dark.png` })
  }
})

test('a changed style sheet reloads the page with it; a data file does not reload', async ({ page, request }) => {
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'light') } catch { /* private mode */ } })
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await expect(f.locator('#items li')).toHaveCount(3, { timeout: 15_000 })
  await f.locator('body').evaluate(() => { window.__mark = 1 })
  expect((await put(request, `/pages/${NAME}/news.json`, JSON.stringify({ items: [{ title: 'one', source: 'x' }] }))).ok()).toBeTruthy()
  await page.waitForTimeout(1500)
  expect(await f.locator('body').evaluate(() => window.__mark)).toBe(1)
  expect((await put(request, `/pages/${NAME}/theme.css`, THEME.replace('#f6f7f9', '#fdf0e0'))).ok()).toBeTruthy()
  await expect.poll(() => f.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor), { timeout: 15_000 }).toBe('rgb(253, 240, 224)')
  expect(await f.locator('body').evaluate(() => window.__mark)).toBeUndefined()
  await expect(f.locator('#items li')).toHaveCount(1)
  // Put the files back for the other tests.
  expect((await put(request, `/pages/${NAME}/theme.css`, THEME)).ok()).toBeTruthy()
  expect((await put(request, `/pages/${NAME}/news.json`, NEWS)).ok()).toBeTruthy()
})

test('a script that is missing or outside the folder shows a banner in the page and stays out', async ({ page, request }) => {
  const BROKEN = `broken-${stamp}`
  expect((await put(request, `/pages/${BROKEN}/index.html`, `<!doctype html><p id="p">still here</p>
<script src="gone.js"></script><script src="../${NAME}/app.js"></script><script src="%2e%2e/${NAME}/app.js"></script>
<link rel="stylesheet" href="/pages/${NAME}/style.css"><script src="https://example.com/x.js"></script>`)).ok()).toBeTruthy()
  const reads = []
  page.on('request', (r) => { if (r.url().includes('/file?')) reads.push(decodeURIComponent(r.url())) })
  await page.goto(`/app/pages/${BROKEN}`)
  const f = frame(page)
  await expect(f.locator('#p')).toHaveText('still here')
  const notice = f.locator('localai-notice')
  await expect(notice).toHaveCount(1)
  const text = await notice.evaluate((el) => el.shadowRoot === null ? 'closed' : 'open')
  expect(text).toBe('closed')
  expect(reads.every((u) => !u.includes(`pages/${BROKEN}/file?path=../`) && !u.includes(`${NAME}/app.js`)), reads.join('\n')).toBeTruthy()
  expect(reads.some((u) => u.includes('path=gone.js'))).toBeTruthy()
  if (shots) {
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${shots}/pages-files-banner.png` })
  }
})
