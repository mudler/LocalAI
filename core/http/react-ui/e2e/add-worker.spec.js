import { test, expect } from './coverage-fixtures.js'
import { clusterNodes, clusterSettingsFixture, mintedToken, mockSwarm } from './swarm-fixtures.js'
import { mockP2PShare, DEVICE_PEER } from './p2p-share-fixtures.js'

// "Add worker": choose the connections, run the command with a join token that
// the server mints, and wait for the worker. Every call is stubbed: the token
// and the commands are the ones the server would return for the routes the
// page asked for.

const mints = log => log.filter(entry => entry.method === 'POST' && entry.path === '/api/cluster/join-tokens').map(entry => entry.body)
const revokes = log => log.filter(entry => entry.method === 'DELETE' && entry.path.startsWith('/api/cluster/join-tokens/')).map(entry => entry.path)

async function open(page, { mode = 'share', cluster, nodes, log = [], onMint, onNodes } = {}) {
  await mockSwarm(page, { log, cluster, nodes, onMint, onNodes })
  const share = await mockP2PShare(page, { mode })
  await page.goto('/app/nodes/add')
  await expect(page.getByTestId('add-step-choose')).toBeVisible({ timeout: 15_000 })
  return { log, share }
}

const way = (page, name) => page.getByTestId(`way-${name}`)
const toStep2 = page => page.getByTestId('add-continue').click()

test.describe('Add worker: choose the connections', () => {
  test('offers NATS, WebSocket and P2P, with NATS and WebSocket chosen, and a way to the model-split worker', async ({ page }) => {
    await open(page)
    await expect(page.getByRole('heading', { name: 'Add worker', level: 1 })).toBeVisible()
    await expect(page.getByRole('list', { name: 'Steps' }).getByRole('listitem')).toHaveCount(3)
    await expect(way(page, 'nats').getByRole('checkbox')).toBeChecked()
    await expect(way(page, 'websocket').getByRole('checkbox')).toBeChecked()
    await expect(way(page, 'p2p').getByRole('checkbox')).not.toBeChecked()
    await expect(way(page, 'nats')).toContainText('Available, NATS is connected')
    await expect(way(page, 'nats')).toContainText('Preference weight 100')
    await expect(way(page, 'websocket')).toContainText('Preference weight 50')
    await expect(way(page, 'p2p')).toContainText('Preference weight 25')
    await expect(page.getByTestId('add-split-way')).toBeVisible()
  })

  test('NATS is disabled with its reason and a link when NATS is not set up', async ({ page }) => {
    await open(page, { cluster: clusterSettingsFixture({ nats_url: '', bus: { bus: 'postgres', nats_configured: false, nats_connected: false } }) })
    const card = way(page, 'nats')
    await expect(card.getByRole('checkbox')).toBeDisabled()
    await expect(card.getByRole('checkbox')).not.toBeChecked()
    await expect(card).toContainText('Unavailable')
    await expect(page.getByTestId('way-nats-why').getByRole('link', { name: 'Set it up in the cluster settings' })).toHaveAttribute('href', '/app/settings?group=cluster')
    await expect(way(page, 'websocket').getByRole('checkbox')).toBeChecked()
  })

  test('P2P is disabled with a link when sharing is off, and chosen when sharing is on', async ({ page }) => {
    await open(page, { mode: 'connect' })
    await expect(way(page, 'p2p').getByRole('checkbox')).toBeDisabled()
    await expect(page.getByTestId('way-p2p-why').getByRole('link', { name: 'Turn it on in Sharing' })).toHaveAttribute('href', '/app/p2p/settings')
  })

  test('P2P can be chosen when sharing is on, and Continue needs at least one connection', async ({ page }) => {
    await open(page)
    await way(page, 'p2p').getByRole('checkbox').check()
    await expect(way(page, 'p2p').getByRole('checkbox')).toBeChecked()
    await way(page, 'p2p').getByRole('checkbox').uncheck()
    await way(page, 'nats').getByRole('checkbox').uncheck()
    await way(page, 'websocket').getByRole('checkbox').uncheck()
    await expect(page.getByTestId('add-continue')).toBeDisabled()
    await expect(page.getByText('Pick at least one connection.')).toBeVisible()
  })
})

