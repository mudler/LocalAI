import { test, expect } from './coverage-fixtures.js'

// The Skills page has an item of its own in the sidebar, right after Knowledge.
// Skills work in every chat, so Agents does not light up on the page.

const item = (page, href) => page.locator(`.sidebar-nav a.nav-item[href="${href}"]`)

test.describe('Skills in the sidebar', () => {
  test('the item follows Knowledge, opens the page and is the only one lit up', async ({ page }) => {
    await page.goto('/app')
    await expect(item(page, '/app/skills')).toBeVisible()
    const hrefs = await page.locator('.sidebar-nav a.nav-item').evaluateAll(els => els.map(e => e.getAttribute('href')))
    expect(hrefs[hrefs.indexOf('/app/collections') + 1]).toBe('/app/skills')
    await expect(item(page, '/app/skills').locator('.nav-label')).toHaveText('Skills')
    await item(page, '/app/skills').click()
    await expect(page).toHaveURL(/\/app\/skills$/)
    await expect(page.getByTestId('skills-page')).toBeVisible()
    await expect(item(page, '/app/skills')).toHaveClass(/active/)
    await expect(item(page, '/app/agents')).not.toHaveClass(/active/)
    await expect(item(page, '/app/collections')).not.toHaveClass(/active/)
  })

  test('Agents is not lit up on any skills page, and Skills is', async ({ page }) => {
    for (const path of ['/app/skills', '/app/skills/new']) {
      await page.goto(path)
      await expect(item(page, '/app/skills')).toHaveClass(/active/)
      await expect(item(page, '/app/agents')).not.toHaveClass(/active/)
    }
  })

  test('the page says what skills are and that every chat can use them', async ({ page }) => {
    await page.goto('/app/skills')
    await expect(page.locator('.page-header')).toContainText('all your chats')
  })

  test('the page loads without console errors', async ({ page }) => {
    const errors = []
    page.on('pageerror', e => errors.push(e.message))
    page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
    await page.goto('/app/skills')
    await expect(page.getByTestId('skills-page')).toBeVisible()
    expect(errors).toEqual([])
  })
})

test.describe('Skills in the sidebar on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the menu lists Skills after Knowledge and opens the page', async ({ page }) => {
    await page.goto('/app')
    await page.getByRole('button', { name: 'Open menu' }).click()
    await expect(item(page, '/app/skills')).toBeVisible()
    const hrefs = await page.locator('.sidebar-nav a.nav-item').evaluateAll(els => els.map(e => e.getAttribute('href')))
    expect(hrefs[hrefs.indexOf('/app/collections') + 1]).toBe('/app/skills')
    await item(page, '/app/skills').click()
    await expect(page).toHaveURL(/\/app\/skills$/)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
})
