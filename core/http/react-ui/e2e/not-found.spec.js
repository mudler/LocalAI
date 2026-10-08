import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'

test.describe('404', () => {
  test('names the address, offers a way home and lists the places the viewer can go', async ({ page }) => {
    await page.goto('/app/no-such-page')
    const nf = page.getByTestId('not-found')
    await expect(nf.getByRole('heading', { level: 1 })).toHaveText('This page does not exist')
    await expect(nf).toContainText('/app/no-such-page')
    await expect(nf.getByRole('link', { name: 'Go home' })).toHaveAttribute('href', '/app')
    const places = nf.getByRole('navigation', { name: 'Where to go' })
    for (const name of ['Home', 'Models', 'Chat', 'Studio', 'Talk', 'Build', 'Operate']) {
      await expect(places.getByRole('link', { name })).toBeVisible()
    }
  })

  test('has no search, because the app has none to offer', async ({ page }) => {
    await page.goto('/app/no-such-page')
    await expect(page.getByTestId('not-found').getByRole('searchbox')).toHaveCount(0)
    await expect(page.getByTestId('not-found')).not.toContainText('Press /')
  })

  test('the links go where they say', async ({ page }) => {
    await page.goto('/app/no-such-page')
    await page.getByTestId('not-found').getByRole('link', { name: 'Chat' }).click()
    await expect(page).toHaveURL(/\/app\/chat$/)
    await page.goto('/app/no-such-page')
    await page.getByTestId('not-found').getByRole('link', { name: 'Go home' }).click()
    await expect(page).toHaveURL(/\/app\/?$/)
  })

  test('a person who is not an admin is not offered admin places', async ({ page }) => {
    await mockAccess(page, { status: 'member' })
    await page.goto('/app/no-such-page')
    const places = page.getByTestId('not-found').getByRole('navigation', { name: 'Where to go' })
    await expect(places.getByRole('link', { name: 'Chat' })).toBeVisible()
    await expect(places.getByRole('link', { name: 'Models' })).toHaveCount(0)
    await expect(places.getByRole('link', { name: 'Operate' })).toHaveCount(0)
  })

  test('an address outside the app gets the same page', async ({ page }) => {
    await page.goto('/nowhere/at/all')
    await expect(page.getByTestId('not-found')).toContainText('/nowhere/at/all')
    await expect(page.getByTestId('not-found').getByRole('link', { name: 'Go home' })).toBeVisible()
  })

  test('fits a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/app/no-such-page')
    await expect(page.getByTestId('not-found')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
  })
})