test.describe('Add worker: the join token and the commands', () => {
  test('mints a token for NATS and WebSocket, hides it, and shows the server commands in five tabs', async ({ page }) => {
    const { log } = await open(page)
    await toStep2(page)
    await expect(page.getByTestId('join-token')).toBeVisible()
    expect(mints(log)).toEqual([{ ttl_seconds: 600, uses: 1, routes: ['nats', 'websocket'] }])
    await expect(page.getByTestId('add-summary')).toContainText('This worker will attach over NATS and WebSocket')
    await expect(page.getByTestId('add-summary')).toContainText('weights 100 and 50')
    const tabs = page.getByRole('tablist', { name: 'Ways to run the worker' }).getByRole('tab')
    await expect(tabs).toHaveText(['Install script', 'Docker', 'Docker Compose', 'Kubernetes', 'Manual'])
    // The token is not on the page in full: masked in its box and in the command.
    await expect(page.getByTestId('token-text')).toHaveText(/^lai_join_01S•+$/)
    await expect(page.locator('body')).not.toContainText('SECRETSECRETSECRET')
    const block = page.getByTestId('command-block')
    await expect(block).toContainText('curl -fsSL')
    await expect(block).toContainText('routes=nats,websocket')
    await expect(block).not.toContainText('lai_join_01')
    await page.getByTestId('token-reveal').click()
    await expect(page.getByTestId('token-text')).toContainText('lai_join_01SECRETSECRETSECRET')
    await expect(block).toContainText('lai_join_01SECRETSECRETSECRET')
    await page.getByTestId('token-reveal').click()
    await expect(page.locator('body')).not.toContainText('SECRETSECRETSECRET')
    // Windows is a choice of the install script.
    await page.getByRole('radio', { name: 'Windows' }).click()
    await expect(block).toContainText('irm ')
    await expect(block).toContainText('join.ps1')
    await page.getByTestId('tab-docker').click()
    await expect(block).toContainText('docker run')
    await expect(block).toContainText("LOCALAI_WORKER_ROUTES='nats,tunnel'")
    await page.getByTestId('tab-compose').click()
    await expect(block).toContainText('LOCALAI_JOIN_TOKEN')
    await page.getByTestId('tab-kubernetes').click()
    await expect(block).toContainText('kubectl apply -f')
    await page.getByTestId('tab-manual').click()
    await expect(block).toContainText('local-ai worker')
  })

  test('copies the whole command, token included', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => {})
    await open(page)
    await toStep2(page)
    await page.getByTestId('tab-manual').click()
    const copy = page.getByTestId('command-block').getByRole('button', { name: 'Copy command' })
    await copy.click()
    await expect(copy).toContainText('Copied')
    const copied = await page.evaluate(() => navigator.clipboard.readText()).catch(() => null)
    if (copied !== null) expect(copied).toContain('LOCALAI_JOIN_TOKEN=\'lai_join_01SECRETSECRETSECRET\'')
  })

  test('the commands follow the connections that were chosen', async ({ page }) => {
    const { log } = await open(page)
    await way(page, 'nats').getByRole('checkbox').uncheck()
    await toStep2(page)
    await expect(page.getByTestId('add-summary')).toContainText('This worker will attach over WebSocket')
    await expect(page.getByTestId('command-block')).toContainText('routes=websocket')
    expect(mints(log)).toEqual([{ ttl_seconds: 600, uses: 1, routes: ['websocket'] }])
    // Going back and choosing both again mints for both, and ends the first token.
    await page.getByTestId('add-change').click()
    await way(page, 'nats').getByRole('checkbox').check()
    await toStep2(page)
    await expect.poll(() => mints(log).length).toBe(2)
    expect(mints(log)[1].routes).toEqual(['nats', 'websocket'])
    await expect.poll(() => revokes(log)).toEqual(['/api/cluster/join-tokens/jt-1'])
  })

  test('P2P alone has no token: it shows an invite and its command', async ({ page }) => {
    const { log, share } = await open(page)
    await way(page, 'nats').getByRole('checkbox').uncheck()
    await way(page, 'websocket').getByRole('checkbox').uncheck()
    await way(page, 'p2p').getByRole('checkbox').check()
    await toStep2(page)
    const invite = page.getByTestId('invite-panel')
    await expect(invite.getByTestId('worker-invite')).toContainText('Invite ready')
    await expect(invite.getByTestId('worker-invite')).toContainText('Single use')
    await expect(invite.getByTestId('command-block')).toContainText('LOCALAI_JOIN="localai://pair?v=1&k=worker')
    await expect(page.getByTestId('join-token')).toHaveCount(0)
    expect(mints(log)).toEqual([])
    expect(share.calls.find(call => call.method === 'POST').body).toEqual({ kind: 'worker' })
    await invite.getByRole('radio', { name: 'Command line' }).click()
    await expect(invite.getByTestId('command-block')).toContainText('--join "localai://pair?v=1&k=worker')
    await expect(page.getByTestId('add-summary')).toContainText('This worker will attach over P2P')
  })

  test('NATS with P2P shows both: the token commands and the invite', async ({ page }) => {
    const { log } = await open(page)
    await way(page, 'websocket').getByRole('checkbox').uncheck()
    await way(page, 'p2p').getByRole('checkbox').check()
    await toStep2(page)
    await expect(page.getByTestId('command-tabs')).toBeVisible()
    await expect(page.getByTestId('invite-panel').getByTestId('worker-invite')).toBeVisible()
    expect(mints(log)).toEqual([{ ttl_seconds: 600, uses: 1, routes: ['nats'] }])
    await expect(page.getByTestId('add-summary')).toContainText('NATS and P2P')
  })

  test('a token for one worker, or for several, and its lifetime', async ({ page }) => {
    const { log } = await open(page)
    await toStep2(page)
    await expect(page.getByTestId('join-token')).toBeVisible()
    await page.getByRole('radio', { name: 'Several' }).click()
    await expect.poll(() => mints(log).length).toBe(2)
    expect(mints(log)[1].uses).toBe(5)
    const count = page.getByTestId('token-uses')
    await count.fill('12')
    await count.press('Enter')
    await expect.poll(() => mints(log).at(-1).uses).toBe(12)
    // A number outside the limits is clamped to them.
    await count.fill('900')
    await count.blur()
    await expect(count).toHaveValue('100')
    await expect.poll(() => mints(log).at(-1).uses).toBe(100)
    await page.getByTestId('token-ttl').selectOption('86400')
    await expect.poll(() => mints(log).at(-1)).toEqual({ ttl_seconds: 86400, uses: 100, routes: ['nats', 'websocket'] })
    await page.getByRole('radio', { name: '1 worker' }).click()
    await expect.poll(() => mints(log).at(-1).uses).toBe(1)
    // Every token that was replaced was revoked.
    await expect.poll(() => revokes(log).length).toBe(mints(log).length - 1)
  })

  test('counts down to the expiry, and an expired token says so and is minted again', async ({ page }) => {
    const { log } = await open(page, { onMint: (body, index) => {
      const token = mintedToken(body, index)
      // The first token lasts 2 seconds.
      return index === 1 ? { ...token, expires_at: new Date(Date.now() + 2500).toISOString() } : token
    } })
    await toStep2(page)
    await expect(page.getByTestId('token-countdown')).toHaveText(/^0:0[0-3]$/)
    await expect(page.getByTestId('token-expired')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('token-expired')).toContainText('This token has expired')
    await expect(page.getByTestId('command-block').getByRole('button', { name: 'Copy command' })).toBeDisabled()
    await page.getByTestId('token-expired').getByRole('button', { name: 'Mint a new one' }).click()
    await expect(page.getByTestId('token-expired')).toHaveCount(0)
    await expect(page.getByTestId('token-countdown')).toHaveText(/^9:5\d$/)
    expect(mints(log)).toHaveLength(2)
  })

  test('says why a token was not made and tries again', async ({ page }) => {
    let fail = true
    const { log } = await open(page, { onMint: (body, index) => (fail ? { status: 503, error: 'distributed mode is not enabled on this frontend' } : mintedToken(body, index)) })
    await toStep2(page)
    await expect(page.getByTestId('mint-error')).toContainText('distributed mode is not enabled')
    fail = false
    await page.getByTestId('mint-error').getByRole('button', { name: 'Try again' }).click()
    await expect(page.getByTestId('join-token')).toBeVisible()
    expect(mints(log)).toHaveLength(2)
  })
})

