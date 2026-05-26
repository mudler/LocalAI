import { test, expect } from './coverage-fixtures.js'

// P2P (Swarm) admin page — renders in the no-auth test harness (isAdmin).
test.describe('P2P page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/p2p')
  })

  test('renders the P2P distribution overview and capability cards', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/p2p$/)
    await expect(page.getByRole('heading', { name: /P2P Distribution Not Enabled/i })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Instance Federation' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Model Sharding' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Resource Sharing' })).toBeVisible()
    await expect(page.getByRole('heading', { name: /How to Enable P2P/i })).toBeVisible()
  })

  test('hardware selector offers build targets and responds to selection', async ({ page }) => {
    const cpu = page.getByRole('button').filter({ hasText: /^CPU$/ })
    const cuda = page.getByRole('button').filter({ hasText: /^CUDA 12$/ })
    await expect(cpu).toBeVisible()
    await expect(cuda).toBeVisible()
    await cuda.click() // selecting a build target must not break the page
    await expect(page.getByRole('heading', { name: /How to Enable P2P/i })).toBeVisible()
  })
})
