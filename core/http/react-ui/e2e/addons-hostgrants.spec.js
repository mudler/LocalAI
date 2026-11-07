import { test, expect } from '@playwright/test'
import { ADMIN, MEMBER, app, installedAddon, release, sessionFor, mockAddons } from './addons-fixtures.js'

// Host access of add-ons: the step of the install dialog, the approval of a
// held upgrade, the Host access page, the deny rules and the blanket switch.
// The owner decides every answer, so these specs drive the page against a
// stand-in for /api/addons/host-grants/* that gives the shapes of the design
// and records what the page sent.

const shots = process.env.UI_SHOTS_DIR

async function shoot(page, name, fullPage = true) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage })
}

const op = (name, title, text, risk, over = {}) => ({ name, title, text, risk, status: 'not_allowed', ...over })

const PREVIEW = {
  digest: 'sha256:' + 'b'.repeat(64), version: '1.0.0', revision: 7, nonce: 'nonce-1', official: true,
  requested: { required: ['workspace.materialize', 'workspace.commit'], optional: ['egress.check', 'quantize.start'] },
  ops: [
    op('workspace.materialize', 'Copy files in', 'Copy files of a chat into the working area of the add-on.', 'changes data'),
    op('workspace.commit', 'Write files back', 'Write results back to the files of a chat.', 'changes data', { status: 'allowed', source: 'deployment' }),
    op('egress.check', 'Ask about the network', 'Ask the host whether a network connection is allowed.', 'network'),
    op('quantize.start', 'Start quantization', 'Start or stop quantization jobs.', 'runs jobs', { status: 'blocked', reason: 'denied_by_admin' }),
  ],
}

function overview(over = {}) {
  return {
    revision: 3, locked: false,
    blanket: { mode: 'off', includeOptional: false, setBy: 'ui', revision: 0 },
    floor: { source: 'file', addons: [] },
    deny: { file: { '*': ['connector.'] }, ui: {} },
    addons: [
      {
        id: 'notes', title: 'Notes', version: '1.2.0', state: 'running', sourceId: 'main',
        requested: { required: ['workspace.materialize'], optional: ['egress.check'] },
        ops: [
          op('workspace.materialize', 'Copy files in', 'Copy files of a chat into the working area of the add-on.', 'changes data', { status: 'allowed', source: 'install', at: '2026-11-03T10:00:00Z' }),
          op('workspace.commit', 'Write files back', 'Write results back to the files of a chat.', 'changes data', { status: 'allowed', source: 'file' }),
          op('egress.check', 'Ask about the network', 'Ask the host whether a network connection is allowed.', 'network', { status: 'allowed', source: 'blanket', at: '2026-11-03T10:00:00Z' }),
          op('quantize.start', 'Start quantization', 'Start or stop quantization jobs.', 'runs jobs', { status: 'blocked', reason: 'denied_by_admin' }),
        ],
        noLongerRequested: [],
      },
    ],
    ...over,
  }
}

