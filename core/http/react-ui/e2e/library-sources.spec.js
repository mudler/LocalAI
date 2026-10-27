import { test, expect } from './coverage-fixtures.js'

// Library sources of all three kinds: add-ons on the Add-ons Libraries tab,
// models on the Models page and backends on the Backends page, each in the
// panel its "Manage libraries" button opens, against a real
// server that starts like a normal install (playwright.library.config.js): the
// default sources of the add-on, backend and model kinds are in the store, and
// the routes change the store the `local-ai library` command edits. The specs
// share one server and change its sources, so they run in order.
//
// The conflict message needs two sources whose fetched indexes list one entry,
// which a test server without a network cannot give; that spec answers the list
// call itself and checks what the page shows of the message.

test.describe.configure({ mode: 'serial' })

const ROW = (page, id) => page.locator(`[data-source="${id}"]`)
// The defaults this build enables. The model gallery index `localai` is a default
// too, but a build reads the model library or the gallery, not both, so it is
// listed turned off.
const OFFICIAL = { addon: 'official', backend: 'localai-backends', model: 'official-models' }

const PAGES = { addon: '/app/addons?tab=libraries', model: '/app/models', backend: '/app/backends' }

async function openLibraries(page, kind = 'addon') {
  await page.goto(PAGES[kind])
  if (kind !== 'addon') await page.getByRole('button', { name: 'Manage libraries' }).click()
  await expect(page.getByRole('list', { name: 'Libraries' })).toBeVisible()
}

async function sources(page) {
  const res = await page.request.get('/api/library/sources')
  expect(res.ok()).toBe(true)
  return (await res.json())
}

test.describe('Library sources: all three kinds', () => {
  test('each page lists only its own kind, with a Default badge and no kind filter', async ({ page }) => {
    const expected = { addon: ['official'], backend: ['localai-backends'], model: ['official-models', 'localai'] }
    for (const [kind, ids] of Object.entries(expected)) {
      await openLibraries(page, kind)
      await expect(page.locator('[data-source]')).toHaveCount(ids.length)
      for (const id of ids) {
        const row = ROW(page, id)
        await expect(row).toBeVisible()
        await expect(row).toHaveAttribute('data-kind', kind)
        await expect(row.getByTestId('source-default')).toHaveText('Default')
      }
      await expect(ROW(page, OFFICIAL[kind]).getByTestId('source-state')).toHaveText('On')
      await expect(page.getByRole('group', { name: 'Filter by kind' })).toHaveCount(0)
    }
    await openLibraries(page, 'model')
    await expect(ROW(page, 'localai').getByTestId('source-state')).toHaveText('Off')
  })

  test('adds a model source of your own, which has no Default badge and stays after a reload', async ({ page }) => {
    await openLibraries(page, 'model')
    await page.getByRole('button', { name: 'Add a library' }).click()
    const sheet = page.getByTestId('source-editor')
    await expect(sheet.getByLabel('Kind')).toHaveCount(0)
    await sheet.getByLabel('Index format').selectOption('gallery')
    await sheet.getByLabel('Name').fill('Team models')
    await sheet.getByLabel('Catalog URL').fill('https://models.example.org/index.yaml')
    await sheet.getByRole('button', { name: 'Save' }).click()
    const row = ROW(page, 'team-models')
    await expect(row).toBeVisible()
    await expect(row).toHaveAttribute('data-kind', 'model')
    await expect(row.getByTestId('source-default')).toHaveCount(0)
    await expect(row.getByTestId('source-state')).toHaveText('On')

    const stored = (await sources(page)).sources.find(s => s.id === 'team-models')
    expect(stored).toMatchObject({ kind: 'model', format: 'gallery', default: false, enabled: true, url: 'https://models.example.org/index.yaml' })
    await page.reload()
    await page.getByRole('button', { name: 'Manage libraries' }).click()
    await expect(ROW(page, 'team-models')).toBeVisible()

    // The same address twice is refused with the reason.
    await page.getByRole('button', { name: 'Add a library' }).click()
    const again = page.getByTestId('source-editor')
    await again.getByLabel('Name').fill('Same address')
    await again.getByLabel('Catalog URL').fill('https://models.example.org/index.yaml')
    await again.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('alert').filter({ hasText: 'already a model source' })).toBeVisible()
  })

  test('turns a default off and removes it: it stays listed as removed, and restoring brings it back', async ({ page }) => {
    await openLibraries(page, 'model')
    const row = ROW(page, 'official-models')
    await row.locator('[data-action="disable"]').click()
    await expect(row.getByTestId('source-state')).toHaveText('Off')
    await expect(row).toHaveAttribute('data-state', 'disabled')
    expect((await sources(page)).sources.find(s => s.id === 'official-models')).toMatchObject({ enabled: false, removed: false, default: true })

    await row.locator('[data-action="remove"]').click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('stays removed at the next start')
    await dialog.getByRole('button', { name: 'Remove' }).click()
    await expect(row).toHaveAttribute('data-state', 'removed')
    await expect(row.getByTestId('source-state')).toHaveText('Removed')
    await expect(row.getByTestId('source-default')).toHaveText('Default')
    // The tombstone is in the store the CLI reads, not only on the page.
    expect((await sources(page)).sources.find(s => s.id === 'official-models')).toMatchObject({ removed: true, enabled: false, default: true })
    await page.reload()
    await page.getByRole('button', { name: 'Manage libraries' }).click()
    await expect(ROW(page, 'official-models')).toHaveAttribute('data-state', 'removed')

    await ROW(page, 'official-models').locator('[data-action="restore"]').click()
    await expect(ROW(page, 'official-models')).toHaveAttribute('data-state', 'enabled')
    expect((await sources(page)).sources.find(s => s.id === 'official-models')).toMatchObject({ removed: false, enabled: true })

    // A source of your own is deleted, not kept as a tombstone.
    await ROW(page, 'team-models').locator('[data-action="remove"]').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Remove' }).click()
    await expect(ROW(page, 'team-models')).toHaveCount(0)
    expect((await sources(page)).sources.find(s => s.id === 'team-models')).toBeUndefined()
  })

  test('shows the conflict message the store gives next to the source that clashes', async ({ page }) => {
    const message = 'the addon "tables" is listed by two of your library sources, "first" and "second"; disable or remove one of them (local-ai library disable second)'
    await page.route('**/api/library/sources', route => route.request().method() === 'GET'
      ? route.fulfill({
        json: {
          officialLibraries: true,
          sources: [
            { kind: 'addon', id: 'first', name: 'First', url: 'https://one.example/index.yaml', enabled: true, default: false, removed: false, suspended: false, conflict: message },
            { kind: 'addon', id: 'second', name: 'Second', url: 'https://two.example/index.yaml', enabled: true, default: false, removed: false, suspended: false, conflict: message },
            { kind: 'addon', id: 'official', name: 'Official', url: 'https://off.example/index.yaml', enabled: true, default: true, removed: false, suspended: false, conflict: 'the addon "tables" is provided by your source "first", so this default source is ignored for it' },
          ],
        },
      })
      : route.fallback())
    await openLibraries(page)
    await expect(ROW(page, 'first').getByTestId('source-conflict')).toContainText('two of your library sources')
    await expect(ROW(page, 'second').getByTestId('source-conflict')).toContainText('disable or remove one of them')
    await expect(ROW(page, 'official').getByTestId('source-conflict')).toContainText('this default source is ignored for it')
  })
})