test.describe('Add worker: wait for the worker', () => {
  const arrival = (over = {}) => ({
    id: 'n-new', name: 'gpu-box-9', node_type: 'backend', address: '10.0.4.19:50051', status: 'pending', version: 'v5.0.1', labels: {},
    last_heartbeat: new Date().toISOString(), attached: 'nats,tunnel',
    route_state: [{ route: 'heartbeat', state: 'live' }, { route: 'nats', state: 'live' }, { route: 'tunnel', transport: 'websocket', state: 'live' }], ...over,
  })

  test('listens, shows the worker when the roster gains one, how many token uses are left, and approves it', async ({ page }) => {
    // The shell and the page both read /api/nodes: the worker joins when the test says so.
    let joined = false
    const known = clusterNodes()
    const log = []
    await mockSwarm(page, { log, onNodes: () => (joined ? [...known, arrival()] : known) })
    await mockP2PShare(page)
    // The list of tokens: one token of five uses, one used.
    await page.route('**/api/cluster/join-tokens', route => (route.request().method() === 'GET'
      ? route.fulfill({ json: [{ id: 'jt-1', uses: joined ? 1 : 0, max_uses: 5, state: 'partly_used', routes: ['nats', 'websocket'] }] })
      : route.fallback()))
    await page.goto('/app/nodes/add')
    await toStep2(page)
    await page.getByRole('radio', { name: 'Several' }).click()
    await expect(page.getByTestId('join-token')).toBeVisible()
    await page.getByTestId('add-ran').click()
    await expect(page.getByTestId('listening')).toContainText('Listening for the worker')
    await expect(page.getByTestId('token-valid')).toContainText('Token valid for')
    await expect(page.getByTestId('found-node')).toHaveCount(0)
    joined = true
    const found = page.getByTestId('found-node')
    await expect(found).toBeVisible({ timeout: 20_000 })
    await expect(found).toContainText('gpu-box-9')
    await expect(found).toContainText('Waiting for approval')
    await expect(found.getByTestId('connections')).toContainText('NATS live')
    await expect(found.getByTestId('connections')).toContainText('WebSocket live')
    await expect(page.getByRole('heading', { name: 'Worker found' })).toBeVisible()
    await expect(page.getByTestId('listening')).toHaveCount(0)
    await expect(page.getByTestId('uses-left')).toContainText('4 uses of the token left', { timeout: 10_000 })
    await expect(found.getByRole('link', { name: 'Open' })).toHaveAttribute('href', '/app/nodes/n-new')
    await found.getByRole('button', { name: 'Approve' }).click()
    await expect.poll(() => log.filter(entry => entry.path === '/api/nodes/n-new/approve').length).toBe(1)
    await expect(page.getByText('gpu-box-9 approved')).toBeVisible()
    await expect(page.getByTestId('add-done')).toHaveAttribute('href', '/app/nodes')
  })

  test('does not treat the workers it already knew as arrivals', async ({ page }) => {
    await open(page)
    await toStep2(page)
    await page.getByTestId('add-ran').click()
    await expect(page.getByTestId('listening')).toBeVisible()
    await page.waitForTimeout(3500)
    await expect(page.getByTestId('found-node')).toHaveCount(0)
  })

  test('a worker over P2P shows with its peer and path, and asks to check the peer', async ({ page }) => {
    let joined = false
    const known = clusterNodes()
    const p2p = arrival({
      address: '', peer_id: DEVICE_PEER, attached: 'tunnel', tunnel_transport: 'p2p', tunnel_path: 'hole_punched',
      route_state: [{ route: 'heartbeat', state: 'live' }, { route: 'tunnel', transport: 'p2p', state: 'live' }],
    })
    await open(page, { onNodes: () => (joined ? [...known, p2p] : known) })
    await way(page, 'nats').getByRole('checkbox').uncheck()
    await way(page, 'websocket').getByRole('checkbox').uncheck()
    await way(page, 'p2p').getByRole('checkbox').check()
    await toStep2(page)
    await expect(page.getByTestId('worker-invite')).toBeVisible()
    await page.getByTestId('add-ran').click()
    await expect(page.getByTestId('listening')).toBeVisible()
    joined = true
    const found = page.getByTestId('found-node')
    await expect(found).toBeVisible({ timeout: 20_000 })
    await expect(found).toContainText('12D3KooWLp…o2Qd')
    await expect(found.getByTestId('connections')).toContainText('P2P hole-punched')
    await expect(page.getByText('Check the peer ID on the machine before you approve it.')).toBeVisible()
    // A P2P worker has no token: no uses to count.
    await expect(page.getByTestId('uses-left')).toHaveCount(0)
  })

  test('Add another worker starts again with the roster as it is now', async ({ page }) => {
    let joined = false
    const known = clusterNodes()
    await open(page, { onNodes: () => (joined ? [...known, arrival()] : known) })
    await toStep2(page)
    await page.getByTestId('add-ran').click()
    joined = true
    await expect(page.getByTestId('found-node')).toBeVisible({ timeout: 20_000 })
    await page.getByTestId('add-another').click()
    await expect(page.getByTestId('add-step-choose')).toBeVisible()
    await toStep2(page)
    await page.getByTestId('add-ran').click()
    await expect(page.getByTestId('listening')).toBeVisible()
    await expect(page.getByTestId('found-node')).toHaveCount(0)
  })
})