// The stand-in for the host-grants routes. `state.overview` and `state.preview`
// are what the owner answers; every request is logged.
async function mockHostGrants(page, appsState, options = {}) {
  const state = { overview: options.overview || overview(), preview: options.preview || PREVIEW, log: [], affects: options.affects || [], failInstall: options.failInstall || null, previewDelayMs: options.previewDelayMs || 0 }
  await page.route(/\/api\/addons\/host-grants(\/.*)?(\?.*)?$/, async route => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/addons/host-grants', '') || '/'
    const body = req.postData() ? JSON.parse(req.postData()) : null
    state.log.push({ method: req.method(), path, query: url.search, body, confirm: req.headers()['x-localai-confirm'] })
    const json = (payload, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(payload) })
    if (req.method() === 'GET' && path === '/') return json(state.overview)
    if (path === '/preview') {
      // A slow owner: it downloads and verifies the package before it answers.
      if (state.previewDelayMs) await new Promise(r => setTimeout(r, state.previewDelayMs))
      return json(state.preview)
    }
    if (path === '/install') {
      if (state.failInstall) return json({ error: { type: state.failInstall, message: state.failInstall } }, 409)
      if (body.action === 'upgrade') {
        const a = state.overview.addons.find(x => x.id === body.addonId)
        const rec = appsState.installed.find(x => x.id === body.addonId)
        if (rec) { rec.version = body.version; rec.generation += 1 }
        if (a) { a.version = body.version; delete a.pendingUpgrade }
      } else {
        appsState.installed.push(installedAddon({ id: body.addonId, title: body.addonId, enabled: false, state: 'stopped', generation: 1, version: body.version }))
      }
      state.overview.revision += 1
      return json({ payload: { OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: 1 } })
    }
    if (path === '/blanket') {
      state.overview.blanket = { ...state.overview.blanket, mode: body.mode, includeOptional: body.includeOptional, revision: state.overview.blanket.revision + 1 }
      state.overview.revision += 1
      return json({ revision: state.overview.revision, blanket: state.overview.blanket, changed: [] })
    }
    if (path === '/deny') {
      if (body.dryRun) return json({ affects: state.affects })
      state.overview.deny.ui = body.deny
      state.overview.revision += 1
      return json({ revision: state.overview.revision })
    }
    // PUT and DELETE of one add-on.
    const id = decodeURIComponent(path.slice(1))
    const a = state.overview.addons.find(x => x.id === id)
    if (!a) return json({ error: { type: 'addon_error', message: 'unknown' } }, 404)
    if (req.method() === 'PUT') {
      a.ops = a.ops.map(o => (o.status === 'allowed' && o.source !== 'file' && !body.ops.includes(o.name) ? { ...o, status: 'not_allowed', source: '' } : o))
    } else {
      a.ops = a.ops.map(o => (o.status === 'allowed' && o.source !== 'file' ? { ...o, status: 'not_allowed', source: '' } : o))
    }
    state.overview.revision += 1
    return json({ revision: state.overview.revision })
  })
  return state
}

async function signIn(page, user = ADMIN) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(user, { addons: true }) }))
}

async function open(page, { apps, installed, hostgrants, releases } = {}) {
  await signIn(page)
  const state = await mockAddons(page, { apps: apps || [app()], installed: installed || [installedAddon()], releases })
  const host = await mockHostGrants(page, state, hostgrants)
  return { state, host }
}

