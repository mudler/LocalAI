import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'
import { mockRecommended, recommendedAnswer } from './recommended-fixtures.js'
import { mockRemotes, LOCALAI_MODELS, remote } from './remotes-fixtures.js'
import { DEVICE_PEER, REMOTE_PEER, mockAdmin, mockP2PShare, remoteInviteLink } from './p2p-share-fixtures.js'

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
  test('lists the pending invites and the devices with their path', async ({ page }) => {
    await openP2P(page)
    const invites = page.getByTestId('p2p-invites')
    await expect(invites.getByTestId('p2p-invite-inv-1')).toContainText("Alex's laptop")
    await expect(invites.getByTestId('p2p-invite-inv-1')).toContainText('On relay.example.org')
    await expect(invites.getByTestId('p2p-invite-inv-2')).toContainText('None: pairs only over a direct connection')
    // A used invite is not pending.
    await expect(invites.getByTestId('p2p-invite-inv-old')).toHaveCount(0)
    const devices = page.getByTestId('p2p-devices')
    await expect(devices).toContainText('4 active')
    await expect(devices.getByTestId('p2p-device-d-1')).toContainText('Direct')
    await expect(devices.getByTestId('p2p-device-d-2')).toContainText('Hole-punched')
    await expect(devices.getByTestId('p2p-device-d-3')).toContainText('Via relay.example.org')
    await expect(devices.getByTestId('p2p-device-d-3')).toContainText('Limited')
    await expect(devices.getByTestId('p2p-device-d-4')).toContainText('Not connected')
    await expect(devices.getByTestId('p2p-device-d-5')).toContainText('Revoked')
    await expect(page.getByTestId('p2p-device-revoke-d-5')).toHaveCount(0)
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
    await expect(dialog.getByTestId('share-kind-note')).toContainText('Models only.')
    await dialog.getByTestId('share-mint').click()
    await expect(page.getByTestId('share-result').getByTestId('qr-code')).toBeVisible()
    await expect(page.getByTestId('share-admission')).toHaveCount(0)
  })

  test('says so when the server has no relay', async ({ page }) => {
    await openP2P(page, { mint: { relay: { pass: false, open: false, reason: 'no_relay' }, warnings: ['no_relay', 'no_public_addr'] } })
    await page.getByTestId('p2p-share-open').click()
    await page.getByTestId('share-mint').click()
    await expect(page.getByTestId('share-warn-no_relay')).toContainText('cannot connect without a relay')
    await expect(page.getByTestId('share-warn-no_relay').getByRole('link', { name: 'Add a relay' })).toHaveAttribute('href', '/app/p2p/settings')
    await expect(page.getByTestId('share-pass')).toHaveText('None')
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
