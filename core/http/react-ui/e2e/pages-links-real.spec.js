import { test, expect } from './coverage-fixtures.js'

// Links in a page against the real test server: the sandbox opens nothing by
// itself, so a click asks LocalAI, which shows the site and opens it in a new
// tab only after Open. PAGES_SHOTS names a folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

const stamp = Date.now().toString(36)
const NAME = `links-${stamp}`
const shots = process.env.PAGES_SHOTS || ''
const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: body, headers: { 'Content-Type': 'application/octet-stream' },
})

const PAGE = `<!doctype html><html><body style="font:14px system-ui;padding:16px">
<p><a id="ext" href="https://news.example.com/article?id=7" target="_blank">Read the article</a></p>
<p><a id="js" href="javascript:alert(1)">javascript link</a></p>
<p><a id="file" href="other.html">another file</a></p>
<p><a id="look" href="https://аpple.com/login">look-alike</a></p>
<p><button id="win">window.open</button></p>
<p><a id="anchor" href="#end">to the end</a></p>
<div style="height:1600px"></div><p id="end">end</p>
<script>document.getElementById('win').onclick = () => { window.__ret = window.open('https://example.org/x') }</script>
</body></html>`

const frame = (page) => page.frameLocator('[data-testid="page-frame"]')

test.beforeAll(async ({ request }) => {
  expect((await put(request, `/pages/${NAME}/index.html`, PAGE)).ok()).toBeTruthy()
})

test('a link asks first; Open opens a new tab without an opener, Cancel does nothing', async ({ page, context }) => {
  await context.route('https://news.example.com/**', (r) => r.fulfill({ contentType: 'text/html', body: '<title>article</title><p>ok</p>' }))
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'light') } catch { /* private mode */ } })
  const logs = []
  page.on('console', (m) => logs.push(m.text()))
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await f.locator('#ext').click()
  const ask = page.getByTestId('page-link-ask')
  await expect(ask).toBeVisible()
  await expect(page.getByTestId('page-link-host')).toHaveText('news.example.com')
  await expect(ask.locator('.pg-link__site')).toHaveText('example.com')
  await expect(ask).toContainText('https://news.example.com/article?id=7')
  await expect(page.getByTestId('page-link-lookalike')).toHaveCount(0)
  if (shots) await page.screenshot({ path: `${shots}/page-link-ask-light.png` })

  let opened = 0
  context.on('page', () => { opened++ })
  await page.getByTestId('page-link-cancel').click()
  await expect(ask).toHaveCount(0)
  await page.waitForTimeout(300)
  expect(opened).toBe(0)

  await f.locator('#ext').click()
  const [tab] = await Promise.all([context.waitForEvent('page'), page.getByTestId('page-link-open').click()])
  await tab.waitForLoadState()
  expect(tab.url()).toBe('https://news.example.com/article?id=7')
  expect(await tab.evaluate(() => window.opener)).toBeNull()
  await tab.close()
  await expect(ask).toHaveCount(0)

  // Refused links never ask and say why in the console.
  await f.locator('#js').click()
  await f.locator('#file').click()
  await expect.poll(() => logs.join('\n')).toMatch(/the link was not opened: only http and https links open from a page/)
  await expect.poll(() => logs.join('\n')).toMatch(/other\.html opens nothing/)
  await expect(ask).toHaveCount(0)

  // An anchor scrolls the page and asks nothing.
  await f.locator('#anchor').click()
  await expect(ask).toHaveCount(0)
  expect(await f.locator('body').evaluate(() => window.scrollY)).toBeGreaterThan(500)
})

test('window.open asks too, and a look-alike host says how it reads', async ({ page }) => {
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await f.locator('#win').click()
  await expect(page.getByTestId('page-link-host')).toHaveText('example.org')
  expect(await f.locator('body').evaluate(() => window.__ret)).toBeNull()
  await f.locator('#look').click()
  await expect(page.getByTestId('page-link-host')).toHaveText('xn--pple-43d.com')
  await expect(page.getByTestId('page-link-lookalike')).toContainText('аpple.com')
  await expect(page.getByTestId('page-link-ask')).toHaveCount(1)
  if (shots) await page.screenshot({ path: `${shots}/page-link-lookalike-dark.png` })
  await page.getByTestId('page-link-cancel').click()
})
