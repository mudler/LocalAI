import { test, expect } from './coverage-fixtures.js'

// The Operate hub replaces the second navigation rail with a tab bar (the
// kit's dk-hubtabs). The Build hub is gone: its pages are sidebar items or
// part of adding a model. Routes stay flat; the tab that owns the current
// path carries aria-current="page", and a tab nobody can use is not drawn.

const BAR = '.dk-hubtabs'
const tab = (page, id) => page.locator(`${BAR} [data-hub-tab="${id}"]`)

async function mockFeatures(page, features) {
  await page.route('**/api/features', route => route.fulfill({ json: features }))
}

async function mockAuth(page, user, authEnabled = true) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled, staticApiKeyRequired: false, providers: ['local'], user },
  }))
}

async function quietOperate(page) {
  await page.route('**/api/backends/upgrades', route => route.fulfill({ json: {} }))
  await page.route('**/api/operations', route => route.fulfill({ json: [] }))
  await page.route('**/api/nodes', route => route.fulfill({ json: [{ id: 'n1', name: 'atlas', status: 'healthy', healthy: true }] }))
}

test.describe('Hub tabs: the right tab for each route', () => {
  // The pages the Build hub had are pages of their own: no tab bar.
  for (const path of ['/app/collections', '/app/import-model']) {
    test(`${path} has no hub tab bar`, async ({ page }) => {
      await page.goto(path)
      await expect(page.locator('h1').first()).toBeVisible()
      await expect(page.locator(BAR)).toHaveCount(0)
    })
  }

  const operate = [
    ['/app/operate', 'status'],
    ['/app/nodes', 'machine'],
    ['/app/backends', 'runtime'],
    ['/app/activity', 'runtime'],
    ['/app/failover', 'runtime'],
    ['/app/traffic', 'traffic'],
    ['/app/traffic/models', 'traffic'],
    ['/app/traffic/host', 'traffic'],
    ['/app/traffic/prometheus', 'traffic'],
    ['/app/usage', 'traffic'],
    ['/app/traces', 'traffic'],
    ['/app/traces/t1', 'traffic'],
    ['/app/middleware', 'traffic'],
    ['/app/settings', 'settings'],
  ]
  for (const [path, id] of operate) {
    test(`Operate: ${path} highlights ${id}`, async ({ page }) => {
      await quietOperate(page)
      await page.goto(path)
      await expect(tab(page, id)).toHaveAttribute('aria-current', 'page')
      await expect(page.locator(`${BAR} [aria-current="page"]`)).toHaveCount(1)
    })
  }

  test('Operate: backend log pages keep the Runtime tab', async ({ page }) => {
    await page.goto('/app/backend-logs/some-model')
    await expect(tab(page, 'runtime')).toHaveAttribute('aria-current', 'page')
  })

  test('Swarm owns nodes, placement rules, failover and p2p when distributed mode is on', async ({ page }) => {
    await mockFeatures(page, { distributed: true, agents: true, mcp: true })
    await quietOperate(page)
    for (const path of ['/app/nodes', '/app/nodes/n1', '/app/nodes/add', '/app/scheduling', '/app/failover', '/app/p2p', '/app/node-backend-logs/n1/some-model']) {
      await page.goto(path)
      await expect(tab(page, 'swarm')).toHaveAttribute('aria-current', 'page')
      await expect(tab(page, 'machine')).toHaveCount(0)
    }
  })

  test('the second row marks the current route inside a tab', async ({ page }) => {
    await page.goto('/app/activity')
    const sub = page.locator('.hub-subnav')
    await expect(sub.getByRole('link', { name: 'Activity' })).toHaveAttribute('aria-current', 'page')
    await expect(sub.getByRole('link', { name: 'Backends' })).not.toHaveAttribute('aria-current', 'page')
    await sub.getByRole('link', { name: 'Backends' }).click()
    await expect(page).toHaveURL(/\/app\/backends$/)
    await expect(sub.getByRole('link', { name: 'Backends' })).toHaveAttribute('aria-current', 'page')
  })

  test('a tab link goes to the first route of the tab', async ({ page }) => {
    await page.goto('/app/settings')
    await tab(page, 'traffic').click()
    await expect(page).toHaveURL(/\/app\/traffic$/)
    await expect(tab(page, 'traffic')).toHaveAttribute('aria-current', 'page')
  })
})

