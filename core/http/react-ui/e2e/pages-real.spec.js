import { test, expect } from './coverage-fixtures.js'

// Pages of the workspace against the real test server: the files are written
// with the files API, the viewer loads the page into its sandbox and the bridge
// answers through the real pages routes, so what the frame can and cannot reach
// is what a user's page can and cannot reach.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

const stamp = Date.now().toString(36)
const NAME = `probe-${stamp}`
const SECRET = `secret-${stamp}.txt`

const put = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: body, headers: { 'Content-Type': 'application/octet-stream' },
})

// A page that probes its sandbox from the inside and drives every bridge call.
const PROBE = `<!doctype html><html><body>
<p id="probe">running</p><p id="read"></p><p id="outside"></p><p id="write"></p><p id="list"></p><p id="changed"></p><p id="send"></p><p id="frozen"></p>
<button id="w">write</button><button id="s">send</button><button id="nav">nav</button>
<script>
const out = (id, v) => { document.getElementById(id).textContent = v }
const probe = []
try { void parent.document.title; probe.push('parent-readable') } catch (e) { probe.push('parent-blocked') }
try { void top.location.href; probe.push('top-readable') } catch (e) { probe.push('top-blocked') }
try { void localStorage.length; probe.push('storage-readable') } catch (e) { probe.push('storage-blocked') }
try { void document.cookie; probe.push('cookie-readable') } catch (e) { probe.push('cookie-blocked') }
probe.push(typeof window.localai === 'object' ? 'bridge' : 'no-bridge')
Promise.all([
  fetch('/api/chats/files').then(() => 'api-open', () => 'api-blocked'),
  fetch('https://example.com/x', { mode: 'no-cors' }).then(() => 'net-open', () => 'net-blocked'),
  new Promise((r) => { const i = new Image(); i.onload = () => r('img-open'); i.onerror = () => r('img-blocked'); i.src = 'https://example.com/x.png' }),
]).then((r) => out('probe', probe.concat(r).join(',')))
localai.files.read('data.json').then((f) => out('read', f.content), (e) => out('read', 'ERR ' + e.message))
localai.files.read('../../${SECRET}').then((f) => out('outside', 'LEAK ' + f.content), (e) => out('outside', 'refused: ' + e.message))
localai.files.list().then((l) => out('list', l.map((f) => f.path).join(',')))
localai.on('files.changed', (ev) => out('changed', ev.paths.join(',')))
try { window.localai.files = null; localai.files.read = null } catch (e) { /* frozen */ }
out('frozen', typeof localai.files.read)
document.getElementById('w').onclick = () => localai.files.write('data.json', '{"n":2}').then((f) => out('write', 'v' + f.version), (e) => out('write', 'ERR ' + e.message))
document.getElementById('s').onclick = () => localai.chat.send('hello from the page').then((r) => out('send', JSON.stringify(r)))
document.getElementById('nav').onclick = () => { location.href = 'about:blank' }
</script></body></html>`

const frame = (page) => page.frameLocator('[data-testid="page-frame"]')

test.beforeAll(async ({ request }) => {
  expect((await put(request, `/pages/${NAME}/index.html`, PROBE)).ok()).toBeTruthy()
  expect((await put(request, `/pages/${NAME}/data.json`, '{"n":1}')).ok()).toBeTruthy()
  expect((await put(request, `/pages/${NAME}/page.json`, '{"title":"<img src=x onerror=\\"window.__pwned=1\\">Probe"}')).ok()).toBeTruthy()
  expect((await put(request, `/${SECRET}`, 'top secret')).ok()).toBeTruthy()
})

test('a page runs in a sandbox: no parent DOM, no storage, no network, nothing outside its folder', async ({ page }) => {
  await page.goto(`/app/pages/${NAME}`)
  const el = page.getByTestId('page-frame')
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts')
  const f = frame(page)
  await expect(f.locator('#probe')).toHaveText(/net-/, { timeout: 15_000 })
  const probe = (await f.locator('#probe').textContent()).split(',')
  expect(probe).toEqual(['parent-blocked', 'top-blocked', 'storage-blocked', 'cookie-blocked', 'bridge', 'api-blocked', 'net-blocked', 'img-blocked'])
  await expect(f.locator('#outside')).toHaveText(/^refused: /)
  await expect(f.locator('#outside')).not.toContainText('top secret')
  await expect(f.locator('#frozen')).toHaveText('function')
  // The title from page.json is text in the strip, never markup.
  await expect(page.locator('.pg-strip__title')).toHaveText('<img src=x onerror="window.__pwned=1">Probe')
  expect(await page.evaluate(() => window.__pwned)).toBeUndefined()
  await expect(page.getByTestId('page-runs-as-you')).toBeVisible()
})

test('the bridge reads, lists and writes the own folder and tells the page what changed', async ({ page, request }) => {
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await expect(f.locator('#read')).toHaveText('{"n":1}')
  await expect(f.locator('#list')).toHaveText('data.json,index.html,page.json')
  await f.locator('#w').click()
  await expect(f.locator('#write')).toHaveText(/^v\d+$/)
  await expect(f.locator('#changed')).toHaveText('data.json')
  const res = await request.get(`/api/chats/pages/${NAME}/file?path=data.json`)
  expect((await res.json()).content).toBe('{"n":2}')
  // A page cannot change its own code through the bridge.
  const own = await request.put(`/api/chats/pages/${NAME}/file`, { data: { path: 'index.html', content: 'x' } })
  expect(own.status()).toBe(403)
})

