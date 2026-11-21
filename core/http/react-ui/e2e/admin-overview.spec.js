import { test, expect } from './coverage-fixtures.js'
import { FAILED_OP, NODES, TRACES_ERRORS, UPGRADE_LLAMA, CATALOG, gpuHost, mockOperate } from './operate-fixtures.js'

// The Administration overview: "Needs you" first, each row with the one click
// that deals with it, then the cluster (or this machine), traffic and access
// cards. Every number comes from calls the page already made.

const ITEM = '[data-testid="operate-attention-item"]'
const headline = page => page.getByTestId('operate-headline')

const ADMIN = { id: 'a', name: 'Admin', email: 'admin@example.com', role: 'admin', provider: 'local', status: 'active' }
const PENDING = [
  { id: 'p1', name: 'Eli Katz', email: 'eli@example.com', role: 'user', status: 'pending' },
  { id: 'p2', name: 'Gus Lindqvist', email: 'gus@example.com', role: 'user', status: 'pending' },
]

async function withUsers(page, users) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: ADMIN, registrationMode: 'approval' },
  }))
  const state = { users: users.map(u => ({ ...u })), calls: [] }
  await page.route('**/api/auth/admin/users', route => route.fulfill({ json: state.users }))
  await page.route('**/api/auth/admin/users/*/status', route => {
    const id = decodeURIComponent(route.request().url().split('/users/')[1].split('/')[0])
    const body = route.request().postDataJSON()
    state.calls.push({ id, status: body.status })
    state.users = state.users.map(u => (u.id === id ? { ...u, status: body.status } : u))
    return route.fulfill({ json: { ok: true } })
  })
  return state
}