test.describe('Add worker: other ways in', () => {
  test('the model-split worker has its own steps and a way back', async ({ page }) => {
    await open(page)
    await page.getByTestId('add-split-way').click()
    await expect(page.getByTestId('split-mint')).toBeVisible()
    await page.getByTestId('add-back-to-worker').click()
    await expect(page.getByTestId('add-step-choose')).toBeVisible()
  })

  test('a single install starts with turning distributed mode on, as text it can copy', async ({ page }) => {
    await mockSwarm(page, { nodes: null, features: { distributed: false } })
    await page.goto('/app/nodes/add')
    await expect(page.getByRole('heading', { name: 'Turn on distributed mode first' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('command-block').first()).toContainText('local-ai run --distributed')
    await expect(page.getByRole('link', { name: 'distributed mode documentation' })).toHaveAttribute('href', 'https://localai.io/features/distributed-mode/')
    await expect(page.getByTestId('add-step-choose')).toHaveCount(0)
  })

  test('the empty cluster points to Add worker', async ({ page }) => {
    await mockSwarm(page, { nodes: [] })
    await page.goto('/app/nodes')
    await expect(page.getByTestId('swarm-first-run')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('swarm-first-run').getByRole('link', { name: 'Add worker' }).last().click()
    await expect(page).toHaveURL(/\/app\/nodes\/add$/)
  })
})

test.describe('Add worker: a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  const noSideScroll = page => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)

  test('every step fits the screen, and the command scrolls inside its block', async ({ page }) => {
    await open(page)
    expect(await noSideScroll(page)).toBe(true)
    await toStep2(page)
    await expect(page.getByTestId('command-tabs')).toBeVisible()
    expect(await noSideScroll(page)).toBe(true)
    const block = page.getByTestId('command-block').locator('pre')
    const { scrollWidth, clientWidth } = await block.evaluate(el => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }))
    expect(scrollWidth).toBeGreaterThan(clientWidth)
    await page.getByTestId('add-ran').click()
    await expect(page.getByTestId('listening')).toBeVisible()
    expect(await noSideScroll(page)).toBe(true)
  })
})
