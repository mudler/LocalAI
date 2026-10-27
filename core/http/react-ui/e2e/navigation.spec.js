import { test, expect } from './coverage-fixtures.js'
import { stubAddonRoutes } from './addons-fixtures.js'

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

  test('the sidebar has Chats, Files, Agents, MCP connectors, Memory, Models and Add-ons, no Settings and no Create tier', async ({ page }) => {
    await stubAddonRoutes(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-section-title', { hasText: 'Create' })).toHaveCount(0)
    for (const href of ['/app/chats', '/app/files', '/app/agents', '/app/mcp-connectors', '/app/collections', '/app/models', '/app/addons']) {
      await expect(page.locator(`.sidebar-nav a.nav-item[href="${href}"]`).first()).toBeVisible()
    }
    // Settings lives under the Operate hub, not in the sidebar.
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/settings"]')).toHaveCount(0)
    await page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' }).click()
    await expect(page).toHaveURL(/\/app\/operate$/)
    const settingsTab = page.locator('.dk-hubtabs [data-hub-tab="settings"]')
    await expect(settingsTab).toBeVisible()
    await settingsTab.click()
    await expect(page).toHaveURL(/\/app\/settings/)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="settings"]')).toHaveAttribute('aria-current', 'page')
    // Studio and Talk are add-ons: the sidebar does not list them; they are in the drawer while installed.
    for (const href of ['/app/studio', '/app/talk']) {
      await expect(page.locator(`.sidebar-nav a.nav-item[href="${href}"]`)).toHaveCount(0)
    }
  })

  test('Operate sits under a Workspace label and there is no Build entry', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-section-title', { hasText: 'Workspace' })).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })).toHaveCount(0)
  })

  test('the old Build address opens the models, and Import starts from adding a model', async ({ page }) => {
    await page.goto('/app/build')
    await expect(page).toHaveURL(/\/app\/models/)
    await page.goto('/app/model-editor')
    await page.getByTestId('add-model-import').click()
    await expect(page).toHaveURL(/\/app\/import-model$/)
  })

  test('Agents, MCP connectors and Memory are sidebar items, and their pages light them up', async ({ page }) => {
    await page.goto('/app')
    const item = (href) => page.locator(`.sidebar-nav a.nav-item[href="${href}"]`)
    await item('/app/agents').click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    await expect(item('/app/agents')).toHaveClass(/active/)
    // Triggers, skills and the messaging connectors belong to the agents.
    for (const path of ['/app/triggers', '/app/skills', '/app/chats/connectors']) {
      await page.goto(path)
      await expect(item('/app/agents')).toHaveClass(/active/)
      await expect(item('/app/chats')).not.toHaveClass(/active/)
    }
    await item('/app/mcp-connectors').click()
    await expect(page.getByTestId('mcp-connectors-page')).toBeVisible()
    await expect(item('/app/mcp-connectors')).toHaveClass(/active/)
    await item('/app/collections').click()
    await expect(page).toHaveURL(/\/app\/collections$/)
    await expect(item('/app/collections')).toHaveClass(/active/)
    // Memory is a page of its own now, not a tab of a hub.
    await expect(page.locator('.dk-hubtabs')).toHaveCount(0)
  })

  test('the old MCP tab of the connectors opens the MCP connectors page', async ({ page }) => {
    await page.goto('/app/chats/connectors?kind=mcp')
    await expect(page).toHaveURL(/\/app\/mcp-connectors$/)
    await expect(page.getByTestId('mcp-connectors-page')).toBeVisible()
    await page.goto('/app/chats/connectors')
    await expect(page.getByRole('tab', { name: 'MCP server' })).toHaveCount(0)
  })

  test('Operate is a single entry that opens the Operate hub', async ({ page }) => {
    await page.goto('/app')
    const operate = page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })
    await expect(operate).toBeVisible()
    await operate.click()
    await expect(page).toHaveURL(/\/app\/operate$/)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="status"]')).toHaveAttribute('aria-current', 'page')
  })

})