// A page may save data files; the code of a page is changed by the person or
// the assistant, so a page cannot make its next load run new code.
const WRITER = `<!doctype html><html><body><p id="code"></p><p id="data"></p><p id="late"></p>
<script>
const out = (id, v) => { document.getElementById(id).textContent = v }
localai.files.write('app.js', 'alert(1)').then((f) => out('code', 'WROTE ' + f.path), (e) => out('code', e.code + ': ' + e.message))
localai.files.write('data.json', '{"ok":true}').then((f) => out('data', 'v' + f.version), (e) => out('data', 'ERR ' + e.message))
</script></body></html>`

test('a page cannot write its own code, only data files', async ({ page, request }) => {
  const writer = `writer-${stamp}`
  expect((await put(request, `/pages/${writer}/index.html`, WRITER)).ok()).toBeTruthy()
  await page.goto(`/app/pages/${writer}`)
  const f = frame(page)
  await expect(f.locator('#code')).toHaveText(/^forbidden: a page can write data files only/, { timeout: 15_000 })
  await expect(f.locator('#data')).toHaveText(/^v\d+$/)
  expect((await request.get(`/api/chats/pages/${writer}/file?path=app.js`)).status()).toBe(404)
  const res = await request.get(`/api/chats/pages/${writer}/file?path=data.json`)
  expect((await res.json()).content).toBe('{"ok":true}')
  // The server refuses it too, for a caller that skips the host's check.
  const direct = await request.put(`/api/chats/pages/${writer}/file`, { data: { path: 'style.css', content: 'p{}' } })
  expect(direct.status()).toBe(403)
  // The person's own Files route still writes code in the page folder.
  expect((await put(request, `/pages/${writer}/app.js`, 'console.log(1)')).ok()).toBeTruthy()
})

test('chat.send asks first, then puts the message in the page chat', async ({ page, request }) => {
  await page.addInitScript(() => { try { localStorage.setItem('localai_last_model:FLAG_CHAT', 'mock-model') } catch { /* private mode */ } })
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await f.locator('#s').click()
  const ask = page.getByTestId('page-send-ask')
  await expect(ask).toContainText('hello from the page')
  await expect(f.locator('#send')).toHaveText('')
  await ask.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByTestId('page-send-done')).toBeVisible({ timeout: 15_000 })
  await expect(f.locator('#send')).toHaveText(/"sent":true/)
  const chatId = JSON.parse(await f.locator('#send').textContent()).chat_id
  await expect.poll(async () => {
    const r = await request.get(`/api/chats/${chatId}/messages`)
    const pend = await request.get(`/api/chats/${chatId}`)
    return JSON.stringify(await r.json()) + JSON.stringify(await pend.json())
  }, { timeout: 15_000 }).toContain('hello from the page')
  const chat = await (await request.get(`/api/chats/${chatId}`)).json()
  expect(chat.origin).toBe('page')
})

test('a page that navigates its frame away loses its bridge until Reload', async ({ page }) => {
  await page.goto(`/app/pages/${NAME}`)
  const f = frame(page)
  await expect(f.locator('#read')).toHaveText(/n/)
  await f.locator('#nav').click()
  await expect(page.getByTestId('page-navigated')).toBeVisible()
  await page.getByTestId('page-reload').click()
  await expect(frame(page).locator('#read')).toHaveText(/n/)
})

test('the starter task board is copied, opened and saves its tasks', async ({ page, request }) => {
  const before = (await (await request.get('/api/chats/pages')).json()).pages.map((p) => p.name)
  const res = await request.post('/api/chats/page-starters/task-board/copy', { data: {} })
  expect(res.status()).toBe(201)
  const { name } = await res.json()
  expect(before).not.toContain(name)
  await page.goto(`/app/pages/${name}`)
  const f = frame(page)
  await expect(f.locator('.col')).toHaveCount(3)
  await f.getByLabel('New task').fill(`Buy milk ${stamp}`)
  await f.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(f.locator('.status')).toHaveText('Saved')
  const saved = await (await request.get(`/api/chats/pages/${name}/file?path=tasks.json`)).json()
  expect(saved.content).toContain(`Buy milk ${stamp}`)
})

test('the task board keeps a task added while its first read is still on the way', async ({ page, request }) => {
  const { name } = await (await request.post('/api/chats/page-starters/task-board/copy', { data: {} })).json()
  // The first read of tasks.json answers late, with the file as it was before
  // the task was added.
  let first = true
  let landed
  const late = new Promise((resolve) => { landed = resolve })
  await page.route(new RegExp(`/api/chats/pages/${name}/file\\?path=tasks\\.json`), async (route) => {
    if (route.request().method() !== 'GET' || !first) return route.continue()
    first = false
    const before = await route.fetch()
    await new Promise((resolve) => { setTimeout(resolve, 1500) })
    await route.fulfill({ response: before })
    landed()
  })
  await page.goto(`/app/pages/${name}`)
  const f = frame(page)
  await expect(f.locator('.col')).toHaveCount(3)
  await f.getByLabel('New task').fill(`Call the bank ${stamp}`)
  await f.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(f.locator('.status')).toHaveText('Saved')
  await late
  await page.waitForTimeout(500) // the page handles the late answer
  await expect(f.getByText(`Call the bank ${stamp}`)).toBeVisible()
  await expect(f.locator('.status')).toHaveText('Saved')
  await page.unrouteAll({ behavior: 'ignoreErrors' })
})
