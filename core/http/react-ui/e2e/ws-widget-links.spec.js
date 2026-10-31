import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// A list widget whose items link: an http(s) address asks first, with the
// confirmation bar of page links, then opens a new tab without an opener; a
// path of the app navigates inside LocalAI directly; and a file with a bad
// link is refused with an error that names the item. PAGES_SHOTS names a
// folder for screenshots.

test.describe.configure({ mode: 'serial', timeout: 60_000 })
holdHomeFiles(test)

const shots = process.env.PAGES_SHOTS || ''
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const widgets = [{ id: 'news', type: 'list', title: 'Morning news', source: 'data/w-news.json' }]
const news = {
  updatedAt: new Date().toISOString(), source: { kind: 'trigger', note: 'Three sites, last 24 hours' },
  items: [
    { label: 'Local models get faster on small boards', meta: 'example.com', url: 'https://news.example.com/small-boards' },
    { label: 'The release notes of this week, with every change', meta: 'pages', url: '/app/pages' },
    { label: 'A row without a link', meta: 'note' },
  ],
}

test.beforeAll(async ({ request }) => {
  await upload(request, '/LocalAI/home.json', { widgets })
  await upload(request, '/data/w-news.json', news)
})
test.afterAll(async ({ request }) => {
  for (const p of ['/LocalAI/home.json', '/data/w-news.json']) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
})
test.afterAll(() => unlockHomeFiles())

test('widget items ask before they open another site, and link inside the app directly', async ({ page, context }) => {
  await page.route('**/api/chats/triggers*', (r) => r.fulfill({ json: { triggers: [] } }))
  await context.route('https://news.example.com/**', (r) => r.fulfill({ contentType: 'text/html', body: '<title>news</title><p>article</p>' }))
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'light') } catch { /* private mode */ } })
  await page.goto('/app')
  const w = page.getByTestId('home-widget-news')
  const links = w.getByTestId('widget-item-link')
  await expect(links).toHaveCount(2)
  const ext = links.first()
  await expect(ext).toHaveAttribute('data-href', 'https://news.example.com/small-boards')
  await expect(ext).not.toHaveAttribute('href', /.*/)
  await expect(ext).toHaveAttribute('title', 'news.example.com: opens in a new tab')
  await expect(w.locator('li').nth(2).getByTestId('widget-item-link')).toHaveCount(0)
  // Keyboard: the link takes the focus and shows its ring.
  await ext.focus()
  await expect(ext).toBeFocused()
  expect(await ext.evaluate((el) => getComputedStyle(el).outlineStyle)).toBe('solid')
  // A click asks first; Cancel opens nothing.
  await ext.click()
  const ask = w.getByTestId('page-link-ask')
  await expect(ask).toBeVisible()
  await expect(ask).toContainText('This widget wants to open a link')
  await expect(ask.getByTestId('page-link-host')).toHaveText('news.example.com')
  if (shots) await page.screenshot({ path: `${shots}/widget-links-light.png` })
  await ask.getByTestId('page-link-cancel').click()
  await expect(ask).toHaveCount(0)
  expect(context.pages()).toHaveLength(1)
  await ext.click()
  const [tab] = await Promise.all([context.waitForEvent('page'), w.getByTestId('page-link-open').click()])
  await tab.waitForLoadState()
  expect(tab.url()).toBe('https://news.example.com/small-boards')
  expect(await tab.evaluate(() => window.opener)).toBeNull()
  await tab.close()
  await links.nth(1).click()
  await expect(page).toHaveURL(/\/app\/pages$/)
})

test('the widget in the dark theme', async ({ page }) => {
  await page.route('**/api/chats/triggers*', (r) => r.fulfill({ json: { triggers: [] } }))
  await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'dark') } catch { /* private mode */ } })
  await page.goto('/app')
  await expect(page.getByTestId('home-widget-news').getByTestId('widget-item-link')).toHaveCount(2)
  await page.getByTestId('home-widget-news').getByTestId('widget-item-link').first().hover()
  if (shots) await page.screenshot({ path: `${shots}/widget-links-dark.png` })
})

test('a bad link is refused with an error that names the item', async ({ request }) => {
  const bad = { ...news, items: [...news.items, { label: 'x', url: 'javascript:alert(1)' }] }
  const res = await request.put(`/api/chats/wellknown/widget-data?path=${encodeURIComponent('data/w-news.json')}`, { data: { values: bad, replace: true } })
  expect(res.ok()).toBeFalsy()
  expect(JSON.stringify(await res.json())).toContain('items[3].url: a link is an http or https address')
  const schema = await (await request.get('/api/chats/wellknown/widget-data/schema')).json()
  expect(JSON.stringify(schema)).toContain('"url"')
})
