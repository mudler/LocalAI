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

  test('the sidebar has Files, Agents, MCP connectors, Memory, Models and Add-ons, then the recent chats; no Chats entry, no Settings and no Create tier', async ({ page }) => {
    await stubAddonRoutes(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-section-title', { hasText: 'Create' })).toHaveCount(0)
    // Home is where a chat starts: Chats is not an entry of its own.
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/chats"]')).toHaveCount(0)
    await expect(page.getByTestId('sidebar-chats')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/chats/all"]')).toBeVisible()
    for (const href of ['/app/files', '/app/agents', '/app/mcp-connectors', '/app/collections', '/app/models', '/app/addons']) {
      await expect(page.locator(`.sidebar-nav a.nav-item[href="${href}"]`).first()).toBeVisible()
    }
    // Settings lives under the Operate hub, not in the sidebar.
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/settings"]')).toHaveCount(0)
    await page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' }).click()
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

  test('Administration is pinned at the bottom, outside any section, and there is no Build entry', async ({ page }) => {
    await page.goto('/app')
    // It is no longer an item of the Workspace section.
    await expect(page.locator('.sidebar-section-title', { hasText: 'Workspace' })).toHaveCount(0)
    const admin = page.locator('.sidebar-nav--pinned a.nav-item', { hasText: 'Administration' })
    await expect(admin).toBeVisible()
    await expect(page.locator('.sidebar-section a.nav-item', { hasText: 'Administration' })).toHaveCount(0)
    // It sits below the scrolling nav and above the footer controls.
    const [adminBox, homeBox, footerBox] = await Promise.all([
      admin.boundingBox(),
      page.locator('.sidebar-nav a.nav-item', { hasText: 'Home' }).boundingBox(),
      page.locator('.sidebar-footer').boundingBox(),
    ])
    expect(adminBox.y).toBeGreaterThan(homeBox.y)
    expect(adminBox.y + adminBox.height).toBeLessThanOrEqual(footerBox.y)
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
      await expect(item('/app/chats/all')).not.toHaveClass(/active/)
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
    const operate = page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' })
    await expect(operate).toBeVisible()
    await operate.click()
    await expect(page).toHaveURL(/\/app\/operate$/)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="status"]')).toHaveAttribute('aria-current', 'page')
  })

})
