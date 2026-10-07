import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

test.describe('Home console', () => {
  test('renders the greeting header in the sans display font', async ({ page }) => {
    await page.goto('/app')
    const greeting = page.locator('.home-greeting')
    await expect(greeting).toBeVisible({ timeout: 15_000 })
    const family = await greeting.evaluate(el => getComputedStyle(el).fontFamily)
    // Refined-grotesk direction: the greeting uses Geist (no serif).
    expect(family.toLowerCase()).toContain('geist')
    expect(family.toLowerCase()).not.toContain('fraunces')
  })

  test('the greeting carries the date', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
    const date = await page.locator('.home-date').textContent()
    expect(date).toMatch(/\d/)
  })

  test('the library row stays quiet: no filled action in it', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
    // The only solid accent action on the page is Send. Library links are text.
    await expect(page.locator('.home-libline .home-primary, .home-libline .btn-primary')).toHaveCount(0)
  })

  test('the library row keeps gallery, installed models, import and docs', async ({ page }) => {
    await page.goto('/app')
    const row = page.locator('[data-testid="home-library"]')
    await expect(row).toBeVisible({ timeout: 15_000 })
    for (const name of [/browse gallery/i, /installed models/i, /import model/i, /documentation/i]) {
      await expect(row.getByText(name)).toBeVisible()
    }
  })

  test('loaded models sit behind the memory strip, labelled Active models', async ({ page }) => {
    await mockHome(page)
    await page.goto('/app')
    const strip = page.locator('.home-strip')
    await expect(strip).toBeVisible({ timeout: 15_000 })
    // Collapsed by default: the list is one click away.
    await expect(page.getByRole('list', { name: /active models/i })).toHaveCount(0)
    await strip.locator('.home-strip__head').click()
    await expect(page.getByRole('list', { name: /active models/i })).toBeVisible()
  })

  test('the API section is collapsed until asked for', async ({ page }) => {
    await page.goto('/app')
    const head = page.locator('.home-connect__head')
    await expect(head).toBeVisible({ timeout: 15_000 })
    await expect(head).toHaveAttribute('aria-expanded', 'false')
    await expect(page.locator('.home-connect-apis')).toHaveCount(0)
    await head.click()
    await expect(page.locator('.home-connect-apis').first()).toBeVisible()
  })
})
