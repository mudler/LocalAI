import { test, expect } from './coverage-fixtures.js'

// A page header is its title and supporting line. The hub or section name is
// not repeated above the title as a small crumb: the sidebar and the hub tab
// bar already say where the page lives.
//
// An Administration page is the exception the other way: its header opens with
// the breadcrumb trail (Administration / group / page), because the sidebar of
// Administration does not name the group of a page. The trail is its own
// element, so the title is still the first heading and the eyebrow stays out.
const PAGES = [
  ['/app/collections', 'Knowledge', 'H1'],
  ['/app/activity', 'Activity', 'NAV'],
]

for (const [route, title, leadFirst] of PAGES) {
  test(`${route} shows its title with no crumb above it`, async ({ page }) => {
    await page.goto(route)
    const heading = page.locator('.page-header .page-title').first()
    await expect(heading).toBeVisible({ timeout: 15_000 })
    await expect(heading).toContainText(title)
    await expect(page.locator('.page-header__eyebrow')).toHaveCount(0)
    // Nothing sits in the lead block ahead of the title, except the breadcrumb
    // trail of an Administration page.
    const lead = page.locator('.page-header__lead').first()
    expect(await lead.evaluate((el) => el.firstElementChild?.tagName)).toBe(leadFirst)
    if (leadFirst === 'NAV') {
      await expect(lead.getByTestId('admin-crumbs')).toBeVisible()
      expect(await lead.evaluate((el) => el.children[1]?.tagName)).toBe('H1')
    } else {
      await expect(page.getByTestId('admin-crumbs')).toHaveCount(0)
    }
  })
}
