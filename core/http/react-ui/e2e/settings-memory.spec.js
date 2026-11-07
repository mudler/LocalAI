import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// A screenshot of the page in both themes, for review; the files go to test-results.
async function shot(page, name, options = {}) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    // The theme fades in; let it finish so the picture shows the final colours.
    await page.waitForTimeout(500)
    await page.screenshot({ path: `test-results/${name}-${theme}.png`, ...options })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}


// Settings > Memory (a tab of Your settings): the context files the host puts
// in the prompt, with their sizes against the caps, and read, edit, delete and
// clear all. The API is stubbed by a small model of the files.

const LIMITS = { index_lines: 40, index_bytes: 2048, profile_bytes: 4096, turn_bytes: 6144 }
const file = (path, size, extra = {}) => ({ path, name: path.split('/').pop(), size, ...extra })

function fixture() {
  return {
    texts: {
      'Memory/index.md': '- home: Rome -> home.md\n- job: dev -> job.md\n',
      'Memory/home.md': 'Rome, since 2020.\n',
      'Memory/job.md': 'Developer.\n',
      'Memory/agents/bot/index.md': '- tone: short\n',
      'LocalAI/agents/bot/profile.md': 'Be brief.\n',
    },
    removed: [],
    writes: [],
  }
}

function listing(s) {
  const has = p => p in s.texts
  const f = (p, extra) => (has(p) ? [file(p, s.texts[p].length, extra)] : [])
  const idx = (p) => ({ index: true, lines: s.texts[p].split('\n').filter(l => l.trim()).length })
  const groups = []
  const user = [...(has('Memory/index.md') ? [file('Memory/index.md', s.texts['Memory/index.md'].length, idx('Memory/index.md'))] : []), ...f('Memory/home.md'), ...f('Memory/job.md')]
  if (user.length) groups.push({ source: 'notes-user', scope: 'user', max_bytes: 2048, max_lines: 40, files: user })
  const agent = has('Memory/agents/bot/index.md') ? [file('Memory/agents/bot/index.md', s.texts['Memory/agents/bot/index.md'].length, idx('Memory/agents/bot/index.md'))] : []
  if (agent.length) groups.push({ source: 'notes-agent', scope: 'agent', key: 'bot', max_bytes: 2048, max_lines: 40, files: agent })
  const prof = has('LocalAI/agents/bot/profile.md') ? [file('LocalAI/agents/bot/profile.md', s.texts['LocalAI/agents/bot/profile.md'].length, { index: true, lines: 1 })] : []
  if (prof.length) groups.push({ source: 'profile', scope: 'agent', key: 'bot', max_bytes: 4096, files: prof })
  if (s.texts['Memory/channels/chat-1/index.md'] !== undefined) {
    const p = 'Memory/channels/chat-1/index.md'
    groups.push({ source: 'notes-channel', scope: 'channel', key: 'chat-1', max_bytes: 2048, max_lines: 40, files: [file(p, s.texts[p].length, { index: true, lines: 1 })] })
  }
  return { limits: LIMITS, groups }
}

