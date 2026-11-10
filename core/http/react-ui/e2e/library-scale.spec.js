import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS } from './agents-fixtures.js'

// The Knowledge and Skills lists with hundreds of items: the rail keeps a
// bounded height and scrolls inside, search and sort answer at once, the
// choice of order and density is kept, and the keyboard walks the rows.

const LAYERS = ['personal', 'shared', 'addon', 'folder']
const collections = Array.from({ length: 200 }, (_, i) => `kb-${String(i + 1).padStart(3, '0')}`)
const skills = Array.from({ length: 200 }, (_, i) => {
  const layer = LAYERS[i % LAYERS.length]
  return {
    name: `skill-${String(i + 1).padStart(3, '0')}`, description: i === 41 ? 'Summarise a long thread' : 'Does a thing.', layer,
    path: `/s/${i}/SKILL.md`, enabled: true, editable: layer === 'personal',
    ...(layer === 'addon' ? { addon: 'office', addon_title: 'Office' } : {}),
  }
})
const noHScroll = (page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)

async function seed(page) {
  await installAgents(page, {
    agents: AGENTS.map(a => ({ ...a, collections: ['kb-002'], skills: ['skill-002'] })),
    skills, collections,
  })
  await page.route(/\/api\/collections\/[^/?]+\/entries(\?.*)?$/, r => r.fulfill({ json: { entries: Array.from({ length: 300 }, (_, i) => `file-${String(i + 1).padStart(3, '0')}.md`) } }))
  await page.route(/\/api\/collections\/[^/?]+\/sources(\?.*)?$/, r => r.fulfill({ json: { sources: [] } }))
}

test.describe('Knowledge with 200 collections', () => {
  test.beforeEach(async ({ page }) => { await seed(page) })

  test('the rail is bounded, says how many there are and shows a page of rows', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/app/collections')
    await expect(page.getByTestId('collections-count')).toHaveText('200 collections')
    const rail = await page.locator('.lib-rail').boundingBox()
    expect(rail.height).toBeLessThanOrEqual(900)
    // The rows scroll inside the rail; the list does not stretch the page.
    expect(await page.locator('.lib-scroll').evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true)
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(60)
    await page.getByTestId('collections-more').click()
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(120)
    expect(await noHScroll(page)).toBe(0)
  })

  test('search answers with a count and a way back, and Escape clears it', async ({ page }) => {
    await page.goto('/app/collections')
    await page.getByTestId('collections-filter').fill('kb-15')
    await expect(page.getByTestId('collections-count')).toContainText('10 of 200 collections')
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(10)
    await page.getByTestId('collections-filter').press('Escape')
    await expect(page.getByTestId('collections-count')).toHaveText('200 collections')
    await page.getByTestId('collections-filter').fill('nothing-here')
    await expect(page.getByTestId('collections-none')).toBeVisible()
    await page.getByTestId('collections-none').getByRole('button', { name: 'Clear filters' }).click()
    await expect(page.getByTestId('collections-count')).toHaveText('200 collections')
  })

  test('the slash key focuses the search, the arrows walk the rows', async ({ page }) => {
    await page.goto('/app/collections')
    await expect(page.getByTestId('collection-row-kb-001')).toBeVisible()
    await page.keyboard.press('/')
    await expect(page.getByTestId('collections-filter')).toBeFocused()
    await page.getByTestId('collection-row-kb-001').focus()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByTestId('collection-row-kb-002')).toBeFocused()
    await page.keyboard.press('End')
    await expect(page.getByTestId('collection-row-kb-060')).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/app\/collections\/kb-060$/)
  })

  test('sort and density are kept after a reload', async ({ page }) => {
    await page.goto('/app/collections')
    await page.getByTestId('collections-sort').getByRole('button').click()
    await page.getByRole('menuitem', { name: 'Name, Z to A' }).click()
    await page.getByTestId('collections-density').click()
    await page.reload()
    await expect(page.locator('[data-testid^="collection-row-"]').first()).toHaveAttribute('data-testid', 'collection-row-kb-200')
    await expect(page.locator('.lib-rail')).toHaveAttribute('data-density', 'compact')
  })

  test('the used filter counts and filters, the row menu offers its actions', async ({ page }) => {
    await page.goto('/app/collections')
    await expect(page.getByTestId('collections-filter-used')).toContainText('1')
    await page.getByTestId('collections-filter-used').click()
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(1)
    await page.getByTestId('collections-filter-all').click()
    await page.getByTestId('collection-row-kb-003').hover()
    await page.getByRole('button', { name: 'Actions for kb-003' }).click()
    await expect(page.getByRole('menuitem', { name: 'Try a question' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Reset' })).toBeVisible()
  })

  test('a collection with 300 files draws a page of them, filters by name and shows more', async ({ page }) => {
    await page.goto('/app/collections/kb-001')
    await expect(page.getByTestId('entries-count')).toHaveText('300 files')
    await expect(page.locator('[data-testid="entries-list"] > li')).toHaveCount(50)
    await page.getByTestId('entries-more').click()
    await expect(page.locator('[data-testid="entries-list"] > li')).toHaveCount(100)
    await page.getByTestId('entries-filter').fill('file-29')
    await expect(page.getByTestId('entries-count')).toHaveText('10 of 300 files')
    await page.getByTestId('entries-filter').fill('zzz')
    await expect(page.getByTestId('entries-nomatch')).toBeVisible()
  })

  test('a phone gets the page scrolling, a short list and no sideways scroll', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 })
    await page.goto('/app/collections')
    await expect(page.getByTestId('collections-count')).toBeVisible()
    expect(await noHScroll(page)).toBe(0)
    await page.goto('/app/collections/kb-001')
    await expect(page.getByTestId('entries-count')).toBeVisible()
    expect(await noHScroll(page)).toBe(0)
  })

  test('a new collection is made in a sheet', async ({ page }) => {
    let posted
    await page.route(/\/api\/collections$/, route => {
      if (route.request().method() !== 'POST') return route.fallback()
      posted = route.request().postDataJSON()
      return route.fulfill({ json: { status: 'ok' } })
    })
    await page.goto('/app/collections')
    await page.getByTestId('new-collection').click()
    await page.getByLabel('New collection name').fill('fresh')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => posted).toEqual({ name: 'fresh' })
  })
})