test.describe('Host access: install dialog', () => {
  test('lists required operations locked and ticked, optional ones unticked, and sends the approval', async ({ page }) => {
    const { state, host } = await open(page, { installed: [], releases: [release('notes', { name: 'Notes' })] })
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'Notes' }).getByRole('button', { name: /^Install / }).click()
    const dialog = page.getByTestId('install-dialog')
    const step = dialog.getByTestId('install-host-access')
    await expect(step).toBeVisible()
    const row = (name) => step.locator(`[data-op="${name}"]`)
    // Required: on, cannot be turned off.
    await expect(row('workspace.materialize').getByRole('switch')).toHaveAttribute('aria-checked', 'true')
    await expect(row('workspace.materialize').getByRole('switch')).toBeDisabled()
    await expect(row('workspace.materialize')).toContainText('Required. The add-on cannot run without it.')
    // Optional: off by default, can be turned on.
    await expect(row('egress.check').getByRole('switch')).toHaveAttribute('aria-checked', 'false')
    await expect(row('egress.check').getByRole('switch')).toBeEnabled()
    // Held by the deployment: on, no tick needed.
    await expect(row('workspace.commit')).toContainText('Already allowed by the deployment')
    // Blocked by policy: greyed, off, with the reason and without the layer.
    await expect(row('quantize.start').getByRole('switch')).toBeDisabled()
    await expect(row('quantize.start')).toContainText('Blocked by the administrator policy')
    await expect(step).not.toContainText('denied_by_admin')
    await expect(step.getByTestId('hostaccess-not-official')).toHaveCount(0)
    await shoot(page, 'install-dialog')
    await row('egress.check').getByRole('switch').click()
    await dialog.getByTestId('install-confirm').click()
    await expect(dialog.getByTestId('flow-steps')).toBeVisible()
    await expect.poll(() => host.log.filter(l => l.path === '/install').length).toBe(1)
    const sent = host.log.find(l => l.path === '/install')
    expect(sent.body.approve).toEqual({ digest: PREVIEW.digest, ops: ['workspace.materialize', 'egress.check'], revision: 7, nonce: 'nonce-1' })
    expect(sent.body).toMatchObject({ action: 'install', addonId: 'notes', sourceId: 'main', version: '1.0.0' })
    expect(sent.confirm).toBe(`notes@${PREVIEW.digest}`)
    // The plain install route was not used for this add-on.
    expect(state.log.dispatch.filter(d => d.action === 'install')).toHaveLength(0)
  })

  test('says what it waits for when the owner is slow, then shows the permissions, with no sideways scroll', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await open(page, { installed: [], releases: [release('notes', { name: 'Notes' })], hostgrants: { previewDelayMs: 4500 } })
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'Notes' }).getByRole('button', { name: /^Install / }).click()
    const dialog = page.getByTestId('install-dialog')
    const loading = dialog.getByTestId('hostaccess-loading')
    await expect(loading).toHaveText('Reading what this add-on asks for...')
    // The install stays closed while the owner has not confirmed anything.
    await expect(dialog.getByTestId('install-confirm')).toBeDisabled()
    await expect(loading).toContainText('downloaded and its signature checked', { timeout: 6000 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBe(false)
    await expect(dialog.getByTestId('install-host-access')).toBeVisible({ timeout: 8000 })
    await expect(loading).toHaveCount(0)
  })

  test('says so when the add-on comes from another library than the official one', async ({ page }) => {
    await open(page, { installed: [], releases: [release('notes', { name: 'Notes' })], hostgrants: { preview: { ...PREVIEW, official: false } } })
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'Notes' }).getByRole('button', { name: /^Install / }).click()
    await expect(page.getByTestId('hostaccess-not-official')).toContainText('not the official library')
  })

  test('blocks the install when a required operation is blocked by the administrator policy', async ({ page }) => {
    const blocked = { ...PREVIEW, blockedRequired: ['workspace.materialize'], canInstall: false, ops: PREVIEW.ops.map(o => (o.name === 'workspace.materialize' ? { ...o, status: 'blocked' } : o)) }
    await open(page, { installed: [], releases: [release('notes', { name: 'Notes' })], hostgrants: { preview: blocked } })
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'Notes' }).getByRole('button', { name: /^Install / }).click()
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('hostaccess-blocked')).toContainText('which your administrator policy blocks. It cannot be installed.')
    await expect(dialog.getByTestId('install-confirm')).toBeDisabled()
    await shoot(page, 'install-blocked')
  })

  test('an add-on that asks for nothing shows no host access step', async ({ page }) => {
    await open(page, { installed: [], releases: [release('notes', { name: 'Notes' })], hostgrants: { preview: { ...PREVIEW, requested: { required: [], optional: [] }, ops: [] } } })
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'Notes' }).getByRole('button', { name: /^Install / }).click()
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-confirm')).toBeEnabled()
    await expect(dialog.getByTestId('install-host-access')).toHaveCount(0)
  })

  test('a stale approval is refused with words, and nothing is claimed as installed', async ({ page }) => {
    await open(page, { installed: [], releases: [release('notes', { name: 'Notes' })], hostgrants: { failInstall: 'approval_stale' } })
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'Notes' }).getByRole('button', { name: /^Install / }).click()
    const dialog = page.getByTestId('install-dialog')
    await dialog.getByTestId('install-confirm').click()
    await expect(dialog.getByTestId('install-dialog-failed')).toContainText('The package changed since you looked at it.')
  })
})