async function serve(page, s = fixture()) {
  await mockChat(page)
  await page.route('**/api/chats/context-files', route => route.fulfill({ json: listing(s) }))
  await page.route('**/api/chats/files/content*', (route) => {
    const req = route.request()
    const path = decodeURIComponent(new URL(req.url()).searchParams.get('path')).replace(/^\//, '')
    if (req.method() === 'PUT') {
      s.texts[path] = req.postData()
      s.writes.push({ path, query: new URL(req.url()).search })
      return route.fulfill({ json: { path: `/${path}`, name: path, kind: 'file', size: s.texts[path].length, version: 2, rev: 9 } })
    }
    return route.fulfill({ status: 200, contentType: 'text/plain', body: s.texts[path] ?? '' })
  })
  await page.route(/\/api\/chats\/files\?.*$/, (route) => {
    const req = route.request()
    if (req.method() !== 'DELETE') return route.fallback()
    const path = decodeURIComponent(new URL(req.url()).searchParams.get('path')).replace(/^\//, '')
    delete s.texts[path]
    s.removed.push(path)
    return route.fulfill({ json: { rev: 11 } })
  })
  return s
}

async function open(page, s) {
  const state = await serve(page, s)
  await page.goto('/app/account?tab=memory')
  await expect(page.getByTestId('memory-tab')).toBeVisible()
  return state
}

const group = (page, id) => page.locator(`[data-group="${id}"]`)

test('lists the notes of the user, of an agent and its profile with their sizes against the caps', async ({ page }) => {
  await open(page)
  await expect(page.getByRole('tab', { name: 'Memory' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByTestId('memory-limits')).toContainText('at most 40 lines and 2.0 KiB')
  await expect(page.getByTestId('memory-limits')).toContainText('6.0 KiB of notes')
  const user = group(page, 'notes-user:')
  await expect(user.getByRole('heading', { name: 'Your notes' })).toBeVisible()
  await expect(user.getByTestId('memory-use')).toContainText('45 B of 2.0 KiB · 2 of 40 lines')
  await expect(user.getByTestId('memory-file')).toHaveCount(3)
  await expect(user.getByTestId('memory-file').first()).toContainText('index.md')
  await expect(user.getByTestId('memory-file').first()).toContainText('Index')
  await page.getByRole('tab', { name: 'bot' }).click()
  await expect(group(page, 'notes-agent:bot').getByRole('heading')).toHaveText('Notes of agent bot')
  const profile = group(page, 'profile:bot')
  await expect(profile.getByRole('heading')).toHaveText('Profile of agent bot')
  await expect(profile.getByTestId('memory-use')).toContainText('10 B of 4.0 KiB')
  await expect(profile.getByTestId('memory-use')).not.toContainText('lines')
  await shot(page, 'memory-page', { fullPage: true })
})

test('a file opens to read, and the owner edits it directly; the index is held to its caps', async ({ page }) => {
  const s = await open(page)
  const idx = group(page, 'notes-user:').getByTestId('memory-file').first()
  await idx.locator('.mt-file__name').click()
  await expect(idx.getByTestId('memory-text')).toContainText('home: Rome')
  await idx.getByTestId('memory-edit').click()
  const area = idx.getByTestId('memory-edit-text')
  await expect(area).toBeFocused()
  await area.fill('- home: Milan -> home.md\n')
  await expect(idx.getByTestId('memory-edit-size')).toContainText('1 of 40 lines')
  await area.fill(Array.from({ length: 41 }, (_, i) => `- n${i}`).join('\n'))
  await expect(idx.getByTestId('memory-edit-size')).toContainText('Too long')
  await expect(idx.getByRole('button', { name: 'Save' })).toBeDisabled()
  await shot(page, 'memory-page-too-long', { fullPage: true })
  await area.fill('- home: Milan -> home.md\n')
  await idx.getByRole('button', { name: 'Save' }).click()
  await expect(idx.getByTestId('memory-text')).toContainText('home: Milan')
  expect(s.writes).toHaveLength(1)
  expect(s.writes[0].path).toBe('Memory/index.md')
  expect(s.writes[0].query).toContain('overwrite=1')
  expect(s.texts['Memory/index.md']).toBe('- home: Milan -> home.md\n')
})

test('Escape leaves the editor without saving', async ({ page }) => {
  const s = await open(page)
  const f = group(page, 'notes-user:').getByTestId('memory-file').nth(1)
  await f.locator('.mt-file__name').click()
  await f.getByTestId('memory-edit').click()
  await f.getByTestId('memory-edit-text').press('Escape')
  await expect(f.getByTestId('memory-text')).toBeVisible()
  expect(s.writes).toHaveLength(0)
})

test('Delete asks first, then moves the file to the Trash', async ({ page }) => {
  const s = await open(page)
  const f = group(page, 'notes-user:').getByTestId('memory-file').nth(2)
  await f.getByTestId('memory-delete').click()
  await expect(f.getByRole('group', { name: 'Delete job.md?' })).toBeVisible()
  await f.getByRole('button', { name: 'Cancel' }).click()
  expect(s.removed).toEqual([])
  await f.getByTestId('memory-delete').click()
  await f.getByTestId('memory-delete-yes').click()
  await expect(group(page, 'notes-user:').getByTestId('memory-file')).toHaveCount(2)
  expect(s.removed).toEqual(['Memory/job.md'])
  // The page's own live region and the toast both announce the move.
  await expect(page.locator('.sr-only[role="status"]').filter({ hasText: 'Moved job.md to the Trash.' })).toHaveCount(1)
  await expect(page.getByRole('status').filter({ hasText: 'Moved job.md to the Trash.' })).toHaveCount(2)
})

test('Clear all asks to confirm, then removes every file', async ({ page }) => {
  const s = await open(page)
  await page.getByTestId('memory-clear').click()
  await expect(page.getByTestId('memory-clear-confirm')).toContainText('Delete 5 files?')
  await shot(page, 'memory-page-clear', { fullPage: true })
  await page.getByTestId('memory-clear-yes').click()
  await expect(page.getByTestId('memory-empty')).toContainText('No notes yet')
  expect(s.removed.sort()).toEqual(Object.keys(fixture().texts).sort())
})

test('a file over its cap says so', async ({ page }) => {
  const s = fixture()
  s.texts['Memory/index.md'] = Array.from({ length: 45 }, (_, i) => `- n${i}`).join('\n')
  await open(page, s)
  const use = group(page, 'notes-user:').getByTestId('memory-use')
  await expect(use).toContainText('45 of 40 lines')
  await expect(use).toContainText('Over the cap')
})

test('nothing saved yet shows an empty state', async ({ page }) => {
  await open(page, { texts: {}, removed: [], writes: [] })
  await expect(page.getByTestId('memory-empty')).toContainText('No notes yet')
  await expect(page.getByTestId('memory-clear')).toHaveCount(0)
})

test('a server that cannot list shows an error with a retry', async ({ page }) => {
  await mockChat(page)
  await page.route('**/api/chats/context-files', route => route.fulfill({ status: 500, json: { error: { message: 'busy' } } }))
  await page.goto('/app/account?tab=memory')
  await expect(page.getByTestId('memory-load-error')).toBeVisible()
})

test('on a phone the page fits and the rows keep their buttons', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 })
  await open(page)
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  await shot(page, 'memory-page-phone', { fullPage: true })
})

test('the Conversations tab lists the notes saved from messaging chats, to read and delete', async ({ page }) => {
  const s = fixture()
  s.texts['Memory/channels/chat-1/index.md'] = '- coffee: black\n'
  await open(page, s)
  const tab = page.getByTestId('memory-tabs').getByRole('tab', { name: 'Conversations' })
  await tab.click()
  await expect(page.getByTestId('memory-channels-note')).toContainText('not verified')
  const g = group(page, 'notes-channel:chat-1')
  await expect(g.getByRole('heading')).toHaveText('Notes of conversation chat-1')
  await g.locator('.mt-file__name').click()
  await expect(g.getByTestId('memory-text')).toContainText('coffee: black')
  await shot(page, 'memory-conversations')
  await g.getByTestId('memory-delete').click()
  await g.getByTestId('memory-delete-yes').click()
  await expect.poll(() => s.removed).toEqual(['Memory/channels/chat-1/index.md'])
})
