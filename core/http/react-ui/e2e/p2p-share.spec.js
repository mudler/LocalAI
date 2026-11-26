import { test, expect } from './coverage-fixtures.js'
import { adminGroup } from './admin-nav.js'
import { mockHome } from './home-fixtures.js'
import { mockRecommended, recommendedAnswer } from './recommended-fixtures.js'
import { mockRemotes, LOCALAI_MODELS, remote } from './remotes-fixtures.js'
import { DEVICE_PEER, REMOTE_PEER, deviceRows, mockAdmin, mockP2PShare, pendingInviteRows, remoteInviteLink } from './p2p-share-fixtures.js'

// Swarm, P2P, "Devices and invites": the pending invites and the paired
// devices, the Share dialog (kind, link, QR code, what the link carries, its
// warnings, the confirmation phrase), the revoke confirmation, and the invite
// step of "connect a remote" on the first run.

async function openP2P(page, options = {}) {
  await mockAdmin(page)
  const state = await mockP2PShare(page, options)
  await page.goto('/app/p2p')
  await expect(page.getByTestId('p2p-page')).toBeVisible({ timeout: 15_000 })
  return state
}

test.describe('P2P devices and invites', () => {
  test('renders against the real server, where P2P is off, and links to the settings', async ({ page }) => {
    await page.goto('/app/p2p')
    await expect(page.getByTestId('p2p-mode-notice')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('p2p-mode-fix')).toHaveAttribute('href', '/app/p2p/settings')
  })

  test('lists the pending invites and the devices with their path', async ({ page }) => {
    await openP2P(page)
    const invites = page.getByTestId('p2p-invites')
    await expect(invites.getByTestId('p2p-invite-inv-1')).toContainText("Alex's laptop")
    await expect(invites.getByTestId('p2p-invite-inv-1')).toContainText('On relay.example.org')
    await expect(invites.getByTestId('p2p-invite-inv-2')).toContainText('None: pairs only over a direct connection')
    // A used invite is not pending.
    await expect(invites.getByTestId('p2p-invite-inv-old')).toHaveCount(0)
    const devices = page.getByTestId('p2p-devices')
    await expect(devices).toContainText('5 active')
    await expect(devices.getByTestId('p2p-device-d-1')).toContainText('Direct')
    await expect(devices.getByTestId('p2p-device-d-2')).toContainText('Hole-punched')
    await expect(devices.getByTestId('p2p-device-d-3')).toContainText('Via relay.example.org')
    await expect(devices.getByTestId('p2p-device-d-3')).toContainText('Limited')
    await expect(devices.getByTestId('p2p-device-d-4')).toContainText('Not connected')
    await expect(devices.getByTestId('p2p-device-d-5')).toContainText('Revoked')
    await expect(page.getByTestId('p2p-device-revoke-d-5')).toHaveCount(0)
    // A remote shows what it was granted: every model, or the number chosen.
    await expect(invites.getByTestId('p2p-grant-inv-2')).toHaveText('All models')
    await expect(invites.getByTestId('p2p-grant-inv-3')).toHaveText('2 models')
    await expect(invites.getByTestId('p2p-grant-inv-3')).toHaveAttribute('title', 'llama-3.2-3b, qwen3-8b')
    await expect(invites.getByTestId('p2p-grant-inv-1')).toHaveCount(0)
    await expect(devices.getByTestId('p2p-grant-d-2')).toHaveText('All models')
    await expect(devices.getByTestId('p2p-grant-d-6')).toHaveText('2 models')
    await expect(devices.getByTestId('p2p-grant-d-1')).toHaveCount(0)
    await expect(page.getByTestId('p2p-tabs').getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/app/p2p/settings')
  })

  test('cancels a pending invite', async ({ page }) => {
    const state = await openP2P(page)
    await page.getByTestId('p2p-invite-cancel-inv-2').click()
    await expect.poll(() => state.calls.filter(c => c.method === 'DELETE' && c.path === '/invites/inv-2').length).toBe(1)
    await expect(page.getByTestId('p2p-invite-inv-2')).toHaveCount(0)
  })

  test('revokes a device after a confirmation that says what stops', async ({ page }) => {
    const state = await openP2P(page)
    await page.getByTestId('p2p-device-revoke-d-2').click()
    const dialog = page.getByTestId('p2p-revoke-dialog')
    await expect(dialog).toContainText('Revoke Branch LocalAI?')
    await expect(dialog).toContainText('its API key')
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await dialog.getByTestId('p2p-revoke-confirm').click()
    await expect(dialog).toHaveCount(0)
    await expect.poll(() => state.calls.filter(c => c.method === 'DELETE' && c.path === '/devices/d-2').length).toBe(1)
    await expect(page.getByTestId('p2p-device-d-2')).toContainText('Revoked')
  })

  test('sits under Cluster as Sharing when P2P is on, on a single server too', async ({ page }) => {
    await page.route('**/api/features', route => route.fulfill({ json: { distributed: false, p2p: true } }))
    await openP2P(page)
    await expect(adminGroup(page, 'cluster').locator('a[data-admin-item="/app/p2p"]')).toHaveAttribute('aria-current', 'page')
    await expect(adminGroup(page, 'cluster').locator('a[data-admin-item="/app/p2p"]')).toContainText('Sharing')
    await expect(adminGroup(page, 'machine').locator('a[data-admin-item="/app/nodes"]')).toBeVisible()
    await expect(page.getByTestId('p2p-tabs').getByRole('link', { name: 'Devices and invites' })).toHaveAttribute('aria-current', 'page')
  })

  for (const mode of ['off', 'connect']) {
    test(`in mode ${mode} the page says why nothing is listed and links to the settings`, async ({ page }) => {
      await openP2P(page, { mode })
      await expect(page.getByTestId('p2p-mode-notice')).toBeVisible()
      await expect(page.getByTestId('p2p-invites')).toHaveCount(0)
      await expect(page.getByTestId('p2p-share-open')).toHaveCount(0)
      await expect(page.getByTestId('p2p-mode-fix')).toHaveAttribute('href', '/app/p2p/settings')
    })
  }
})

test.describe('Share this instance', () => {
  test('makes a device invite and shows the link, the QR code and the relay pass', async ({ page }) => {
    const state = await openP2P(page)
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await expect(dialog.getByRole('radio', { name: /Device/ })).toBeChecked()
    await expect(dialog.getByTestId('share-kind-note')).toContainText('P2P admission only.')
    await dialog.getByTestId('share-label').fill("Alex's laptop")
    await dialog.getByTestId('share-ttl-3600').click()
    await dialog.getByTestId('share-mint').click()
    const mint = state.calls.find(c => c.method === 'POST')
    expect(mint.body).toEqual({ kind: 'device', label: "Alex's laptop", ttl_seconds: 3600, relays: 'auto' })
    const result = page.getByTestId('share-result')
    await expect(result).toContainText("Invite for Alex's laptop")
    await expect(result.getByTestId('share-link')).toContainText('localai://pair?v=1&k=device')
    await expect(result.getByTestId('qr-code')).toBeVisible()
    await expect(result.getByTestId('share-pass')).toContainText('Registered on relay.example.org')
    await expect(result.getByTestId('share-countdown')).toHaveText(/^(59|60):\d\d$/)
    await expect(result.getByTestId('share-admission')).toBeVisible()
  })

  test('a remote invite has no admission note; the kind changes the note', async ({ page }) => {
    await openP2P(page)
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await expect(dialog.getByTestId('share-kind-note')).toContainText('Models only, nothing else.')
    await expect(dialog.getByTestId('share-kind-note')).toContainText('also the models you add later')
    await dialog.getByTestId('share-mint').click()
    await expect(page.getByTestId('share-result').getByTestId('qr-code')).toBeVisible()
    await expect(page.getByTestId('share-admission')).toHaveCount(0)
  })

  test('a remote invite shares all models by default, with an explicit all_models', async ({ page }) => {
    const state = await openP2P(page)
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    // The model choice belongs to a remote invite only.
    await expect(dialog.getByTestId('share-models')).toHaveCount(0)
    await dialog.getByTestId('share-kind-remote').click()
    const models = dialog.getByTestId('share-models')
    await expect(models.getByRole('radio', { name: 'All models' })).toBeChecked()
    await expect(models).toContainText('also the models you add later')
    await expect(models.getByTestId('share-models-list')).toHaveCount(0)
    await dialog.getByTestId('share-mint').click()
    const mint = state.calls.find(c => c.method === 'POST')
    expect(mint.body).toEqual({ kind: 'remote', label: '', ttl_seconds: 600, relays: 'auto', all_models: true })
    await expect(page.getByTestId('share-granted')).toHaveText('All models')
  })

  test('a remote invite can name the models, and Make invite waits for one', async ({ page }) => {
    const state = await openP2P(page)
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await dialog.getByTestId('share-models-scope-specific').click()
    const list = dialog.getByTestId('share-models-list')
    await expect(list.getByRole('checkbox')).toHaveCount(4)
    // None chosen: the button is off and the reason is next to it.
    const mint = dialog.getByTestId('share-mint')
    await expect(mint).toBeDisabled()
    await expect(mint).toHaveAttribute('aria-describedby', /models-reason/)
    await expect(dialog.getByTestId('share-models-reason')).toHaveText('Choose at least one model, or choose All models.')
    await list.getByRole('checkbox', { name: 'qwen3-8b' }).check()
    await list.getByRole('checkbox', { name: 'llama-3.2-3b' }).check()
    await expect(mint).toBeEnabled()
    await expect(dialog.getByTestId('share-models-reason')).toHaveCount(0)
    await expect(dialog.getByTestId('share-models-count')).toHaveText('2 models chosen')
    // Unticking the last one turns the button off again.
    await list.getByRole('checkbox', { name: 'qwen3-8b' }).uncheck()
    await list.getByRole('checkbox', { name: 'llama-3.2-3b' }).uncheck()
    await expect(mint).toBeDisabled()
    await list.getByRole('checkbox', { name: 'gemma-3-4b' }).check()
    // Going back to All models turns the button on; the choice is kept.
    await dialog.getByTestId('share-models-scope-all').click()
    await expect(mint).toBeEnabled()
    await dialog.getByTestId('share-models-scope-specific').click()
    await expect(list.getByRole('checkbox', { name: 'gemma-3-4b' })).toBeChecked()
    await mint.click()
    const post = state.calls.find(c => c.method === 'POST')
    expect(post.body.models).toEqual(['gemma-3-4b'])
    expect(post.body).not.toHaveProperty('all_models')
    const granted = page.getByTestId('share-granted')
    await expect(granted).toContainText('1 model')
    await expect(granted).toContainText('gemma-3-4b')
    // The pending list shows the grant once the dialog is closed.
    await page.getByTestId('share-done').click()
    await expect(page.getByTestId('p2p-grant-inv-new')).toHaveText('1 model')
  })

  test('shows the refusal of a model the server cannot share', async ({ page }) => {
    await openP2P(page, { fail: { 'POST /invites': { status: 400, message: 'model "qwen3-8b": it has tools attached: MCP servers' } } })
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await dialog.getByTestId('share-models-scope-specific').click()
    await dialog.getByRole('checkbox', { name: 'qwen3-8b' }).check()
    await dialog.getByTestId('share-mint').click()
    await expect(dialog.getByTestId('share-error')).toContainText('it has tools attached')
    await expect(dialog.getByTestId('share-mint')).toBeEnabled()
  })

  test('searches a long list of models', async ({ page }) => {
    const many = Array.from({ length: 12 }, (_, i) => `model-${String(i).padStart(2, '0')}`)
    await openP2P(page, { models: many })
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await dialog.getByTestId('share-models-scope-specific').click()
    await expect(dialog.getByTestId('share-models-list').getByRole('checkbox')).toHaveCount(12)
    await dialog.getByTestId('share-models-search').fill('model-07')
    await expect(dialog.getByTestId('share-models-list').getByRole('checkbox')).toHaveCount(1)
    await dialog.getByTestId('share-models-search').fill('nothing')
    await expect(dialog.getByTestId('share-models-list')).toContainText('No model matches.')
  })

  test('says so when the server has no model to share', async ({ page }) => {
    await openP2P(page, { models: [] })
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await dialog.getByTestId('share-models-scope-specific').click()
    await expect(dialog.getByTestId('share-models-empty')).toContainText('no model to share yet')
    await expect(dialog.getByTestId('share-mint')).toBeDisabled()
  })

  test('keeps the model choice inside the dialog on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openP2P(page)
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await dialog.getByTestId('share-models-scope-specific').click()
    await expect(dialog.getByTestId('share-models-list')).toBeVisible()
    const fits = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    expect(fits).toBe(true)
    const box = await dialog.getByTestId('share-models').boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    await dialog.getByRole('checkbox', { name: 'mistral-7b' }).check()
    await expect(dialog.getByTestId('share-mint')).toBeEnabled()
    await expect(dialog.getByTestId('share-mint')).toBeInViewport()
  })

  test('says that a remote needs a user account when the server refuses the invite', async ({ page }) => {
    const message = 'sharing with a remote needs a signed-in user account'
    await openP2P(page, { fail: { 'POST /invites': { status: 409, type: 'needs_account', message } } })
    await page.getByTestId('p2p-share-open').click()
    const dialog = page.getByTestId('share-dialog')
    await dialog.getByTestId('share-kind-remote').click()
    await dialog.getByTestId('share-mint').click()
    const note = dialog.getByTestId('share-needs-account')
    await expect(note).toContainText('Sign in with a user account to share with a remote')
    await expect(note).toContainText('LOCALAI_API_KEY')
    await expect(dialog.getByTestId('share-error')).toHaveCount(0)
    // Another kind clears it: device invites need no account.
    await dialog.getByTestId('share-kind-device').click()
    await expect(note).toHaveCount(0)
  })

  test('shows any other refusal as the server wrote it', async ({ page }) => {
    await openP2P(page, { fail: { 'POST /invites': { status: 400, message: 'invalid request: label' } } })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    await expect(page.getByTestId('share-error')).toHaveText('invalid request: label')
    await expect(page.getByTestId('share-needs-account')).toHaveCount(0)
  })

  test('says so when the server has no relay', async ({ page }) => {
    await openP2P(page, { mint: { relay: { pass: false, open: false, reason: 'no_relay' }, warnings: ['no_relay', 'no_public_addr'] } })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    await expect(page.getByTestId('share-warn-no_relay')).toContainText('cannot connect without a relay')
    await expect(page.getByTestId('share-warn-no_relay').getByRole('link', { name: 'Add a relay' })).toHaveAttribute('href', '/app/p2p/settings')
    await expect(page.getByTestId('share-pass')).toHaveText('None')
    await expect(page.getByTestId('share-hints')).toHaveText('No public relay')
    // No relay and no public relay: only the local network can pair, and the
    // dialog does not claim that hole punching will work.
    const noPublic = page.getByTestId('share-warn-no_public_addr')
    await expect(noPublic).toContainText('Only a device on the same network can pair')
    await expect(noPublic).toContainText('few minutes after it starts')
    await expect(noPublic).not.toContainText('connects by hole punching')
  })

  test('says that the invite works by hole punching only when it names public relays and no relay of yours', async ({ page }) => {
    await openP2P(page, { mint: { relay: { pass: false, open: false, reason: 'no_relay' }, hints: 2, warnings: ['punch_only', 'no_public_addr'] } })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    const warn = page.getByTestId('share-warn-punch_only')
    await expect(warn).toContainText('works only where hole punching works')
    await expect(warn).toContainText('About 3 in 10 networks block it')
    await expect(warn.getByRole('link', { name: 'Add a relay' })).toHaveAttribute('href', '/app/p2p/settings')
    await expect(page.getByTestId('share-warn-no_relay')).toHaveCount(0)
    await expect(page.getByTestId('share-hints')).toHaveText('Through 2 public relays')
    await expect(page.getByTestId('share-warn-no_public_addr')).toContainText('connects by hole punching through a public relay')
    const shots = process.env.UI_SHOTS_DIR
    if (shots) {
      for (const theme of ['light', 'dark']) {
        await page.emulateMedia({ colorScheme: theme })
        await page.evaluate((t) => { document.documentElement.setAttribute('data-theme', t) }, theme)
        await page.getByTestId('share-result').screenshot({ path: `${shots}/share-punch-only-${theme}.png` })
      }
    }
  })

  test('tells to turn on the public DHT when it is off and the invite has no route', async ({ page }) => {
    await openP2P(page, { status: { public_dht: { on: false, peers: 0 } }, mint: { relay: { pass: false, open: false, reason: 'no_relay' }, hints: 0, warnings: ['no_relay', 'no_public_addr'] } })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    await expect(page.getByTestId('share-warn-no_public_addr')).toContainText('The public DHT is off')
    const shots = process.env.UI_SHOTS_DIR
    if (shots) {
      for (const theme of ['light', 'dark']) {
        await page.emulateMedia({ colorScheme: theme })
        await page.evaluate((t) => { document.documentElement.setAttribute('data-theme', t) }, theme)
        await page.getByTestId('share-result').screenshot({ path: `${shots}/share-no-route-${theme}.png` })
      }
    }
  })

  test('says so when the relay did not answer, and makes the invite again', async ({ page }) => {
    const state = await openP2P(page, { mint: { relay: { id: 'r1', label: 'relay.example.org', pass: false, open: false, reason: 'relay_unreachable' }, warnings: ['relay_unreachable', 'no_relay'] } })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    const warn = page.getByTestId('share-warn-relay_unreachable')
    await expect(warn).toContainText('This invite has no relay pass.')
    await expect(page.getByTestId('share-warn-no_relay')).toHaveCount(0)
    await expect(page.getByTestId('share-pass')).toContainText('relay.example.org did not answer')
    await warn.getByRole('button', { name: 'Make it again' }).click()
    await expect(page.getByTestId('share-dialog')).toBeVisible()
    await page.getByTestId('share-mint').click()
    await expect.poll(() => state.calls.filter(c => c.method === 'POST').length).toBe(2)
  })

  test('cancels the invite from the dialog', async ({ page }) => {
    const state = await openP2P(page)
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    await page.getByTestId('share-cancel-invite').click()
    await expect(page.getByTestId('share-result')).toHaveCount(0)
    await expect.poll(() => state.calls.filter(c => c.method === 'DELETE' && c.path === '/invites/inv-new').length).toBe(1)
  })

  test('shows the phrase once the device redeems, and "It does not match" revokes it', async ({ page }) => {
    const state = await openP2P(page, { redeemAfter: 2 })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-label').fill("Alex's laptop")
    await page.getByTestId('share-mint').click()
    const confirm = page.getByTestId('share-confirm')
    await expect(confirm).toBeVisible({ timeout: 15_000 })
    await expect(confirm).toContainText("Alex's laptop connected")
    await expect(confirm.getByTestId('share-phrase')).toContainText('amber')
    await expect(confirm.getByTestId('share-phrase')).toContainText('echo')
    await expect(confirm).toContainText(DEVICE_PEER.slice(0, 10))
    await confirm.getByTestId('share-mismatch').click()
    await expect(confirm).toHaveCount(0)
    await expect.poll(() => state.calls.filter(c => c.method === 'DELETE' && c.path === '/devices/d-new').length).toBe(1)
  })
})

