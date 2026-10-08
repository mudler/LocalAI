import { test, expect } from './coverage-fixtures.js'
import { mockLedger } from './ledger-fixtures.js'

// The Explore inspector holds the model's actions in its header. Whatever the
// pane width, none of them may spill past the pane, and the text must sit
// inside the pane's padding rather than against its border.

const pane = (page) => page.getByTestId('discover-pane')
const SHOTS = process.env.EXPLORE_PANE_SHOTS

async function openModel(page, name) {
  await mockLedger(page)
  await page.goto('/app/models')
  // Search first: on a phone the grouped list is long and the row may sit
  // below a collapsed group.
  await expect(page.getByTestId('models-search')).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('models-search').fill(name)
  const row = page.locator(`[data-entity="${name}"]`)
  await expect(row).toBeVisible({ timeout: 15_000 })
  await row.click()
  await expect(pane(page).locator('.detail-pane')).toBeVisible()
}

async function expectButtonsInside(page) {
  const box = await pane(page).boundingBox()
  const buttons = pane(page).locator('.detail-pane__actions button:visible')
  const count = await buttons.count()
  expect(count).toBeGreaterThan(0)
  for (let i = 0; i < count; i++) {
    const b = await buttons.nth(i).boundingBox()
    expect(b.x, `button ${i} left edge`).toBeGreaterThanOrEqual(box.x)
    expect(b.x + b.width, `button ${i} right edge`).toBeLessThanOrEqual(box.x + box.width)
  }
  const scrolls = await pane(page).evaluate((el) => el.scrollWidth > el.clientWidth + 1)
  expect(scrolls).toBe(false)
}

for (const theme of ['light', 'dark']) {
  for (const [label, width] of [['wide', 1440], ['pane-1100', 1100], ['pane-920', 920]]) {
    for (const [kind, name] of [['installed', 'qwen3-8b-instruct'], ['available', 'qwen3-32b-instruct']]) {
      test(`${kind} model actions stay inside the pane (${theme}, ${label})`, async ({ page }) => {
        await page.addInitScript((m) => localStorage.setItem('localai-theme', m), theme)
        await page.setViewportSize({ width, height: 900 })
        await openModel(page, name)
        await expectButtonsInside(page)
        // Padding: the pane's first text sits clear of its border.
        const padding = await pane(page).evaluate((el) => parseFloat(getComputedStyle(el).paddingLeft))
        expect(padding).toBeGreaterThanOrEqual(16)
        const head = await pane(page).locator('.detail-pane__name').boundingBox()
        const box = await pane(page).boundingBox()
        expect(head.x - box.x).toBeGreaterThanOrEqual(16)
        if (SHOTS) await pane(page).screenshot({ path: `${SHOTS}/${kind}-${theme}-${label}.png` })
      })
    }
  }
}

test('a 320px pane keeps the actions inside it', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await openModel(page, 'qwen3-8b-instruct')
  await pane(page).evaluate((el) => { el.style.width = '320px'; el.style.maxWidth = '320px'; el.style.justifySelf = 'start' })
  await expectButtonsInside(page)
  if (SHOTS) await pane(page).screenshot({ path: `${SHOTS}/installed-320.png` })
})
