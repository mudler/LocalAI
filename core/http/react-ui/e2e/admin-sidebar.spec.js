import { test, expect } from './coverage-fixtures.js'

// Administration has a sidebar of its own. Clicking Administration in the main
// sidebar swaps the list for the Administration one, with a way back; there is
// no tab bar over the pages. The groups and what each viewer sees come from
// hubConfig.js; the group of the current page is always open and Traffic is
// folded until one of its pages is open.

const NAV = '[data-testid="admin-nav"]'
const item = (page, path) => page.locator(`${NAV} a[data-admin-item="${path}"]`)
const current = (page) => page.locator(`${NAV} a[aria-current="page"]`)
// The routes of the items of a group, in order (the names carry the counts).
const paths = (page, id) => page.locator(`${NAV} [data-group="${id}"] a[data-admin-item]`).evaluateAll(els => els.map(el => el.getAttribute('data-admin-item') + (el.getAttribute('href').includes('?') ? `?${el.getAttribute('href').split('?')[1]}` : '')))

async function mockFeatures(page, features) {
  await page.route('**/api/features', route => route.fulfill({ json: features }))
}

async function mockAuth(page, user, authEnabled = true) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled, staticApiKeyRequired: false, providers: ['local'], user },
  }))
}

async function quiet(page) {
  await page.route('**/api/operations', route => route.fulfill({ json: [] }))
  await page.route('**/api/nodes', route => route.fulfill({ json: [{ id: 'n1', name: 'atlas', status: 'healthy', healthy: true }] }))
}

test.describe('Administration sidebar: the swap', () => {
  test('the Administration entry swaps the sidebar, and the back link returns', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/models"]')).toBeVisible()
    await page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' }).click()
    await expect(page).toHaveURL(/\/app\/operate$/)
    await expect(page.locator(NAV)).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/models"]')).toHaveCount(0)
    await expect(item(page, '/app/operate')).toHaveAttribute('aria-current', 'page')
    await page.getByRole('link', { name: /Back to LocalAI/ }).click()
    await expect(page).toHaveURL(/\/app$/)
    await expect(page.locator(NAV)).toHaveCount(0)
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/models"]')).toBeVisible()
  })

  test('there is no tab bar and no second row on any Administration page', async ({ page }) => {
    await quiet(page)
    for (const path of ['/app/operate', '/app/activity', '/app/traffic/models']) {
      await page.goto(path)
      await expect(page.locator(NAV)).toBeVisible()
      await expect(page.locator('.dk-hubtabs')).toHaveCount(0)
      await expect(page.locator('.hub-subnav')).toHaveCount(0)
    }
  })

  test('a non-admin user gets no Administration entry and no admin sidebar', async ({ page }) => {
    await mockAuth(page, { id: 'u1', name: 'Sam', role: 'user', permissions: { collections: true } })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' })).toHaveCount(0)
    await expect(page.locator(NAV)).toHaveCount(0)
  })
})

test.describe('Administration sidebar: the current item', () => {
  const single = [
    ['/app/operate', '/app/operate'],
    ['/app/nodes', '/app/nodes'],
    ['/app/backends', '/app/backends'],
    ['/app/activity', '/app/activity'],
    ['/app/backend-logs/some-model', '/app/backend-logs'],
    ['/app/failover', '/app/failover'],
    ['/app/updates', '/app/updates'],
    ['/app/traffic', '/app/traffic'],
    ['/app/usage', '/app/usage'],
    ['/app/traffic/models', '/app/traffic/models'],
    ['/app/traffic/host', '/app/traffic/host'],
    ['/app/traces', '/app/traces'],
    ['/app/traces/t1', '/app/traces'],
    ['/app/middleware', '/app/middleware'],
    ['/app/traffic/prometheus', '/app/traffic/prometheus'],
    ['/app/settings', '/app/settings'],
  ]
  for (const [path, lit] of single) {
    test(`${path} lights ${lit}`, async ({ page }) => {
      await mockFeatures(page, { distributed: false, p2p: false, chats: true })
      await quiet(page)
      await page.goto(path)
      await expect(current(page)).toHaveCount(1)
      await expect(current(page)).toHaveAttribute('data-admin-item', lit)
    })
  }

  for (const path of ['/app/nodes', '/app/nodes/n1', '/app/nodes/add', '/app/scheduling', '/app/scheduling/failover', '/app/failover', '/app/p2p', '/app/p2p/settings']) {
    test(`on a cluster ${path} is in the Cluster group`, async ({ page }) => {
      await mockFeatures(page, { distributed: true, agents: true, mcp: true })
      await quiet(page)
      await page.goto(path)
      await expect(page.locator(`${NAV} [data-group="cluster"]`)).toBeVisible()
      await expect(page.locator(`${NAV} [data-group="machine"]`)).toHaveCount(0)
      await expect(page.locator(`${NAV} [data-group="cluster"] a[aria-current="page"]`)).toHaveCount(1)
    })
  }

  test('the second Administration page of a group is reached with one click', async ({ page }) => {
    await mockFeatures(page, { distributed: false })
    await quiet(page)
    await page.goto('/app/activity')
    await item(page, '/app/backends').click()
    await expect(page).toHaveURL(/\/app\/backends$/)
    await expect(item(page, '/app/backends')).toHaveAttribute('aria-current', 'page')
    await expect(item(page, '/app/activity')).not.toHaveAttribute('aria-current', 'page')
  })
})

