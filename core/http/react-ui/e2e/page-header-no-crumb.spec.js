import { test, expect } from './coverage-fixtures.js'

// A page header is its title and supporting line. The hub or section name is
// not repeated above the title as a small crumb: the sidebar and the hub tab
// bar already say where the page lives.
const PAGES = [
  ['/app/studio', 'Studio'],
  ['/app/agents', 'Agents'],
  ['/app/activity', 'Activity'],
]

for (const [route, title] of PAGES) {
  test(`${route} shows its title with no crumb above it`, async ({ page }) => {
    await page.goto(route)
    const heading = page.locator('.page-header .page-title').first()
    await expect(heading).toBeVisible({ timeout: 15_000 })
    await expect(heading).toContainText(title)
    await expect(page.locator('.page-header__eyebrow')).toHaveCount(0)
    // Nothing sits in the lead block ahead of the title.
    const lead = page.locator('.page-header__lead').first()
    expect(await lead.evaluate((el) => el.firstElementChild?.tagName)).toBe('H1')
  })
}