test.describe('Host access: held upgrade', () => {
  const waiting = () => {
    const o = overview()
    o.addons[0].pendingUpgrade = { version: '1.3.0', delta: ['workspace.export'] }
    return o
  }
  const upgradePreview = {
    ...PREVIEW, version: '1.3.0', added: ['workspace.export'],
    requested: { required: ['workspace.materialize', 'workspace.export'], optional: [] },
    ops: [
      op('workspace.materialize', 'Copy files in', 'Copy files of a chat into the working area of the add-on.', 'changes data', { status: 'allowed', source: 'install' }),
      op('workspace.export', 'Export files', 'Export files from the workspace of a chat.', 'changes data'),
    ],
  }

  test('shows a banner and a chip, keeps the old version, and approves with the upgrade action', async ({ page }) => {
    const { state, host } = await open(page, { hostgrants: { overview: waiting(), preview: upgradePreview } })
    await page.goto('/app/addons')
    const banner = page.getByTestId('host-waiting')
    await expect(banner).toContainText('Notes 1.3.0 waits for your approval')
    await expect(banner).toContainText('Version 1.2.0 keeps running')
    await expect(page.getByTestId('approval-chip')).toBeVisible()
    await expect(page.getByTestId('access-waiting')).toHaveText('1')
    await shoot(page, 'upgrade-banner')
    await banner.getByRole('button', { name: /Review and approve/ }).click()
    const dialog = page.getByTestId('approve-dialog')
    await expect(dialog.locator('[data-op="workspace.export"]')).toHaveAttribute('data-added', 'true')
    await expect(dialog.locator('[data-op="workspace.materialize"]')).not.toHaveAttribute('data-added', 'true')
    await shoot(page, 'upgrade-approve')
    await dialog.getByTestId('approve-confirm').click()
    await expect.poll(() => host.log.filter(l => l.path === '/install').length).toBe(1)
    const sent = host.log.find(l => l.path === '/install')
    expect(sent.body).toMatchObject({ action: 'upgrade', addonId: 'notes', version: '1.3.0', approve: { ops: ['workspace.export'], digest: PREVIEW.digest } })
    await expect(page.getByTestId('host-waiting')).toHaveCount(0)
    expect(state.installed.find(a => a.id === 'notes').version).toBe('1.3.0')
  })
})

test.describe('Host access: page', () => {
  test('is for administrators only', async ({ page }) => {
    await signIn(page, MEMBER)
    await mockAddons(page, {})
    await page.goto('/app/addons?tab=access')
    await expect(page.getByRole('tab', { name: 'Host access' })).toHaveCount(0)
    await expect(page.getByTestId('host-access')).toHaveCount(0)
  })

  test('shows what each add-on holds, from where, and greys the deployment and blocked ones', async ({ page }) => {
    await open(page)
    await page.goto('/app/addons?tab=access')
    const card = page.getByTestId('host-addon')
    await expect(card).toBeVisible()
    const row = (name) => card.locator(`[data-op="${name}"]`)
    await expect(row('workspace.materialize')).toHaveAttribute('data-source', 'you')
    await expect(row('egress.check')).toHaveAttribute('data-source', 'blanket')
    await expect(row('egress.check')).toContainText('Blanket')
    await expect(row('workspace.commit')).toHaveAttribute('data-source', 'deployment')
    await expect(row('workspace.commit').getByRole('switch')).toBeDisabled()
    await expect(row('workspace.commit')).toContainText("Set in the deployment's policy file or flags.")
    await expect(row('quantize.start').getByRole('switch')).toBeDisabled()
    await expect(row('quantize.start')).toContainText('Blocked by the administrator policy')
    await shoot(page, 'host-access')
  })

  test('revokes one operation, and asks first when the add-on requires it', async ({ page }) => {
    const { host } = await open(page)
    await page.goto('/app/addons?tab=access')
    const card = page.getByTestId('host-addon')
    await card.locator('[data-op="egress.check"]').getByRole('switch').click()
    await expect.poll(() => host.log.filter(l => l.method === 'PUT' && l.path === '/notes').length).toBe(1)
    const put = host.log.find(l => l.method === 'PUT' && l.path === '/notes')
    expect(put.body).toEqual({ revision: 3, ops: ['workspace.materialize'] })
    expect(put.confirm).toBe('notes')
    await expect(card.locator('[data-op="egress.check"]')).toHaveAttribute('data-status', 'not_allowed')
    // A required operation: the administrator is told the add-on will stop.
    await card.locator('[data-op="workspace.materialize"]').getByRole('switch').click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toContainText('cannot run without Copy files in')
    await shoot(page, 'revoke-required')
    await confirm.getByRole('button', { name: 'Cancel' }).click()
    expect(host.log.filter(l => l.method === 'PUT')).toHaveLength(1)
  })

  test('revokes all the host access of an add-on after a confirmation', async ({ page }) => {
    const { host } = await open(page)
    await page.goto('/app/addons?tab=access')
    await page.getByTestId('host-revoke-all').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke all' }).click()
    await expect.poll(() => host.log.filter(l => l.method === 'DELETE').length).toBe(1)
    expect(host.log.find(l => l.method === 'DELETE').query).toBe('?revision=3')
  })

  test('keeps the deployment rules locked and adds a rule after showing what it takes away', async ({ page }) => {
    const { host } = await open(page, { hostgrants: { affects: [{ id: 'notes', loses: ['egress.check'], willStop: false }] } })
    await page.goto('/app/addons?tab=access')
    const deny = page.getByTestId('host-deny')
    const fileRule = deny.locator('[data-origin="file"]')
    await expect(fileRule).toContainText('connector.')
    await expect(fileRule).toContainText('Set in the deployment')
    await expect(fileRule.getByRole('button')).toHaveCount(0)
    await deny.getByTestId('deny-target').selectOption('notes')
    await deny.getByTestId('deny-entry').fill('egress.')
    await deny.getByTestId('deny-add').click()
    const dialog = page.getByTestId('deny-confirm-dialog')
    await expect(dialog.getByTestId('deny-affects')).toContainText('egress.check')
    await shoot(page, 'deny-confirm')
    // The dry run changed nothing.
    expect(host.log.filter(l => l.path === '/deny').map(l => !!l.body.dryRun)).toEqual([true])
    await dialog.getByTestId('deny-confirm').click()
    await expect.poll(() => host.log.filter(l => l.path === '/deny').length).toBe(2)
    expect(host.log.filter(l => l.path === '/deny')[1].body).toEqual({ revision: 3, deny: { notes: ['egress.'] } })
    await expect(deny.locator('[data-origin="ui"]')).toContainText('egress.')
  })

  test('a typo in a rule is explained before anything is sent', async ({ page }) => {
    const { host } = await open(page)
    await page.goto('/app/addons?tab=access')
    await page.getByTestId('deny-entry').fill('Egress Check')
    await page.getByTestId('deny-add').click()
    await expect(page.getByTestId('deny-error')).toContainText('Write an operation such as')
    expect(host.log.filter(l => l.path === '/deny')).toHaveLength(0)
  })

  test('is read-only when the deployment locks it', async ({ page }) => {
    const { host } = await open(page, { hostgrants: { overview: overview({ locked: true }) } })
    await page.goto('/app/addons?tab=access')
    await expect(page.getByTestId('host-locked')).toBeVisible()
    await expect(page.getByTestId('host-addon').locator('[data-op="egress.check"]').getByRole('switch')).toBeDisabled()
    await expect(page.getByTestId('host-revoke-all')).toHaveCount(0)
    await expect(page.getByTestId('deny-form')).toHaveCount(0)
    await expect(page.getByTestId('blanket-switch')).toBeDisabled()
    expect(host.log.filter(l => l.method !== 'GET')).toHaveLength(0)
  })
})

