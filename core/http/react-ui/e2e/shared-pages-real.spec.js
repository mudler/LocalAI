import { test, expect } from './coverage-fixtures.js'

// Pages of shared folders against the real test server started with
// --shared-pages-fixture: /shared/team (read-only for the viewer, written by
// "luca") has the pages standup and releases (allowed in an earlier version),
// /shared/board (writable) has the page board. Nothing of a shared page runs
// before the trust sheet; the bridge works only with the consent, with the
// viewer's own grant; a revoke and the administrator's switch stop it.

test.describe.configure({ mode: 'serial', timeout: 90_000 })

const api = (request, path) => request.get(path).then(r => r.json())
// Screenshots wait for the entry animations, so a sheet is drawn as it stays.
const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().filter(a => a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))))
const shot = async (page, name) => { await settle(page); await page.screenshot({ path: `test-results/ws-share/${name}.png` }) }
const frame = (page) => page.frameLocator('[data-testid="page-frame"]')
let pages = []
const pathOf = (name) => {
  const p = pages.find(x => x.name === name)
  return `/app/pages/shared/${p.folder_id}/${p.name}`
}

test.beforeAll(async ({ request }) => {
  pages = (await api(request, '/api/chats/shared-pages')).pages
  expect(pages.map(p => p.name).sort()).toEqual(['board', 'releases', 'standup'])
  // A run before this one may have allowed them; releases keeps the consent
  // the fixture gave it.
  for (const p of pages.filter(x => x.name !== 'releases')) await request.delete(`/api/chats/shared-pages/${p.folder_id}/${p.name}/consent`)
})

test('the Pages screen lists the shared pages with their author, folder and state', async ({ page }) => {
  await page.goto('/app/pages')
  const group = page.getByTestId('shared-group')
  await expect(group).toBeVisible()
  await expect(group.getByRole('heading', { name: 'Shared with you' })).toBeVisible()
  const standup = group.locator('[data-testid="shared-card"][data-page="standup"]')
  await expect(standup.getByTestId('shared-badge')).toHaveText('Not allowed yet')
  await expect(standup).toContainText('From luca')
  await expect(standup).toContainText('shared/team/pages/standup')
  await expect(standup.getByTestId('shared-open')).toHaveText('Review')
  await expect(group.locator('[data-testid="shared-card"][data-page="releases"]').getByTestId('shared-badge')).toHaveText('Changed since you allowed it')
  await expect(group).toContainText('A shared page runs only after you allow it.')
  await expect(page.getByRole('link', { name: /^Pages/ }).first()).toBeVisible()
  await shot(page, 's4-pages-list-desktop-dark')
})

test('the first open shows the trust sheet with Cancel focused, and nothing runs', async ({ page }) => {
  await page.goto(pathOf('standup'))
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(sheet).toHaveAttribute('data-variant', 'first')
  await expect(sheet.getByRole('heading', { name: 'Open "Team standup"?' })).toBeVisible()
  await expect(sheet).toContainText('Written by luca')
  await expect(sheet).toContainText('shared/team')
  await expect(sheet).toContainText('Read and write files in its own folder')
  await expect(sheet).toContainText('You can only read this shared folder')
  await expect(sheet).toContainText('Reach the network or other sites')
  await expect(sheet).toContainText('it can load another address in its own frame. That sends one request out, and then the page stops.')
  await expect(sheet).toContainText('It acts as you inside its folder. If luca changes it, you are asked again.')
  await expect(sheet.getByTestId('trust-cancel')).toBeFocused()
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  await expect(page.getByTestId('shared-pill')).toHaveText(/Not allowed yet/)
  await shot(page, 's4-trust-first-desktop-dark')

  // View source opens beside the sheet, as text.
  await sheet.getByTestId('trust-source').click()
  const src = page.getByTestId('trust-source-sheet')
  await expect(src).toContainText("localai.files.read('data.json')")
  await page.keyboard.press('Escape')
  await expect(src).toHaveCount(0)
  await expect(sheet).toBeVisible()

  // Escape is Cancel: the frame says the page has not run.
  await page.keyboard.press('Escape')
  await expect(sheet).toHaveCount(0)
  await expect(page.getByTestId('shared-blocked')).toContainText('This page has not run')
  await expect(page.getByTestId('shared-blocked')).toContainText("It comes from luca's shared folder shared/team.")
  await expect(page.getByTestId('shared-pill')).toHaveText(/Not allowed/)
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  await shot(page, 's4-blocked-desktop-dark')
})