test.describe('Library sources: Use official libraries', () => {
  const settingRow = page => page.locator('[data-field="official_libraries"]')

  async function setOfficial(page, on) {
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /^Backends and galleries/ }).click()
    const toggle = settingRow(page).getByRole('switch')
    await expect(settingRow(page)).toContainText('Use official libraries')
    await expect(toggle).toHaveAttribute('aria-checked', on ? 'false' : 'true')
    await toggle.click()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText('Settings saved successfully')).toBeVisible()
  }

  const settings = async page => (await (await page.request.get('/api/settings')).json())

  test('turning it off hides the default galleries and shows the defaults as off, and on brings them back', async ({ page }) => {
    // On: the default model and backend galleries are read.
    const on = await settings(page)
    expect(on.official_libraries).toBe(true)
    expect(on.galleries.length).toBeGreaterThan(0)
    expect(on.backend_galleries.length).toBeGreaterThan(0)
    const defaultUrls = [...on.galleries, ...on.backend_galleries].map(g => g.url)

    // A source of your own, so there is something that must stay.
    const added = await page.request.post('/api/library/sources', { data: { kind: 'model', format: 'gallery', id: 'mine', name: 'Mine', url: 'https://mine.example.org/index.yaml' } })
    expect(added.status()).toBe(201)

    await setOfficial(page, false)
    const off = await settings(page)
    expect(off.official_libraries).toBe(false)
    expect(off.galleries.map(g => g.url)).toEqual(['https://mine.example.org/index.yaml'])
    expect(off.backend_galleries).toEqual([])

    // The Libraries tab lists the defaults, shown as off; the source of your own is on.
    for (const [kind, id] of Object.entries(OFFICIAL)) {
      await openLibraries(page, kind)
      await expect(page.getByTestId('official-off')).toBeVisible()
      await expect(ROW(page, id)).toHaveAttribute('data-state', 'suspended')
      await expect(ROW(page, id).getByTestId('source-default')).toBeVisible()
      await expect(ROW(page, id).getByTestId('source-state')).toContainText('Off')
    }
    await openLibraries(page, 'model')
    await expect(ROW(page, 'mine')).toHaveAttribute('data-state', 'enabled')

    await setOfficial(page, true)
    const back = await settings(page)
    expect(back.official_libraries).toBe(true)
    expect(back.galleries.map(g => g.url)).toEqual(expect.arrayContaining([...on.galleries.map(g => g.url), 'https://mine.example.org/index.yaml']))
    expect(back.backend_galleries.map(g => g.url)).toEqual(on.backend_galleries.map(g => g.url))
    for (const [kind, id] of Object.entries(OFFICIAL)) {
      await openLibraries(page, kind)
      await expect(page.getByTestId('official-off')).toHaveCount(0)
      await expect(ROW(page, id)).toHaveAttribute('data-state', 'enabled')
    }
    await page.request.delete('/api/library/sources/mine')
  })
})
