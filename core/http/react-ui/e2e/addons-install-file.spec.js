import { test, expect } from '@playwright/test'
import { ADMIN, app, installedAddon, sessionFor, mockAddons } from './addons-fixtures.js'

// Install from a file: the button on the Add-ons page, the dialog with the
// unsigned notice and the host access step, the upgrade and the downgrade
// rules, and the link that `local-ai addons install` prints. The owner decides
// every answer, so these specs drive the page against a stand-in for the file
// routes and the host-grants routes and record what the page sent.

const shots = process.env.UI_SHOTS_DIR

async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}.png` })
}

const ID = 'c'.repeat(32)
const SHA = 'd'.repeat(64)

const op = (name, title, text, risk, over = {}) => ({ name, title, text, risk, status: 'not_allowed', ...over })

function fileView(over = {}) {
  return {
    fileId: ID, name: 'memory-0.2.0-linux-amd64.zip', size: 5364318, sha256: SHA, addonId: 'memory', title: 'Short memory',
    version: '0.2.0', platform: 'linux/amd64', api: { min: 2, max: 2 }, required: ['workspace.materialize'], optional: ['egress.check'],
    installed: false, installedVersion: '', relation: 'new', source: 'local-file', origin: 'local file, unsigned', ...over,
  }
}

const PREVIEW = {
  addonId: 'memory', title: 'Short memory', digest: 'sha256:' + SHA, version: '0.2.0', installed: false, official: false, revision: 4, nonce: 'nonce-f',
  requested: { required: ['workspace.materialize'], optional: ['egress.check'] }, canInstall: true, blockedRequired: [], added: [], blanket: { applies: false },
  ops: [
    op('workspace.materialize', 'Copy files in', 'Copy files of a chat into the working area of the add-on.', 'changes data'),
    op('egress.check', 'Ask about the network', 'Ask the host whether a network connection is allowed.', 'network'),
  ],
}

async function open(page, { file = fileView(), installed = [], uploadStatus = 200, uploadBody, locked } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
  const state = await mockAddons(page, { apps: [], installed })
  const seen = { uploads: [], described: [], removed: [], preview: [], install: [], dispatch: state.log.dispatch }
  await page.route(/\/api\/addons\/files(\/[0-9a-f]+)?$/, route => {
    const req = route.request()
    const json = (payload, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(payload) })
    if (req.method() === 'POST') {
      seen.uploads.push({ contentType: req.headers()['content-type'], body: req.postDataBuffer()?.toString('latin1') || '' })
      return json(uploadBody || file, uploadStatus)
    }
    const id = req.url().split('/').pop()
    if (req.method() === 'DELETE') { seen.removed.push(id); return route.fulfill({ status: 204 }) }
    seen.described.push(id)
    return id === ID ? json(file) : json({ error: { type: 'not_found', message: 'No such file' } }, 404)
  })
  await page.route(/\/api\/addons\/host-grants(\/.*)?(\?.*)?$/, route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/addons/host-grants', '') || '/'
    const body = req.postData() ? JSON.parse(req.postData()) : null
    const json = (payload, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(payload) })
    if (path === '/') return json({ revision: 4, locked: false, blanket: { mode: 'off', includeOptional: false, setBy: 'ui', revision: 0 }, floor: { source: 'file', addons: [] }, deny: { file: {}, ui: {} }, addons: [] })
    if (path === '/preview') { seen.preview.push(body); return json({ ...PREVIEW, installed: file.installed, ...(locked ? { locked: true, lockedReason: locked } : {}) }) }
    if (path === '/install') {
      seen.install.push({ body, confirm: req.headers()['x-localai-confirm'] })
      if (!file.installed) {
        state.installed.push(installedAddon({ id: 'memory', title: 'Short memory', version: file.version, sourceId: 'local-file', verification: 'sha256', enabled: false, state: 'stopped', generation: 1 }))
        state.apps.push(app({ id: 'memory', name: 'memory', title: 'Short memory', version: file.version, generation: 1, enabled: false, state: 'stopped', mainPage: undefined, hasUI: false }))
      }
      return json({ payload: { OperationID: body.operationId, AddonID: body.addonId, Phase: 'completed', Generation: 1 } })
    }
    return json({ error: { type: 'addon_error', message: 'unknown' } }, 404)
  })
  return { state, seen }
}

test.describe('Install from a file', () => {
  test('uploads the zip, shows the unsigned notice, the digest and the host operations, and sends the approval', async ({ page }) => {
    const { seen } = await open(page)
    await page.goto('/app/addons')
    await page.getByTestId('install-file-input').setInputFiles({ name: 'memory-0.2.0-linux-amd64.zip', mimeType: 'application/zip', buffer: Buffer.from('PK-fake-zip') })
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog).toBeVisible()
    expect(seen.uploads).toHaveLength(1)
    expect(seen.uploads[0].contentType).toContain('multipart/form-data')
    expect(seen.uploads[0].body).toContain('name="file"; filename="memory-0.2.0-linux-amd64.zip"')
    await expect(dialog.getByTestId('install-unsigned')).toContainText('Local file, unsigned')
    await expect(dialog.getByTestId('install-unsigned')).toContainText('Nothing signs this file')
    const facts = dialog.getByTestId('install-file-facts')
    await expect(facts.getByTestId('install-file-sha')).toHaveText(SHA)
    await expect(facts).toContainText('memory 0.2.0')
    await expect(facts).toContainText('linux/amd64')
    // The same step as for a library entry; a file is never official.
    const step = dialog.getByTestId('install-host-access')
    await expect(step).toBeVisible()
    await expect(step.locator('[data-op="workspace.materialize"]').getByRole('switch')).toBeDisabled()
    await expect(step.getByTestId('hostaccess-not-official')).toHaveCount(0)
    expect(seen.preview).toEqual([{ sourceId: 'local-file', addonId: 'memory', version: ID }])
    await step.locator('[data-op="egress.check"]').getByRole('switch').click()
    await shoot(page, 'install-from-file-dialog')
    await dialog.getByTestId('install-confirm').click()
    await expect(dialog.getByTestId('flow-steps')).toBeVisible()
    await expect.poll(() => seen.install.length).toBe(1)
    const { body, confirm } = seen.install[0]
    expect(confirm).toBe('memory@sha256:' + SHA)
    expect(body).toMatchObject({ action: 'install', sourceId: 'local-file', addonId: 'memory', version: ID })
    expect(body.downgrade).toBeUndefined()
    expect(body.approve).toMatchObject({ digest: 'sha256:' + SHA, revision: 4, nonce: 'nonce-f' })
    expect(body.approve.ops.sort()).toEqual(['egress.check', 'workspace.materialize'])
    // A fresh install is turned on, like a library install.
    await expect.poll(() => seen.dispatch.some(d => d.action === 'enable')).toBe(true)
  })

  test('an upgrade says what it replaces, installs through the upgrade action and does not turn the add-on on', async ({ page }) => {
    const { seen } = await open(page, { file: fileView({ installed: true, installedVersion: '0.1.0', relation: 'upgrade' }), installed: [installedAddon({ id: 'memory', title: 'Short memory', version: '0.1.0', sourceId: 'local-file', verification: 'sha256', enabled: false, state: 'stopped' })] })
    await page.goto('/app/addons')
    await page.getByTestId('install-file-input').setInputFiles({ name: 'm.zip', mimeType: 'application/zip', buffer: Buffer.from('PK') })
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-file-upgrade')).toContainText('replaces version 0.1.0 with version 0.2.0')
    await dialog.getByTestId('install-confirm').click()
    await expect.poll(() => seen.install.length).toBe(1)
    expect(seen.install[0].body.action).toBe('upgrade')
    expect(seen.install[0].body.downgrade).toBeUndefined()
    await expect(dialog).toContainText('now runs version 0.2.0')
    expect(seen.dispatch.filter(d => d.action === 'enable')).toHaveLength(0)
  })

  test('an older version needs a tick and sends the downgrade confirmation', async ({ page }) => {
    const { seen } = await open(page, { file: fileView({ version: '0.0.9', installed: true, installedVersion: '0.1.0', relation: 'downgrade' }), installed: [installedAddon({ id: 'memory', title: 'Short memory', version: '0.1.0', sourceId: 'local-file', verification: 'sha256', enabled: false, state: 'stopped' })] })
    await page.goto('/app/addons')
    await page.getByTestId('install-file-input').setInputFiles({ name: 'm.zip', mimeType: 'application/zip', buffer: Buffer.from('PK') })
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-file-downgrade')).toContainText('older than the installed version 0.1.0')
    await shoot(page, 'install-from-file-downgrade')
    await expect(dialog.getByTestId('install-confirm')).toBeDisabled()
    await dialog.getByTestId('install-file-downgrade-confirm').check()
    await expect(dialog.getByTestId('install-confirm')).toBeEnabled()
    await dialog.getByTestId('install-confirm').click()
    await expect.poll(() => seen.install.length).toBe(1)
    expect(seen.install[0].body.downgrade).toBe(true)
    expect(seen.install[0].body.action).toBe('upgrade')
  })

  test('a deployment that locks UI host grants cannot install a file', async ({ page }) => {
    await open(page, { locked: 'no_auth' })
    await page.goto('/app/addons')
    await page.getByTestId('install-file-input').setInputFiles({ name: 'm.zip', mimeType: 'application/zip', buffer: Buffer.from('PK') })
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-file-locked')).toContainText('Sign-in is off')
    await expect(dialog.getByTestId('install-confirm')).toBeDisabled()
  })

  test('the same version cannot be installed again', async ({ page }) => {
    await open(page, { file: fileView({ installed: true, installedVersion: '0.2.0', relation: 'same' }), installed: [installedAddon({ id: 'memory', title: 'Short memory', version: '0.2.0', sourceId: 'local-file', verification: 'sha256' })] })
    await page.goto('/app/addons')
    await page.getByTestId('install-file-input').setInputFiles({ name: 'm.zip', mimeType: 'application/zip', buffer: Buffer.from('PK') })
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog.getByTestId('install-file-same')).toContainText('installed already')
    await expect(dialog.getByTestId('install-confirm')).toBeDisabled()
  })

  test('shows the reason the owner gives when it refuses the file', async ({ page }) => {
    await open(page, { uploadStatus: 400, uploadBody: { error: { type: 'wrong_platform', code: 400, message: 'This file is for linux/arm64; this host is linux/amd64' } } })
    await page.goto('/app/addons')
    await page.getByTestId('install-file-input').setInputFiles({ name: 'm.zip', mimeType: 'application/zip', buffer: Buffer.from('PK') })
    const err = page.getByTestId('install-file-error')
    await expect(err).toContainText('The file was not accepted.')
    await expect(err).toContainText('This file is for linux/arm64; this host is linux/amd64')
    await expect(page.getByTestId('install-dialog')).toHaveCount(0)
  })

  test('the link of the command line opens the dialog for the staged file, and cancel removes the file', async ({ page }) => {
    const { seen } = await open(page)
    await page.goto(`/app/addons?file=${ID}`)
    const dialog = page.getByTestId('install-dialog')
    await expect(dialog).toBeVisible()
    expect(seen.described).toEqual([ID])
    expect(seen.uploads).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    await expect.poll(() => seen.removed).toEqual([ID])
    await expect(page).not.toHaveURL(/file=/)
  })

  test('a link to a file that is gone says so', async ({ page }) => {
    await open(page)
    await page.goto(`/app/addons?file=${'e'.repeat(32)}`)
    await expect(page.getByTestId('install-file-error')).toContainText('The file is gone')
  })

  test('the list and the host access page name an add-on from a file as unsigned', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
    const installed = installedAddon({ id: 'memory', title: 'Short memory', version: '0.2.0', sourceId: 'local-file', verification: 'sha256' })
    await mockAddons(page, { apps: [{ id: 'memory', name: 'memory', title: 'Short memory', description: 'Notes.', version: '0.2.0', generation: 1, enabled: true, state: 'running', hasUI: false, tools: [], toolCount: 0, launchMode: 'always' }], installed: [installed] })
    await page.route(/\/api\/addons\/host-grants$/, route => route.fulfill({ json: {
      revision: 1, locked: false, blanket: { mode: 'off', includeOptional: false, setBy: 'ui', revision: 0 }, floor: { source: 'file', addons: [] }, deny: { file: {}, ui: {} },
      addons: [{ id: 'memory', title: 'Short memory', version: '0.2.0', state: 'running', sourceId: 'local-file', requested: { required: [], optional: [] }, ops: [], noLongerRequested: [], newOptional: [], identityMismatch: false, stopped: '' }],
    } }))
    await page.goto('/app/addons')
    await expect(page.locator('[data-source="local-file"]').first()).toContainText('Local file, unsigned')
    await page.goto('/app/addons?tab=access')
    await expect(page.getByTestId('host-local-file')).toContainText('Local file, unsigned')
  })
})