test.describe('Administration sidebar: groups and gating', () => {
  const groups = (page) => page.locator(`${NAV} [data-group]`)

  test('a single node: This machine, no Cluster group, Failover with the runtime pages', async ({ page }) => {
    await mockFeatures(page, { distributed: false, p2p: false })
    await quiet(page)
    await page.goto('/app/operate')
    await expect(page.locator(`${NAV} [data-group="machine"]`)).toBeVisible()
    await expect(page.locator(`${NAV} [data-group="cluster"]`)).toHaveCount(0)
    await expect(item(page, '/app/nodes')).toContainText('This machine')
    await expect(page.locator(`${NAV} [data-group="runtime"] a[data-admin-item="/app/failover"]`)).toBeVisible()
  })

  test('a cluster: Workers, Placement and Sharing; Failover is a Placement view', async ({ page }) => {
    await mockFeatures(page, { distributed: true })
    await quiet(page)
    await page.goto('/app/operate')
    await expect(item(page, '/app/scheduling')).toBeVisible()
    expect(await paths(page, 'cluster')).toEqual(['/app/nodes', '/app/scheduling', '/app/p2p'])
    await expect(item(page, '/app/nodes')).toContainText('Workers')
    await expect(item(page, '/app/scheduling')).toContainText('Placement')
    await expect(item(page, '/app/p2p')).toContainText('Sharing')
    await expect(page.locator(`${NAV} [data-group="machine"]`)).toHaveCount(0)
    await expect(page.locator(`${NAV} [data-group="runtime"] a[data-admin-item="/app/failover"]`)).toHaveCount(0)
  })

  test('a single node with P2P on shows Sharing under Cluster and keeps This machine', async ({ page }) => {
    await mockFeatures(page, { distributed: false, p2p: true })
    await quiet(page)
    await page.goto('/app/operate')
    await expect(item(page, '/app/p2p')).toBeVisible()
    expect(await paths(page, 'cluster')).toEqual(['/app/p2p'])
    await expect(item(page, '/app/nodes')).toContainText('This machine')
  })

  test('the groups of a full install, in order', async ({ page }) => {
    await mockFeatures(page, { distributed: true, chats: true })
    await mockAuth(page, { id: 'a', name: 'Admin', role: 'admin', provider: 'local' })
    await quiet(page)
    await page.goto('/app/operate')
    await expect(page.locator(`${NAV} [data-group="settings"]`)).toBeVisible()
    const ids = await groups(page).evaluateAll(els => els.map(el => el.getAttribute('data-group')).filter(Boolean))
    expect(ids).toEqual(['landing', 'cluster', 'runtime', 'traffic', 'access', 'settings'])
    expect(await paths(page, 'access')).toEqual(['/app/users', '/app/api-keys', '/app/chats-admin'])
    await expect(page.getByRole('link', { name: 'API reference' })).toHaveAttribute('href', /swagger/)
  })

  test('without sign-in Users and API keys are not drawn; without chats neither is Chats of all users', async ({ page }) => {
    await mockFeatures(page, { chats: false })
    await quiet(page)
    await page.goto('/app/operate')
    await expect(page.locator(`${NAV} [data-group="access"]`)).toHaveCount(0)
    await expect(item(page, '/app/users')).toHaveCount(0)
  })

  test('API keys and Users are two pages of the Access group', async ({ page }) => {
    await mockAuth(page, { id: 'a', name: 'Admin', role: 'admin', provider: 'local' })
    await quiet(page)
    await page.goto('/app/users')
    const users = item(page, '/app/users')
    const keys = item(page, '/app/api-keys')
    await expect(users).toHaveAttribute('aria-current', 'page')
    await keys.click()
    await expect(page).toHaveURL(/\/app\/api-keys$/)
    await expect(keys).toHaveAttribute('aria-current', 'page')
    await expect(users).not.toHaveAttribute('aria-current', 'page')
  })

  test('Traffic is folded by default, opens by hand and stays open on one of its pages', async ({ page }) => {
    await quiet(page)
    await page.goto('/app/operate')
    const traffic = page.locator(`${NAV} [data-group="traffic"]`)
    const toggle = traffic.getByRole('button', { name: /Traffic/ })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(item(page, '/app/usage')).toBeHidden()
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(traffic.getByRole('link')).toHaveCount(7)
    await item(page, '/app/usage').click()
    await expect(page).toHaveURL(/\/app\/usage$/)
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    // The group of the current page cannot be folded away.
    await expect(toggle).toBeDisabled()
    await page.goto('/app/traces')
    await expect(page.locator(`${NAV} [data-group="traffic"]`).getByRole('button', { name: /Traffic/ })).toHaveAttribute('aria-expanded', 'true')
  })

  test('another group is open whatever the folded state of Traffic', async ({ page }) => {
    await quiet(page)
    await page.goto('/app/activity')
    await expect(page.locator(`${NAV} [data-group="runtime"]`).getByRole('link', { name: 'Backends' })).toBeVisible()
    await expect(page.locator(`${NAV} [data-group="traffic"]`).getByRole('button', { name: /Traffic/ })).toHaveAttribute('aria-expanded', 'false')
  })
})