test.describe('Hub tabs: gating', () => {
  test('Swarm is hidden on a single node and This machine is shown', async ({ page }) => {
    await mockFeatures(page, { distributed: false })
    await quietOperate(page)
    await page.goto('/app/operate')
    await expect(tab(page, 'machine')).toBeVisible()
    await expect(tab(page, 'swarm')).toHaveCount(0)
  })

  test('Swarm shows with distributed mode on, and the tab opens the Nodes route', async ({ page }) => {
    await mockFeatures(page, { distributed: true })
    await quietOperate(page)
    await page.goto('/app/operate')
    await expect(tab(page, 'swarm')).toHaveAttribute('href', '/app/nodes')
    await expect(tab(page, 'machine')).toHaveCount(0)
  })

  test('feature flags hide the sidebar items they gate', async ({ page }) => {
    await mockFeatures(page, { collections: false, mcp: false, fine_tuning: false, quantization: false, distributed: false })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/collections"]')).toHaveCount(0)
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/agents"]')).toBeVisible()
  })

  test('a user without admin sees Memory with the collections permission and no Import', async ({ page }) => {
    await mockFeatures(page, { collections: true, mcp: true, fine_tuning: true, quantization: true })
    await mockAuth(page, { id: 'u1', name: 'Sam', role: 'user', permissions: { collections: true } })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/collections"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/import-model"]')).toHaveCount(0)
  })

  test('a non-admin user gets no Operate entry in the sidebar', async ({ page }) => {
    await mockAuth(page, { id: 'u1', name: 'Sam', role: 'user', permissions: { collections: true } })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })).toHaveCount(0)
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })).toHaveCount(0)
  })

  test('Users appears in the Settings row only with auth on', async ({ page }) => {
    await mockAuth(page, { id: 'a', name: 'Admin', role: 'admin', provider: 'local' })
    await page.goto('/app/users')
    await expect(tab(page, 'settings')).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.hub-subnav a[href="/app/users"]')).toHaveAttribute('aria-current', 'page')
  })
})

test.describe('Hub tabs: badges', () => {
  test('no badge is drawn on a quiet install', async ({ page }) => {
    await quietOperate(page)
    await page.goto('/app/operate')
    await expect(tab(page, 'status')).toBeVisible()
    await expect(page.locator(`${BAR} .dk-hubtab-attn`)).toHaveCount(0)
  })

  test('failed operations raise the Status attention badge', async ({ page }) => {
    await quietOperate(page)
    await page.route('**/api/operations', route => route.fulfill({
      json: [{ id: 'op-1', name: 'qwen3-8b', type: 'install', error: 'no space left on device' }],
    }))
    await page.goto('/app/operate')
    await expect(tab(page, 'status').locator('.dk-hubtab-attn')).toContainText('1')
  })
})

test.describe('Hub tabs: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('scrolling reaches the last tab and keeps it tappable', async ({ page }) => {
    await quietOperate(page)
    await page.goto('/app/operate')
    const last = tab(page, 'settings')
    await last.scrollIntoViewIfNeeded()
    await expect(last).toBeInViewport()
    await last.click()
    await expect(page).toHaveURL(/\/app\/settings$/)
    await expect(tab(page, 'settings')).toHaveAttribute('aria-current', 'page')
  })

  test('every tab is at least 36px tall', async ({ page }) => {
    await quietOperate(page)
    await page.goto('/app/operate')
    const heights = await page.locator(`${BAR} .dk-hubtab`).evaluateAll(els => els.map(el => el.getBoundingClientRect().height))
    expect(Math.min(...heights)).toBeGreaterThanOrEqual(36)
  })
})

test.describe('Hub tabs: reveal on route', () => {
  test('Swarm does not appear next to This machine on the Nodes route', async ({ page }) => {
    await mockFeatures(page, { distributed: false })
    await quietOperate(page)
    await page.goto('/app/nodes')
    await expect(tab(page, 'machine')).toHaveAttribute('aria-current', 'page')
    await expect(tab(page, 'swarm')).toHaveCount(0)
  })

  test('a direct link to P2P keeps its Swarm tab on a single node', async ({ page }) => {
    await mockFeatures(page, { distributed: false })
    await quietOperate(page)
    await page.goto('/app/p2p')
    await expect(tab(page, 'swarm')).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.hub-subnav a[href="/app/nodes"]')).toHaveCount(0)
  })
})