test('allow once runs the page for this visit only; a read-only folder refuses its write', async ({ page }) => {
  await page.goto(pathOf('standup'))
  await page.getByTestId('trust-allow-once').click()
  await expect(page.getByTestId('shared-pill')).toHaveText(/Allowed for this visit/)
  await expect(frame(page).locator('#read')).toHaveText('{"n":1}', { timeout: 15_000 })
  await expect(frame(page).locator('#list')).toContainText('data.json')
  await frame(page).locator('#w').click()
  await expect(frame(page).locator('#write')).toContainText('ERR you can only read this shared folder')
  expect((await api(page.request, '/api/chats/shared-pages')).pages.find(p => p.name === 'standup').consent).toBe('none')
  await page.reload()
  await expect(page.getByTestId('trust-sheet')).toBeVisible()
})

test('allow runs it on every open; stop allowing closes it, and a revoke elsewhere stops it at its next call', async ({ page }) => {
  await page.goto(pathOf('board'))
  await page.getByTestId('trust-allow').click()
  await expect(frame(page).locator('#read')).toHaveText(/^\{"n":\d+\}$/, { timeout: 15_000 })
  await frame(page).locator('#w').click()
  await expect(frame(page).locator('#write')).toHaveText(/^v\d+$/)
  await expect(page.getByTestId('shared-pill')).toHaveText(/Allowed/)
  await shot(page, 's4-running-desktop-dark')
  await page.reload()
  await expect(page.getByTestId('trust-sheet')).toHaveCount(0)
  await expect(frame(page).locator('#read')).toHaveText('{"n":2}', { timeout: 15_000 })

  // Revoked from another tab: the next bridge call is refused and the frame goes.
  const p = pages.find(x => x.name === 'board')
  expect((await page.request.delete(`/api/chats/shared-pages/${p.folder_id}/board/consent`)).status()).toBe(204)
  await frame(page).locator('#again').click()
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  await expect(page.getByTestId('shared-blocked')).toContainText('no longer allowed')

  // Stop allowing from the page menu.
  await page.getByTestId('shared-review').click()
  await page.getByTestId('trust-allow').click()
  await expect(frame(page).locator('#read')).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('shared-menu').click()
  await page.getByTestId('shared-revoke').click()
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  await expect(page.getByTestId('shared-blocked')).toContainText('You stopped allowing this page')
  expect((await api(page.request, '/api/chats/shared-pages')).pages.find(x => x.name === 'board').consent).toBe('none')
})

test('a page changed since it was allowed asks again and says what changed', async ({ page }) => {
  await page.goto(pathOf('releases'))
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toHaveAttribute('data-variant', 'changed')
  await expect(sheet.getByRole('heading', { name: '"Release calendar" has changed' })).toBeVisible()
  await expect(sheet).toContainText('Changed by luca')
  await expect(sheet).toContainText('You allowed the version of')
  await expect(sheet.getByTestId('trust-changes')).toContainText('index.html')
  await expect(sheet.getByTestId('trust-changes')).toContainText('3 → 4 lines')
  await expect(sheet).toContainText('If you allow it, this version runs each time you open it, until luca changes it again.')
  await expect(sheet.getByTestId('trust-cancel')).toBeFocused()
  await expect(page.getByTestId('shared-pill')).toHaveText(/Changed since you allowed it/)
  await shot(page, 's4-trust-changed-desktop-dark')
})

for (const v of [{ name: 'desktop', width: 1440, height: 900 }, { name: 'phone', width: 390, height: 844 }]) {
  for (const theme of ['dark', 'light']) {
    test(`the trust sheet and the shared group, ${v.name} ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: v.width, height: v.height })
      await page.addInitScript((m) => localStorage.setItem('localai-theme', m), theme)
      await page.goto('/app/pages')
      await expect(page.getByTestId('shared-group')).toBeVisible()
      await shot(page, `s4-pages-list-${v.name}-${theme}`)
      await page.goto(pathOf('standup'))
      await expect(page.getByTestId('trust-sheet')).toBeVisible()
      await shot(page, `s4-trust-first-${v.name}-${theme}`)
      await page.goto(pathOf('releases'))
      await expect(page.getByTestId('trust-sheet')).toHaveAttribute('data-variant', 'changed')
      await shot(page, `s4-trust-changed-${v.name}-${theme}`)
      await page.getByTestId('trust-cancel').click()
      await expect(page.getByTestId('shared-blocked')).toBeVisible()
      await shot(page, `s4-blocked-${v.name}-${theme}`)
    })
  }
}

test('an administrator switches shared pages off: the list says so and a page does not open', async ({ page }) => {
  expect((await page.request.post('/api/settings', { data: { shared_pages_enabled: false } })).ok()).toBe(true)
  try {
    await page.goto('/app/pages')
    await expect(page.getByTestId('shared-off')).toContainText('Shared pages are off on this server. An administrator turned them off.')
    await expect(page.getByTestId('shared-open')).toHaveCount(0)
    await shot(page, 's4-shared-off-desktop-dark')
    await page.goto(pathOf('board'))
    await expect(page.getByTestId('shared-off')).toContainText('Shared pages are off on this server.')
  } finally {
    await page.request.post('/api/settings', { data: { shared_pages_enabled: true } })
  }
})