test.describe('Overview: the sentence and Needs you', () => {
  test('a quiet single node says so, names its scope and shows no row', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/operate')
    await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible()
    await expect(headline(page)).toContainText('Single node')
    await expect(headline(page)).toContainText('Everything is running.')
    await expect(page.getByTestId('operate-attention-clear')).toBeVisible()
    await expect(page.locator(ITEM)).toHaveCount(0)
  })

  test('a cluster names the worker that is down, with a link that opens it', async ({ page }) => {
    await mockOperate(page, {
      distributed: true,
      nodes: NODES.map(n => (n.id === 'n3' ? { ...n, status: 'unhealthy', healthy: false } : n)),
    })
    await page.goto('/app/operate')
    await expect(headline(page)).toContainText('Cluster, 4 nodes')
    await expect(headline(page)).toContainText('1 thing needs you.')
    const item = page.locator(ITEM)
    await expect(item).toContainText('n3 is not answering')
    await item.getByRole('link', { name: 'Open worker' }).click()
    await expect(page).toHaveURL(/\/app\/nodes\/n3$/)
  })

  test('rows come in the order workers, operations, sign-ups, updates', async ({ page }) => {
    await mockOperate(page, {
      distributed: true,
      nodes: NODES.map(n => (n.id === 'n3' ? { ...n, status: 'unhealthy', healthy: false } : n)),
      operations: [FAILED_OP],
      upgrades: UPGRADE_LLAMA,
    })
    await withUsers(page, [ADMIN, ...PENDING])
    await page.goto('/app/operate')
    const kinds = page.locator(ITEM)
    await expect(kinds).toHaveCount(4)
    const order = await kinds.evaluateAll(els => els.map(el => el.getAttribute('data-kind')))
    expect(order).toEqual(['node-unhealthy', 'operation-failed', 'signups-waiting', 'backend-update'])
    await expect(page.getByTestId('operate-needs-count')).toHaveText('4')
  })

  test('Retry installs the failed model again, after moving the failure to the record', async ({ page }) => {
    await mockOperate(page, { operations: [FAILED_OP] })
    const calls = []
    await page.route('**/api/operations/job-gemma27/dismiss', route => { calls.push('dismiss'); return route.fulfill({ json: {} }) })
    await page.route('**/api/models/install/**', route => { calls.push(new URL(route.request().url()).pathname); return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')
    const item = page.locator(ITEM)
    await expect(item).toContainText('gemma-3-27b did not finish')
    await expect(item).toContainText('Connection reset by the registry')
    await item.getByRole('button', { name: 'Retry' }).click()
    await expect.poll(() => calls).toEqual(['dismiss', '/api/models/install/localai@gemma-3-27b'])
  })

  test('Dismiss moves a failure out of the list', async ({ page }) => {
    await mockOperate(page, { operations: [FAILED_OP] })
    const calls = []
    await page.route('**/api/operations/job-gemma27/dismiss', route => { calls.push('dismiss'); return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')
    await page.locator(ITEM).getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(() => calls).toEqual(['dismiss'])
  })

  test('the update row opens the Updates page and starts nothing', async ({ page }) => {
    await mockOperate(page, { upgrades: UPGRADE_LLAMA })
    const calls = []
    await page.route('**/api/backends/upgrade/llama-cpp', route => { calls.push(route.request().method()); return route.fulfill({ json: { status: 'ok' } }) })
    await page.goto('/app/operate')
    await page.locator(ITEM).getByRole('link', { name: 'Review' }).click()
    await expect(page).toHaveURL(/\/app\/updates\?filter=ready/)
    expect(calls).toEqual([])
  })

  test('sign-ups waiting for approval are one row, approved with one click', async ({ page }) => {
    await mockOperate(page)
    const state = await withUsers(page, [ADMIN, ...PENDING])
    await page.goto('/app/operate')
    const row = page.locator(ITEM, { hasText: 'sign-ups wait for approval' })
    await expect(row).toContainText('2 sign-ups wait for approval')
    await expect(row).toContainText('Eli Katz, Gus Lindqvist')
    await expect(row.getByRole('link', { name: 'Review' })).toHaveAttribute('href', '/app/users')
    await row.getByTestId('overview-approve').click()
    await expect.poll(() => state.calls.map(c => `${c.id}:${c.status}`).sort()).toEqual(['p1:active', 'p2:active'])
    await expect(page.getByText('Approved 2 sign-ups')).toBeVisible()
    // The row goes with the next reading, and so does the count in the sidebar.
    await expect(page.locator(ITEM, { hasText: 'sign-ups' })).toHaveCount(0)
  })

  test('one sign-up says Approve, and a failed approval says why and keeps the row', async ({ page }) => {
    await mockOperate(page)
    await withUsers(page, [ADMIN, PENDING[0]])
    await page.route('**/api/auth/admin/users/*/status', route => route.fulfill({ status: 500, json: { error: 'boom' } }))
    await page.goto('/app/operate')
    const row = page.locator(ITEM, { hasText: 'sign-up waits' })
    await row.getByRole('button', { name: 'Approve', exact: true }).click()
    await expect(page.getByText(/Could not approve/)).toBeVisible()
    await expect(row).toBeVisible()
  })

  test('without sign-in there is no sign-up row and no Access card', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/operate')
    await expect(page.locator(ITEM, { hasText: 'sign-up' })).toHaveCount(0)
    await expect(page.getByTestId('overview-access')).toHaveCount(0)
  })

  test('a first run offers a backend and the model list instead of a report', async ({ page }) => {
    await mockOperate(page, {
      catalog: CATALOG.map(b => ({ ...b, installed: false })),
      installed: [],
      models: { data: [] },
      loaded: [],
      resources: gpuHost({ usedGB: 0.4 }),
    })
    await page.goto('/app/operate')
    await expect(headline(page)).toContainText('Nothing is running yet.')
    await expect(page.getByTestId('operate-first-run').getByRole('link', { name: 'Install a backend' })).toHaveAttribute('href', '/app/backends?view=catalog')
    await expect(page.getByTestId('operate-first-run').getByRole('link', { name: 'Browse models' })).toHaveAttribute('href', '/app/models')
    await expect(page.getByTestId('operate-needs')).toHaveCount(0)
  })
})

test.describe('Overview: the cards', () => {
  test('a cluster card counts the workers and draws a meter for each', async ({ page }) => {
    await mockOperate(page, {
      distributed: true,
      nodes: NODES.map(n => (n.id === 'n3' ? { ...n, status: 'unhealthy', healthy: false, model_count: 2, in_flight_count: 1 } : { ...n, model_count: 1, in_flight_count: 0 })),
    })
    await page.goto('/app/operate')
    const card = page.getByTestId('overview-cluster')
    await expect(card.getByRole('heading', { name: 'Cluster' })).toBeVisible()
    await expect(card).toContainText('3')
    await expect(card).toContainText('of 4')
    await expect(card.getByRole('img', { name: /gpu-box-1: 18.0 GB of 24.0 GB in use/ })).toBeVisible()
    await expect(card.getByRole('link', { name: 'Open workers' })).toHaveAttribute('href', '/app/nodes')
  })

  test('a single node card is This machine, with the memory and the disk', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/operate')
    const card = page.getByTestId('overview-cluster')
    await expect(card.getByRole('heading', { name: 'This machine' })).toBeVisible()
    await expect(card).toContainText('Loaded')
    await expect(card).toContainText('75%')
    await expect(card).toContainText('588 GB free of 1000 GB')
    await expect(card.getByRole('link', { name: 'Open This machine' })).toHaveAttribute('href', '/app/nodes')
  })

  test('the traffic card counts requests, the failed share and the p95, from the counted summary', async ({ page }) => {
    let listCalls = 0
    await page.route('**/api/traces?**', route => { listCalls += 1; route.fulfill({ json: [] }) })
    await mockOperate(page, { traces: { ...TRACES_ERRORS, buckets: Array.from({ length: 12 }, (_, i) => ({ start: `b${i}`, count: 100 + i * 10, errors: i === 5 ? 3 : 0 })) } })
    await page.goto('/app/operate')
    const card = page.getByTestId('overview-traffic')
    await expect(card.getByTestId('operate-traffic')).toContainText('18,402')
    await expect(card.getByTestId('operate-traffic')).toContainText('0.2%')
    await expect(card.getByTestId('operate-traffic')).toContainText('842')
    await expect(card.getByRole('img', { name: /last 24 hours/ })).toBeVisible()
    await expect(card.getByRole('link', { name: 'Open usage' })).toHaveAttribute('href', '/app/usage')
    expect(listCalls).toBe(0)
  })

  test('a host that recorded nothing says so in the traffic card', async ({ page }) => {
    await mockOperate(page, { traces: { total: 0, errors: 0, p95_ms: 0, window_hours: 24, buckets: [] } })
    await page.goto('/app/operate')
    await expect(page.getByTestId('overview-traffic')).toContainText('No requests recorded in the last 24 hours.')
  })

  test('the access card counts people, waiting sign-ups and admins when sign-in is on', async ({ page }) => {
    await mockOperate(page)
    await withUsers(page, [ADMIN, { id: 'u', name: 'Ben', email: 'b@example.com', role: 'user', status: 'active' }, ...PENDING])
    await page.goto('/app/operate')
    const card = page.getByTestId('overview-access')
    await expect(card).toContainText('Users')
    await expect(card.locator('.ov-stat', { hasText: 'Users' })).toContainText('4')
    await expect(card.locator('.ov-stat', { hasText: 'Waiting' })).toContainText('2')
    await expect(card.locator('.ov-stat', { hasText: 'Admins' })).toContainText('1')
    await expect(card.getByRole('link', { name: 'Open users' })).toHaveAttribute('href', '/app/users')
  })

  test('the page asks nothing away from Administration', async ({ page }) => {
    let calls = 0
    await page.route('**/api/traces/summary', route => { calls += 1; route.fulfill({ json: {} }) })
    await page.route('**/api/operations', route => route.fulfill({ json: [] }))
    await page.goto('/app/chats')
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.waitForTimeout(1500)
    expect(calls).toBe(0)
  })
})

test.describe('Overview: dark theme and phone', () => {
  test('dark: the row icons and the counts keep their colours', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'dark'))
    await mockOperate(page, { operations: [FAILED_OP] })
    await page.goto('/app/operate')
    await expect(page.locator(ITEM)).toBeVisible()
    const colours = await page.locator('.ov-row__text strong, .ov-card__title').evaluateAll(els => els.map(el => getComputedStyle(el).color))
    expect(colours.length).toBeGreaterThan(1)
    expect(colours.filter(c => c === 'rgb(0, 0, 0)')).toEqual([])
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 } })

    test('a row stacks its buttons under the words and nothing scrolls sideways', async ({ page }) => {
      await mockOperate(page, { operations: [FAILED_OP], upgrades: UPGRADE_LLAMA })
      await page.goto('/app/operate')
      await expect(page.locator(ITEM).first()).toBeVisible()
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(over).toBeLessThanOrEqual(0)
      const row = await page.locator(ITEM).first().boundingBox()
      expect(row.x + row.width).toBeLessThanOrEqual(390)
    })
  })
})