test.describe('Administration sidebar: counts', () => {
  test('a quiet install draws no count', async ({ page }) => {
    await quiet(page)
    await page.goto('/app/operate')
    await expect(item(page, '/app/operate')).toBeVisible()
    await expect(page.locator(`${NAV} [data-testid="admin-signal-attention"]`)).toHaveCount(0)
  })

  test('a failed operation raises the Overview count', async ({ page }) => {
    await quiet(page)
    await page.route('**/api/operations', route => route.fulfill({
      json: [{ id: 'op-1', name: 'qwen3-8b', type: 'install', error: 'no space left on device' }],
    }))
    await page.goto('/app/operate')
    await expect(item(page, '/app/operate').locator('[data-testid="admin-signal-attention"]')).toContainText('1')
  })

  test('sign-ups waiting for approval show on Users', async ({ page }) => {
    await mockAuth(page, { id: 'a', name: 'Admin', role: 'admin', provider: 'local' })
    await page.route('**/api/auth/admin/users', route => route.fulfill({ json: [
      { id: 'a', name: 'Admin', email: 'a@example.com', role: 'admin', status: 'active' },
      { id: 'b', name: 'Eli', email: 'eli@example.com', role: 'user', status: 'pending' },
    ] }))
    await quiet(page)
    await page.goto('/app/operate')
    await expect(item(page, '/app/users').locator('[data-testid="admin-signal-pendingUsers"]')).toContainText('1')
  })
})

test.describe('Administration: Placement and the old failover address', () => {
  test('on a cluster the failover address opens the Failover view of Placement', async ({ page }) => {
    await mockFeatures(page, { distributed: true })
    await quiet(page)
    await page.goto('/app/failover?x=1')
    await expect(page).toHaveURL(/\/app\/scheduling\/failover\?x=1$/)
    await expect(page.getByTestId('placement-view-failover')).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('failover-overview')).toBeVisible()
    await page.getByTestId('placement-view-rules').click()
    await expect(page).toHaveURL(/\/app\/scheduling$/)
    await expect(page.getByTestId('placement-view-rules')).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('placement-rules')).toBeVisible()
    await expect(page.getByRole('heading', { level: 1, name: 'Placement' })).toBeVisible()
  })

  test('a single node keeps Failover as a page of its own', async ({ page }) => {
    await mockFeatures(page, { distributed: false })
    await quiet(page)
    await page.goto('/app/failover')
    await expect(page).toHaveURL(/\/app\/failover$/)
    await expect(page.getByTestId('failover-overview')).toBeVisible()
    await expect(page.getByTestId('placement-views')).toHaveCount(0)
  })
})

test.describe('Administration: breadcrumb', () => {
  test('the page header carries Administration / group / page', async ({ page }) => {
    await mockFeatures(page, { distributed: false })
    await quiet(page)
    await page.goto('/app/backends')
    const crumbs = page.getByTestId('admin-crumbs')
    await expect(crumbs).toHaveText(/Administration\s*\/?\s*Models and runtime\s*\/?\s*Backends/)
    await expect(crumbs.getByRole('link', { name: 'Administration' })).toHaveAttribute('href', '/app/operate')
    await expect(crumbs.locator('[aria-current="page"]')).toHaveText('Backends')
  })
})

test.describe('Administration sidebar: phone and tablet', () => {
  test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 } })

    test('the drawer shows the Administration sidebar, and a tap closes it', async ({ page }) => {
      await quiet(page)
      await page.goto('/app/operate')
      await page.getByRole('button', { name: 'Open menu' }).click()
      await expect(page.locator(NAV)).toBeVisible()
      await expect(page.getByRole('link', { name: /Back to LocalAI/ })).toBeVisible()
      await item(page, '/app/backends').click()
      await expect(page).toHaveURL(/\/app\/backends$/)
      await expect(page.locator('#app-sidebar')).not.toHaveClass(/\bopen\b/)
    })

    for (const path of ['/app/operate', '/app/activity', '/app/settings', '/app/traffic']) {
      test(`${path} has no horizontal scroll`, async ({ page }) => {
        await quiet(page)
        await page.goto(path)
        await expect(page.locator('h1').first()).toBeVisible()
        const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
        expect(over).toBeLessThanOrEqual(0)
      })
    }
  })

  test.describe('tablet', () => {
    test.use({ viewport: { width: 820, height: 900 } })

    test('the icon rail lists every page, the folded group included', async ({ page }) => {
      await quiet(page)
      await page.goto('/app/operate')
      await expect(item(page, '/app/backends')).toBeVisible()
      await expect(item(page, '/app/usage')).toBeVisible()
      await expect(page.locator(`${NAV} .nav-label`).first()).toBeHidden()
    })
  })
})
