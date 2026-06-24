import { test, expect } from './coverage-fixtures.js'

// Skills feature page (src/pages/Skills.jsx).
test.describe('Skills page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/skills')
  })

  test('renders the skills list with create affordances', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/skills$/)
    await expect(page.getByRole('heading', { name: 'Skills', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'New skill' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Git Repos' })).toBeVisible()
  })

  test('New skill navigates to the skill editor', async ({ page }) => {
    await page.getByRole('button', { name: 'New skill' }).click()
    await expect(page).toHaveURL(/\/app\/skills\/new$/)
  })
})