test.describe('Host access: blanket switch', () => {
  test('needs the typed sentence, states the risk, and sends the scope', async ({ page }) => {
    const { host } = await open(page)
    await page.goto('/app/addons?tab=access')
    const panel = page.getByTestId('host-blanket')
    await expect(panel.getByTestId('blanket-state')).toContainText('Off.')
    await panel.getByTestId('blanket-switch').click()
    const dialog = page.getByTestId('blanket-on-dialog')
    const confirm = dialog.getByTestId('blanket-confirm')
    await expect(confirm).toBeDisabled()
    await expect(dialog.getByTestId('blanket-risk')).toContainText('official library')
    await dialog.getByTestId('blanket-phrase').fill('grant official add-ons what they declare')
    await expect(confirm).toBeEnabled()
    // Widening the scope clears the phrase and shows the plain statement.
    await dialog.getByRole('button', { name: 'Any library' }).click()
    await expect(confirm).toBeDisabled()
    await expect(dialog.getByTestId('blanket-risk')).toContainText('Any entry of any library receives what it declares')
    await expect(dialog.getByTestId('blanket-risk')).toContainText('malicious or compromised entry gets those operations at once')
    await dialog.getByTestId('blanket-phrase').fill('grant official add-ons what they declare')
    await expect(confirm).toBeDisabled()
    await dialog.getByTestId('blanket-phrase').fill('grant any library add-on what it declares')
    await shoot(page, 'blanket-any')
    await confirm.click()
    await expect.poll(() => host.log.filter(l => l.path === '/blanket').length).toBe(1)
    expect(host.log.find(l => l.path === '/blanket').body).toEqual({ revision: 3, mode: 'any', includeOptional: false, confirm: 'grant any library add-on what it declares', applyToInstalled: false })
    expect(host.log.find(l => l.path === '/blanket').confirm).toBe('blanket')
    await expect(panel.getByTestId('blanket-state')).toContainText('any library')
  })

  test('turning it off keeps the grants it made, marked blanket, with a button that revokes them all', async ({ page }) => {
    const on = overview({ blanket: { mode: 'official', includeOptional: false, setBy: 'ui', revision: 2 } })
    const { host } = await open(page, { hostgrants: { overview: on } })
    await page.goto('/app/addons?tab=access')
    const panel = page.getByTestId('host-blanket')
    await expect(panel.getByTestId('blanket-held')).toContainText('Notes')
    await panel.getByTestId('blanket-switch').click()
    const dialog = page.getByTestId('blanket-off-dialog')
    await expect(dialog).toContainText('The grants the switch already made stay')
    await expect(dialog.getByTestId('blanket-also-revoke')).toHaveAttribute('aria-checked', 'false')
    await shoot(page, 'blanket-off')
    await dialog.getByTestId('blanket-off-confirm').click()
    await expect.poll(() => host.log.filter(l => l.path === '/blanket').length).toBe(1)
    expect(host.log.find(l => l.path === '/blanket').body).toEqual({ revision: 3, mode: 'off', includeOptional: false, revokeOnOff: false })
    // The grant stays, still marked blanket, and can be revoked in one click.
    await expect(page.getByTestId('host-addon').locator('[data-op="egress.check"]')).toHaveAttribute('data-source', 'blanket')
    await expect(panel.getByTestId('blanket-revoke-all')).toBeVisible()
    await panel.getByTestId('blanket-revoke-all').click()
    await expect.poll(() => host.log.filter(l => l.method === 'PUT' && l.path === '/notes').length).toBe(1)
    expect(host.log.find(l => l.method === 'PUT' && l.path === '/notes').body.ops).toEqual(['workspace.materialize'])
    await expect(page.getByTestId('host-addon').locator('[data-op="egress.check"]')).toHaveAttribute('data-status', 'not_allowed')
  })

  test('is read-only when the deployment sets it', async ({ page }) => {
    await open(page, { hostgrants: { overview: overview({ blanket: { mode: 'official', includeOptional: false, setBy: 'deployment', revision: 1 } }) } })
    await page.goto('/app/addons?tab=access')
    await expect(page.getByTestId('blanket-fixed')).toBeVisible()
    await expect(page.getByTestId('blanket-switch')).toBeDisabled()
  })
})