test.describe('Skills with 200 skills', () => {
  test.beforeEach(async ({ page }) => { await seed(page) })

  test('groups by source with headings, filters by source and searches the description', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/app/skills')
    await expect(page.getByTestId('skills-count')).toHaveText('200 skills')
    await expect(page.locator('.lib-heading').first()).toContainText('Personal')
    expect((await page.locator('.lib-rail').boundingBox()).height).toBeLessThanOrEqual(900)
    await page.getByTestId('skills-layer').selectOption('addon')
    await expect(page.getByTestId('skills-count')).toContainText('50 of 200 skills')
    await page.getByTestId('skills-layer').selectOption('all')
    await page.getByTestId('skills-search').fill('long thread')
    await expect(page.getByTestId('skill-row-skill-042')).toBeVisible()
    await expect(page.locator('[data-testid^="skill-row-"]')).toHaveCount(1)
    expect(await noHScroll(page)).toBe(0)
  })

  test('the row menu edits and deletes only what the person may', async ({ page }) => {
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-skill-001').hover()
    await page.getByRole('button', { name: 'Actions for skill-001' }).click()
    await expect(page.getByRole('menuitem', { name: 'Edit' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Delete' })).toBeVisible()
    await page.keyboard.press('Escape')
    await page.getByTestId('skills-search').fill('skill-002')
    await page.getByTestId('skill-row-skill-002').hover()
    await expect(page.getByRole('button', { name: 'Actions for skill-002' })).toHaveCount(0)
  })

  test('the order is kept after a reload', async ({ page }) => {
    await page.goto('/app/skills')
    await page.getByTestId('skills-sort').getByRole('button').click()
    await page.getByRole('menuitem', { name: 'Name, Z to A' }).click()
    await page.reload()
    await expect(page.locator('[data-testid^="skill-row-"]').first()).toHaveAttribute('data-testid', 'skill-row-skill-200')
  })

  test('a phone has no sideways scroll at 320 px', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 })
    await page.goto('/app/skills')
    await expect(page.getByTestId('skills-count')).toBeVisible()
    expect(await noHScroll(page)).toBe(0)
  })
})
