import { test, expect } from './coverage-fixtures.js'

test.describe('Navigation', () => {
  test('/ redirects to /app', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/app/)
  })

  test('/app shows the home page', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar')).toBeVisible()
    await expect(page.locator('.home-page')).toBeVisible()
  })

  test('top menu exposes Home and Models', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app"]')).toBeVisible()
    const models = page.locator('.sidebar-nav a.nav-item[href="/app/models"]')
    await expect(models).toBeVisible()
    await expect(models.locator('.nav-label')).toHaveText('Models')
  })

  test('Create stays an inline tier with Chat, Studio and Talk', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-section-title', { hasText: 'Create' })).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/chat"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/studio"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/talk"]')).toBeVisible()
  })

  test('Build and Operate sit under a Workspace label', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-section-title', { hasText: 'Workspace' })).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })).toBeVisible()
  })

  test('Build is a single entry that opens the Build hub', async ({ page }) => {
    await page.goto('/app')
    const build = page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })
    await expect(build).toBeVisible()
    await build.click()
    await expect(page).toHaveURL(/\/app\/build$/)
    const bar = page.locator('.dk-hubtabs')
    await expect(bar.locator('[data-hub-tab="overview"]')).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })).toHaveClass(/active/)
  })

  test('Operate is a single entry that opens the Operate hub', async ({ page }) => {
    await page.goto('/app')
    const operate = page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })
    await expect(operate).toBeVisible()
    await operate.click()
    await expect(page).toHaveURL(/\/app\/operate$/)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="status"]')).toHaveAttribute('aria-current', 'page')
  })

  test('Build hub lists its tools as tabs', async ({ page }) => {
    await page.goto('/app/agents')
    const bar = page.locator('.dk-hubtabs')
    await expect(bar).toBeVisible()
    for (const id of ['overview', 'agents', 'skills', 'memory', 'jobs', 'fine-tune', 'quantize', 'import', 'voices', 'faces']) {
      await expect(bar.locator(`[data-hub-tab="${id}"]`)).toBeVisible()
    }
    await expect(bar.locator('a[href="/app/fine-tune"]')).toBeVisible()
    await expect(bar.locator('a[href="/app/face"]')).toBeVisible()
    await expect(bar.locator('[data-hub-tab="agents"]')).toHaveAttribute('aria-current', 'page')
  })

  test('the Build overview lists the tools with a line each', async ({ page }) => {
    await page.goto('/app/build')
    const list = page.getByRole('list', { name: 'Build tools' })
    // Each tool is a row with its sentence and an Open link.
    const row = (path) => list.locator('li.bt-tool', { has: page.locator(`a[href="${path}"]`) })
    await expect(row('/app/agents')).toContainText('Create and run agents')
    await expect(list.locator('a[href="/app/fine-tune"]')).toBeVisible()
  })

})