test.describe('Host access: small screen and dark theme', () => {
  test('fits 390 px without scrolling sideways, in the dark theme, and the dialog keeps its actions in view', async ({ page }) => {
    await page.addInitScript(() => { try { localStorage.setItem('localai-theme', 'dark') } catch { /* private window */ } })
    await page.setViewportSize({ width: 390, height: 800 })
    const o = overview()
    o.addons[0].pendingUpgrade = { version: '1.3.0', delta: ['workspace.export'] }
    await open(page, { hostgrants: { overview: o } })
    await page.goto('/app/addons?tab=access')
    await expect(page.getByTestId('host-addon')).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
    expect(wide).toBe(false)
    await shoot(page, 'host-access-390-dark')
    await page.getByTestId('blanket-switch').click()
    const dialog = page.getByTestId('blanket-on-dialog')
    const box = await dialog.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeLessThanOrEqual(800)
    await expect(dialog.getByTestId('blanket-confirm')).toBeInViewport()
    await shoot(page, 'blanket-dialog-390-dark', false)
  })

  test('keyboard: the dialog traps focus, Escape closes it and focus returns to the switch', async ({ page }) => {
    await open(page)
    await page.goto('/app/addons?tab=access')
    const toggle = page.getByTestId('blanket-switch')
    await toggle.focus()
    await page.keyboard.press('Enter')
    const dialog = page.getByTestId('blanket-on-dialog')
    await expect(dialog.getByTestId('blanket-phrase')).toBeFocused()
    for (let i = 0; i < 8; i += 1) await page.keyboard.press('Tab')
    expect(await dialog.evaluate(el => el.contains(document.activeElement))).toBe(true)
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(toggle).toBeFocused()
  })
})
