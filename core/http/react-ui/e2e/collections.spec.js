import { test, expect } from './coverage-fixtures.js'

// Collections (Knowledge Base) feature page (src/pages/Collections.jsx).
test.describe('Collections page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/collections')
  })

  test('renders the knowledge base with an empty state and create control', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/collections$/)
    await expect(page.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible()
    await expect(page.getByText(/No collections yet/i)).toBeVisible()
    await expect(page.locator('button.btn-primary').filter({ hasText: 'Create' })).toBeVisible()
  })

  test('new-collection name field accepts input', async ({ page }) => {
    const input = page.locator('input, textarea').first()
    await expect(input).toBeVisible()
    await input.fill('my-kb')
    await expect(input).toHaveValue('my-kb')
  })
})
