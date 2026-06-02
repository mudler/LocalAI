import { test, expect } from './coverage-fixtures.js'

// Backends admin page (src/pages/Backends.jsx).
test.describe('Backends management page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/backends')
  })

  test('renders the management header and gallery tabs', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/backends$/)
    await expect(page.getByRole('heading', { name: 'Backend Management' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Manual Install' })).toBeVisible()
    await expect(page.getByRole('button').filter({ hasText: /^All$/ })).toBeVisible()
    await expect(page.getByRole('button').filter({ hasText: /^Image$/ })).toBeVisible()
  })

  test('search field accepts input', async ({ page }) => {
    const search = page.getByPlaceholder(/search backends/i)
    await expect(search).toBeVisible()
    await search.fill('whisper')
    await expect(search).toHaveValue('whisper')
  })

  test('Manual Install reveals the OCI install form', async ({ page }) => {
    await page.getByRole('button', { name: 'Manual Install' }).click()
    await expect(page.getByPlaceholder('oci://quay.io/example/backend:latest')).toBeVisible()
  })
})