test.describe('First run: connect a remote with an invite link', () => {
  async function firstRun(page, { create, mode = 'off' } = {}) {
    await mockHome(page, { loaded: [], models: [], chatModels: [] })
    await mockRecommended(page, recommendedAnswer())
    const remotes = await mockRemotes(page, { create, adopt: null })
    const settings = []
    await page.route('**/api/settings', async route => {
      if (route.request().method() === 'POST') settings.push(route.request().postDataJSON())
      return route.fulfill({ json: { success: true } })
    })
    await mockP2PShare(page, { mode })
    await page.goto('/app')
    await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('home-where-remote').click()
    await page.getByTestId('remote-connect-with-invite').click()
    return { remotes, settings }
  }

  const saved = (body) => ({
    status: 201,
    json: { remote: remote({ id: 'remotes-p2p', name: body.name, kind: 'localai', url: `p2p://${REMOTE_PEER}`, entries: [], new: LOCALAI_MODELS }), created: [], skipped: [] },
  })

  test('reads the link, turns P2P on in Connect only mode, saves the remote and adopts the models', async ({ page }) => {
    const { remotes, settings } = await firstRun(page, { create: ({ body }) => saved(body) })
    await expect(page.getByTestId('remote-invite-connect')).toBeDisabled()
    const link = remoteInviteLink()
    await page.getByTestId('remote-invite-link').fill(link)
    const preview = page.getByTestId('remote-invite-preview')
    await expect(preview).toContainText('Branch LocalAI')
    await expect(preview).toContainText('Included · relay.example.org')
    await expect(preview).toContainText('Direct first, then hole punching, then relay.example.org')
    await page.getByTestId('remote-invite-connect').click()
    await expect(page.getByTestId('remote-invite-ok')).toContainText('Connected to Branch LocalAI over P2P. Found 5 models.')
    await expect(page.getByTestId('remote-invite-switched')).toContainText('Connect only')
    expect(settings).toEqual([{ p2p_mode: 'connect' }])
    const create = remotes.calls.find(c => c.method === 'POST' && c.path === '/')
    expect(create.body).toEqual({ kind: 'localai', name: 'Branch LocalAI', invite: link })
    await expect(page.getByTestId('remote-count')).toContainText('5')
    await page.getByTestId('remote-connect').click()
    await expect.poll(() => remotes.calls.filter(c => c.path === '/remotes-p2p/adopt').length).toBe(1)
  })

  test('does not touch the mode when P2P is already on', async ({ page }) => {
    const { settings } = await firstRun(page, { mode: 'connect', create: ({ body }) => saved(body) })
    await page.getByTestId('remote-invite-link').fill(remoteInviteLink())
    await page.getByTestId('remote-invite-connect').click()
    await expect(page.getByTestId('remote-invite-ok')).toBeVisible()
    await expect(page.getByTestId('remote-invite-switched')).toHaveCount(0)
    expect(settings).toEqual([])
  })

  test('warns about an open relay and accepts it on Connect', async ({ page }) => {
    const { remotes } = await firstRun(page, { mode: 'connect', create: ({ body }) => saved(body) })
    const link = `${remoteInviteLink()}&o=0`
    await page.getByTestId('remote-invite-link').fill(link)
    await expect(page.getByTestId('remote-invite-open-relay')).toBeVisible()
    await page.getByTestId('remote-invite-connect').click()
    await expect(page.getByTestId('remote-invite-ok')).toBeVisible()
    const create = remotes.calls.find(c => c.method === 'POST' && c.path === '/')
    expect(create.body).toEqual({ kind: 'localai', name: 'Branch LocalAI', invite: link, allow_open_relay: true })
  })

  test('refuses a link of another kind before sending anything', async ({ page }) => {
    const { remotes } = await firstRun(page)
    await page.getByTestId('remote-invite-link').fill(remoteInviteLink({ kind: 'device' }))
    await expect(page.getByTestId('remote-invite-problem')).toContainText('This is a Device invite.')
    await expect(page.getByTestId('remote-invite-connect')).toBeDisabled()
    expect(remotes.calls.filter(c => c.method === 'POST')).toEqual([])
  })

  test('explains a network that blocks direct connections', async ({ page }) => {
    await firstRun(page, { mode: 'connect', create: { status: 504, json: { error: { message: 'dial: hole punching failed' } } } })
    await page.getByTestId('remote-invite-link').fill(remoteInviteLink())
    await page.getByTestId('remote-invite-connect').click()
    const fail = page.getByTestId('remote-invite-fail')
    await expect(fail).toContainText('Both networks block direct connections.')
    await expect(fail).toContainText('relay.example.org did not answer')
    await expect(fail).toContainText('Open a port.')
    await expect(fail.getByRole('button', { name: 'Try again' })).toBeVisible()
  })

  test('says an invite that was used is not valid', async ({ page }) => {
    await firstRun(page, { mode: 'connect', create: { status: 410, json: { error: { message: 'the invite is not valid' } } } })
    await page.getByTestId('remote-invite-link').fill(remoteInviteLink())
    await page.getByTestId('remote-invite-connect').click()
    await expect(page.getByTestId('remote-invite-fail')).toHaveAttribute('data-kind', 'invalid')
  })
})

