import { test, expect } from './coverage-fixtures.js'

// The errors of a page's script against the real test server: a page that
// throws is loaded into its sandbox, the bridge catches the error, and the
// host reports it to the pages route that the assistant's page_errors tool
// reads from. The requests are watched here; nothing is shown in the page and
// no chat is started.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

const stamp = Date.now().toString(36)
const SYNTAX = `err-syntax-${stamp}`
const LOOP = `err-loop-${stamp}`
const ORDER = `err-order-${stamp}`

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: body, headers: { 'Content-Type': 'application/octet-stream' },
})

// The script starts on line 4; the fault is on line 5.
const BROKEN_SYNTAX = `<!doctype html>
<html><body>
<p id="alive">alive</p>
<script>
const total = 1 + ;
</script>
</body></html>`

// A throw on every animation frame, and an unhandled rejection.
const THROWS_IN_LOOP = `<!doctype html>
<html><body>
<p id="alive">alive</p>
<script>
let frames = 0
function tick() {
  frames++
  document.getElementById('alive').textContent = 'frames ' + frames
  requestAnimationFrame(tick)
  null.boom()
}
requestAnimationFrame(tick)
Promise.reject(new Error('rejected on load'))
</script>
</body></html>`

// An init-order bug: a const read before its line runs.
const WRONG_ORDER = `<!doctype html>
<html><body>
<script>
render()
const items = []
function render() { items.push(1) }
</script>
</body></html>`

const watch = (page, name) => {
  const reports = []
  page.on('request', (req) => {
    if (req.method() === 'POST' && req.url().endsWith(`/api/chats/pages/${name}/errors`)) reports.push(req.postDataJSON())
  })
  return reports
}

test.beforeAll(async ({ request }) => {
  for (const [name, html] of [[SYNTAX, BROKEN_SYNTAX], [LOOP, THROWS_IN_LOOP], [ORDER, WRONG_ORDER]]) {
    expect((await put(request, `/pages/${name}/index.html`, html)).ok()).toBeTruthy()
  }
})

test('a syntax error of the page reaches the server with its line, and shows nothing in the page', async ({ page }) => {
  const reports = watch(page, SYNTAX)
  await page.goto(`/app/pages/${SYNTAX}`)
  await expect.poll(() => reports.length, { timeout: 15_000 }).toBeGreaterThan(0)
  const [r] = reports
  expect(r.message).toMatch(/SyntaxError/)
  expect(r.line).toBe(5)
  expect(r.count).toBe(1)
  const f = page.frameLocator('[data-testid="page-frame"]')
  await expect(f.locator('#alive')).toHaveText('alive')
  expect(await f.locator('localai-notice').count()).toBe(0)
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('an init-order error names the line where it throws', async ({ page }) => {
  const reports = watch(page, ORDER)
  await page.goto(`/app/pages/${ORDER}`)
  await expect.poll(() => reports.length, { timeout: 15_000 }).toBeGreaterThan(0)
  expect(reports[0].message).toMatch(/ReferenceError|Cannot access 'items' before initialization/)
  expect(reports[0].line).toBe(6)
})

test('a throw in an animation frame is counted and does not flood the server', async ({ page }) => {
  const reports = watch(page, LOOP)
  await page.goto(`/app/pages/${LOOP}`)
  const f = page.frameLocator('[data-testid="page-frame"]')
  await expect(f.locator('#alive')).toHaveText(/^frames \d+$/, { timeout: 15_000 })
  // Wait for the first flush of the repeats (5 s).
  await expect.poll(() => reports.some((r) => r.count > 1), { timeout: 20_000 }).toBeTruthy()
  const frames = Number((await f.locator('#alive').textContent()).replace('frames ', ''))
  const loop = reports.filter((r) => /null/.test(r.message))
  const rejection = reports.filter((r) => /rejected on load/.test(r.message))
  expect(rejection).toHaveLength(1)
  expect(frames).toBeGreaterThan(30)
  expect(loop.length).toBeLessThan(6)
  expect(loop[0].count).toBe(1)
  expect(loop.slice(1).reduce((n, r) => n + r.count, 0)).toBeGreaterThan(20)
  expect(reports.length).toBeLessThan(10)
})

test('a page without errors reports nothing, and no chat is started by errors', async ({ page, request }) => {
  const before = (await (await request.get('/api/chats')).json())
  const name = `err-clean-${stamp}`
  expect((await put(request, `/pages/${name}/index.html`, '<!doctype html><p id="ok">fine</p>')).ok()).toBeTruthy()
  const reports = watch(page, name)
  await page.goto(`/app/pages/${name}`)
  await expect(page.frameLocator('[data-testid="page-frame"]').locator('#ok')).toHaveText('fine')
  await page.waitForTimeout(1500)
  expect(reports).toHaveLength(0)
  const after = (await (await request.get('/api/chats')).json())
  expect(JSON.stringify(after)).toBe(JSON.stringify(before))
})