test.describe('First run: connect other machines', () => {
  async function local(page, auth) {
    await mockHome(page, { loaded: [], models: [], chatModels: [] })
    await mockRecommended(page, recommendedAnswer())
    await mockRemotes(page)
    if (auth) await mockAdmin(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 15_000 })
  }

  test('points to Swarm, P2P when authentication is on', async ({ page }) => {
    await local(page, true)
    await expect(page.getByTestId('home-step-share')).toContainText('Connect other machines')
    await expect(page.getByTestId('home-step-share-link')).toHaveAttribute('href', '/app/p2p')
  })

  test('says authentication must be on first, and offers the way that needs none', async ({ page }) => {
    await local(page, false)
    await expect(page.getByTestId('home-step-share-noauth')).toContainText('Authentication is off.')
    await expect(page.getByTestId('home-step-share-link')).toHaveCount(0)
  })
})

// Sharing is what other people hold on this server. A worker that joined over
// P2P is a worker of the cluster: it is on Workers, and the page says so.
test.describe('Sharing: no cluster workers', () => {
  const workerDevice = { id: 'd-w1', kind: 'worker', label: 'gpu-box-1', peer_id: '12D3KooWWw1rN6mHcR8yLdQe5Gh6iKoPzWw1', created_at: new Date(Date.now() - 3_600_000).toISOString(), last_seen_at: new Date(Date.now() - 60_000).toISOString(), status: 'active', online: true, path: { kind: 'direct', limited: false } }
  const splitDevice = { ...workerDevice, id: 'd-w2', kind: 'split', label: 'split-1' }
  const workerInvite = { id: 'inv-w', kind: 'worker', label: 'gpu-box-2', created_at: new Date().toISOString(), created_by: 'u1', expires_at: new Date(Date.now() + 300_000).toISOString(), status: 'pending' }

  async function openSharing(page, { distributed = true, devices = [...deviceRows(), workerDevice, splitDevice], invites = [...pendingInviteRows(), workerInvite] } = {}) {
    await page.route('**/api/features', route => route.fulfill({ json: { distributed, p2p: true } }))
    await mockAdmin(page)
    await mockP2PShare(page, { devices, invites })
    await page.goto('/app/p2p')
    await expect(page.getByTestId('p2p-page')).toBeVisible({ timeout: 15_000 })
  }

  test('the title is Sharing, in the header with the breadcrumb and the one primary action', async ({ page }) => {
    await openSharing(page)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sharing')
    const header = page.locator('.page-header')
    await expect(header.getByTestId('admin-crumbs')).toContainText('Sharing')
    await expect(header.locator('.dk-btn--primary')).toHaveCount(1)
    await expect(header.getByTestId('p2p-share-open')).toBeVisible()
  })

  test('devices and invites of kind worker and split are not listed', async ({ page }) => {
    await openSharing(page)
    await expect(page.getByTestId('p2p-device-d-1')).toBeVisible()
    await expect(page.getByTestId('p2p-device-d-w1')).toHaveCount(0)
    await expect(page.getByTestId('p2p-device-d-w2')).toHaveCount(0)
    await expect(page.getByTestId('p2p-invite-inv-w')).toHaveCount(0)
    // Five devices of other people stay counted; the workers are not in the count.
    await expect(page.getByTestId('p2p-devices')).toContainText('5 active')
    await expect(page.getByTestId('p2p-devices')).not.toContainText('gpu-box-1')
  })

  test('a note says workers that joined over P2P are on Workers, and links there', async ({ page }) => {
    await openSharing(page)
    const note = page.getByTestId('p2p-workers-note')
    await expect(note).toContainText('Workers that joined over P2P are on Workers.')
    await expect(page.getByTestId('p2p-workers-link')).toHaveAttribute('href', '/app/nodes')
    await expect(page.getByTestId('p2p-workers-link')).toHaveText('Go to Workers')
  })

  test('a single server with no worker rows shows no note', async ({ page }) => {
    await openSharing(page, { distributed: false, devices: deviceRows(), invites: pendingInviteRows() })
    await expect(page.getByTestId('p2p-devices')).toBeVisible()
    await expect(page.getByTestId('p2p-workers-note')).toHaveCount(0)
  })

  test('a single server that has worker rows still says where they went', async ({ page }) => {
    await openSharing(page, { distributed: false })
    await expect(page.getByTestId('p2p-workers-note')).toBeVisible()
  })

  test('keeps the share state, the invites with their expiry, the grants and the revoke', async ({ page }) => {
    await openSharing(page)
    await expect(page.getByTestId('p2p-share-state')).toContainText('Sharing is on')
    await expect(page.getByTestId('p2p-share-address')).toHaveText('12D3KooWRk…Xk9')
    await expect(page.getByTestId('p2p-settings-link')).toHaveAttribute('href', '/app/p2p/settings')
    await expect(page.getByTestId('p2p-invite-inv-1')).toContainText(/\d+:\d\d/)
    await expect(page.getByTestId('p2p-grant-d-6')).toHaveText('2 models')
    await expect(page.getByTestId('p2p-device-revoke-d-2')).toBeVisible()
  })

  test('the P2P settings and federation sub-routes still open', async ({ page }) => {
    await openSharing(page)
    await page.getByTestId('p2p-tabs').getByRole('link', { name: 'Federation' }).click()
    await expect(page).toHaveURL(/\/app\/p2p\/federation$/)
    await page.getByTestId('p2p-tabs').getByRole('link', { name: 'Settings' }).click()
    await expect(page).toHaveURL(/\/app\/p2p\/settings$/)
  })

  for (const theme of ['light', 'dark']) {
    for (const [name, viewport] of [['desktop', { width: 1280, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
      test(`${theme} ${name}: nothing scrolls sideways`, async ({ page }) => {
        await page.setViewportSize(viewport)
        await page.addInitScript((value) => { try { localStorage.setItem('localai-theme', value) } catch { /* private window */ } }, theme)
        await openSharing(page)
        await expect(page.getByTestId('p2p-devices')).toBeVisible()
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
        expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
      })
    }
  }
})
