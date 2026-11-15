import { test, expect } from './coverage-fixtures.js'
import { mockLedger, installedSeed } from './ledger-fixtures.js'
import {
  mockRemotes, remote, CONNECTED, REJECTED, UNREACHABLE, NEVER, TEST_OK, CLOUD_MODELS, CLOUD_BARE, LOCALAI_MODELS,
} from './remotes-fixtures.js'

// The remote side of the Models page: the "Connect a remote" sheet, the
// Remote filter and row marks in Installed, and the Remotes tab.

const REMOTE_ROWS = [
  { id: 'remote-chat', backend: 'cloud-proxy', capabilities: ['FLAG_CHAT'], remote: true, remote_id: 'remotes-1', upstream_state: 'ok' },
  { id: 'remote-gone', backend: 'cloud-proxy', capabilities: ['FLAG_CHAT'], remote: true, remote_id: 'remotes-1', upstream_state: 'unavailable' },
  { id: 'remote-orphan', backend: 'cloud-proxy', capabilities: ['FLAG_CHAT'], remote: true, remote_id: 'remotes-missing', upstream_state: 'ok' },
]
const seed = () => [...installedSeed().map(m => ({ ...m, remote: false, remote_id: '', upstream_state: '' })), ...REMOTE_ROWS]

const rows = (page) => page.locator('[data-testid="installed-models-rail-item"]')
const row = (page, name) => page.locator(`[data-entity="${name}"]`)
const cards = (page) => page.getByTestId('remote-card')
const card = (page, name) => cards(page).filter({ hasText: name })

async function open(page, { remotes = [CONNECTED], url = '/app/models?view=installed', installed = seed(), ...rest } = {}) {
  await mockLedger(page, { installed })
  const state = await mockRemotes(page, { remotes, ...rest })
  await page.goto(url)
  return state
}

test.describe('Models page: connect a remote', () => {
  test('the header has the button next to Add model and Import model', async ({ page }) => {
    await open(page)
    const actions = page.locator('.models-bar__actions')
    await expect(actions.getByRole('button', { name: 'Add model' })).toBeVisible()
    await expect(actions.getByRole('button', { name: 'Import model' })).toBeVisible()
    await expect(actions.getByRole('button', { name: 'Connect a remote' })).toBeVisible()
  })

  test('the button opens a sheet and Escape returns focus to it', async ({ page }) => {
    await open(page)
    const button = page.getByTestId('models-connect-remote')
    await button.click()
    const sheet = page.getByRole('dialog', { name: 'Connect a remote' })
    await expect(sheet).toBeVisible()
    await expect(sheet.getByTestId('remote-connect-step')).toBeVisible()
    await expect(sheet.getByTestId('remote-choose-empty')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    await expect(button).toBeFocused()
  })

  test('connecting from the sheet toasts, refreshes and opens the Remotes tab', async ({ page }) => {
    const state = await open(page, { remotes: [], url: '/app/models', test: TEST_OK(CLOUD_MODELS) })
    await page.getByTestId('models-connect-remote').click()
    const sheet = page.getByRole('dialog', { name: 'Connect a remote' })
    await sheet.getByTestId('remote-kind-anthropic').click()
    await sheet.getByTestId('remote-key').fill('key-abc')
    await sheet.getByTestId('remote-test').click()
    await expect(sheet.getByTestId('remote-ok')).toContainText('Found 3 models')
    await sheet.getByTestId('remote-models').getByRole('checkbox').first().check()
    await sheet.getByTestId('remote-connect').click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page).toHaveURL(/view=remotes/)
    await expect(page.getByText('Connected Anthropic. Added 1 model.')).toBeVisible()
    await expect(cards(page)).toHaveCount(1)
    await expect(card(page, 'Anthropic')).toBeVisible()
    expect(state.calls.filter(c => c.method === 'GET').length).toBeGreaterThan(1)
  })
})

test.describe('Models page: the sheet shows what a connect left out', () => {
  const connect = async (page, options) => {
    const state = await open(page, { remotes: [], url: '/app/models', test: TEST_OK(LOCALAI_MODELS), ...options })
    await page.getByTestId('models-connect-remote').click()
    const sheet = page.getByRole('dialog', { name: 'Connect a remote' })
    await sheet.getByTestId('remote-url').fill('https://office.example.com')
    await sheet.getByTestId('remote-test').click()
    await sheet.getByTestId('remote-connect').click()
    return { state, sheet }
  }
  const made = (body, over = {}) => remote({ id: 'remotes-new', name: body.name, entries: ['chat-large'], ...over })

  test('skipped models are listed in the sheet, and the toast carries the count and the first ids', async ({ page }) => {
    const { sheet } = await connect(page, {
      create: ({ body }) => ({ json: { remote: made(body), created: ['chat-large', 'chat-small'], skipped: [{ id: 'embed-base', reason: 'no free model name' }, { id: 'speech-to-text', reason: 'could not build the entry' }] } }),
    })
    await expect(sheet.getByTestId('remote-outcome')).toBeVisible()
    await sheet.getByTestId('remote-skipped').getByText('2 models not added').click()
    await expect(sheet.getByTestId('remote-skipped-item')).toHaveCount(2)
    await expect(sheet.getByTestId('remote-skipped')).toContainText('No free model name')
    await expect(sheet.getByTestId('remote-skipped')).toContainText('Could not prepare the model')
    await sheet.getByTestId('remote-continue').click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByText('Connected office.example.com. Added 2 models.')).toBeVisible()
    await expect(page.getByText(/2 models were not added\. embed-base, speech-to-text/)).toBeVisible()
    await expect(page).toHaveURL(/view=remotes/)
  })

  test('a warning shows in the sheet and Adopt models adds the rest without a second create', async ({ page }) => {
    const { state, sheet } = await connect(page, {
      create: ({ body }) => ({ json: { remote: made(body), created: ['chat-large'], skipped: [{ id: 'chat-small', reason: 'could not write the entry' }], warning: 'The remote was saved, but some models could not be added.' } }),
      adopt: ({ body }) => ({ json: { remote: made({ name: 'office.example.com' }, { entries: ['chat-large', ...body.models.map(m => m.id)] }), created: body.models.map(m => m.id), skipped: [] } }),
    })
    await expect(sheet.getByTestId('remote-warning')).toContainText('The remote is saved, but some models were not added.')
    await expect(sheet.getByTestId('remote-warning')).toContainText('Adopt the missing models')
    await expect(sheet.getByTestId('remote-connect')).toHaveCount(0)
    await sheet.getByTestId('remote-adopt').click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(state.calls.filter(c => c.method === 'POST' && c.path === '/')).toHaveLength(1)
    expect(state.calls.filter(c => c.path === '/remotes-new/adopt')).toHaveLength(1)
    await expect(page.getByText('Connected office.example.com. Added 5 models.')).toBeVisible()
  })

  test('Continue leaves a partial result as saved', async ({ page }) => {
    const { state, sheet } = await connect(page, {
      create: ({ body }) => ({ json: { remote: made(body), created: ['chat-large'], skipped: [{ id: 'chat-small', reason: 'could not write the entry' }], warning: 'partial' } }),
    })
    await sheet.getByTestId('remote-continue').click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page).toHaveURL(/view=remotes/)
    await expect(card(page, 'office.example.com')).toBeVisible()
    expect(state.calls.some(c => c.path === '/remotes-new/adopt')).toBe(false)
  })
})

test.describe('Installed: remote models', () => {
  test('the Remote filter counts the remote rows and filters to them', async ({ page }) => {
    await open(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    const tab = (name) => page.getByRole('tab', { name: new RegExp(`^${name}`) })
    await expect(tab('Remote')).toContainText('3')
    // After Distributed.
    const names = await page.locator('.ledger-states [role="tab"]').allTextContents()
    expect(names.findIndex(n => n.startsWith('Remote'))).toBe(names.findIndex(n => n.startsWith('Distributed')) + 1)
    await tab('Remote').click()
    await expect(page).toHaveURL(/state=remote/)
    await expect(rows(page)).toHaveCount(3)
    await expect(tab('Remote')).toHaveAttribute('aria-selected', 'true')
  })

  test('a remote row has a mark, its remote, "Remote" for a size, and no Load or Stop', async ({ page }) => {
    await open(page, { url: '/app/models?view=installed&state=remote' })
    const r = row(page, 'remote-chat')
    await expect(r).toBeVisible({ timeout: 15_000 })
    await expect(r.getByRole('img', { name: 'Remote model' })).toBeVisible()
    await expect(r.locator('.dk-table-sub')).toHaveText('cloud-proxy · Office server')
    await expect(r.locator('.ledger-size')).toHaveText('Remote')
    await expect(r.getByRole('button', { name: 'Load', exact: true })).toHaveCount(0)
    await expect(r.getByRole('button', { name: 'Stop', exact: true })).toHaveCount(0)
    // The menu stays.
    await r.getByRole('button', { name: 'Actions for remote-chat' }).click()
    const menu = page.getByRole('menu')
    for (const name of ['Edit configuration', 'Backend logs', 'Delete model']) await expect(menu.getByRole('menuitem', { name })).toBeVisible()
  })

  test('a row whose remote is gone keeps the backend alone', async ({ page }) => {
    await open(page, { url: '/app/models?view=installed&state=remote' })
    await expect(row(page, 'remote-orphan').locator('.dk-table-sub')).toHaveText('cloud-proxy')
  })

  test('a model the remote stopped listing says so with a warning dot, and still counts under All', async ({ page }) => {
    await open(page)
    const r = row(page, 'remote-gone')
    await expect(r).toBeVisible({ timeout: 15_000 })
    await expect(r.locator('.ledger-state')).toHaveAttribute('data-state', 'unavailable')
    await expect(r.locator('.ledger-state')).toContainText('Unavailable upstream')
    await expect(r.locator('.dk-dot--warn')).toHaveCount(1)
    await expect(page.getByRole('tab', { name: /^All/ })).toContainText('16')
  })

  test('a local model still has Load', async ({ page }) => {
    await open(page)
    await expect(row(page, 'kokoro-82m').getByRole('button', { name: 'Load', exact: true })).toBeVisible({ timeout: 15_000 })
    await expect(row(page, 'kokoro-82m').getByRole('img', { name: 'Remote model' })).toHaveCount(0)
  })
})

test.describe('Remotes tab', () => {
  test('the tab shows the count and a card for each remote', async ({ page }) => {
    await open(page, { remotes: [CONNECTED, REJECTED], url: '/app/models?view=remotes' })
    await expect(cards(page)).toHaveCount(2, { timeout: 15_000 })
    await expect(page.getByTestId('models-tab-remotes')).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('models-tab-remotes')).toContainText('2')
    await page.getByRole('link', { name: /^Installed/ }).click()
    await expect(page).toHaveURL(/view=installed/)
    await page.getByTestId('models-tab-remotes').click()
    await expect(page).toHaveURL(/view=remotes/)
  })

  test('a connected card shows name, kind, host, status and its models', async ({ page }) => {
    await open(page, { remotes: [remote({ url: 'https://user:secret@office.example.com:8443/v1?x=1' })], url: '/app/models?view=remotes' })
    const c = cards(page).first()
    await expect(c).toBeVisible({ timeout: 15_000 })
    await expect(c.getByRole('heading', { name: 'Office server' })).toBeVisible()
    await expect(c).toContainText('LocalAI')
    await expect(c).toContainText('office.example.com:8443')
    await expect(c).not.toContainText('secret')
    await expect(c.getByTestId('remote-status')).toContainText('Connected · refreshed')
    await expect(c.getByTestId('remote-status')).toContainText('12 minutes ago')
    await expect(c.locator('.dk-dot--ok')).toHaveCount(1)
    await expect(c).toContainText('2 models from this remote')
    await expect(c.getByTestId('remote-new')).toHaveCount(0)
  })

  test('never refreshed reads as such', async ({ page }) => {
    await open(page, { remotes: [NEVER], url: '/app/models?view=remotes' })
    await expect(cards(page).first().getByTestId('remote-status')).toHaveText('Not refreshed yet')
  })

  test('new models wait in a banner; Add selected adopts the ticked ones', async ({ page }) => {
    const state = await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    const banner = page.getByTestId('remote-new')
    await expect(banner).toBeVisible({ timeout: 15_000 })
    await expect(banner).toContainText('2 new models on this remote')
    const boxes = banner.getByRole('checkbox')
    await expect(boxes).toHaveCount(2)
    await expect(boxes.nth(0)).toBeChecked()
    await boxes.nth(1).uncheck()
    await page.getByTestId('remote-new-add').click()
    await expect.poll(() => state.calls.find(c => c.path === '/remotes-1/adopt')?.body).toEqual({ models: [{ id: 'chat-small', usecases: ['chat'] }] })
    await expect(page.getByText('Added 1 model from Office server.')).toBeVisible()
    // The one left unticked still waits.
    await expect(page.getByTestId('remote-new')).toContainText('1 new model on this remote')
    await expect(page.getByTestId('remote-new').getByRole('checkbox')).toHaveCount(1)
  })

  test('Not now hides the banner for this session only', async ({ page }) => {
    await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    await expect(page.getByTestId('remote-new')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('remote-new-skip').click()
    await expect(page.getByTestId('remote-new')).toHaveCount(0)
    await page.reload()
    await expect(page.getByTestId('remote-new')).toBeVisible({ timeout: 15_000 })
  })

  test('a rejected key shows the error and Update key opens the edit sheet', async ({ page }) => {
    await open(page, { remotes: [REJECTED], url: '/app/models?view=remotes' })
    const c = cards(page).first()
    await expect(c.getByTestId('remote-status')).toHaveText('Key rejected', { timeout: 15_000 })
    await expect(c.locator('.dk-dot--error')).toHaveCount(1)
    const err = c.getByTestId('remote-error')
    await expect(err).toContainText('The remote rejected the key.')
    await expect(err).toContainText('invalid key')
    await err.getByRole('button', { name: 'Update key' }).click()
    await expect(page.getByRole('dialog', { name: 'Edit remote' })).toBeVisible()
  })

  test('an unreachable remote shows the error', async ({ page }) => {
    await open(page, { remotes: [UNREACHABLE], url: '/app/models?view=remotes' })
    const c = cards(page).first()
    await expect(c.getByTestId('remote-status')).toHaveText('Unreachable', { timeout: 15_000 })
    await expect(c.getByTestId('remote-error')).toContainText('connection refused')
    await expect(c).toContainText('1 no longer listed')
  })

  test('Refresh posts and keeps the card', async ({ page }) => {
    const state = await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    await page.getByTestId('remote-refresh').click()
    await expect.poll(() => state.calls.some(c => c.method === 'POST' && c.path === '/remotes-1/refresh')).toBe(true)
    await expect(cards(page)).toHaveCount(1)
    await expect(page.getByTestId('remote-refresh')).not.toHaveAttribute('aria-busy', 'true')
  })

  test('View in Installed opens Installed with the Remote filter', async ({ page }) => {
    await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    await page.getByTestId('remote-view').click()
    await expect(page).toHaveURL(/view=installed/)
    await expect(page).toHaveURL(/state=remote/)
    await expect(page.getByRole('tab', { name: /^Remote/ })).toHaveAttribute('aria-selected', 'true')
  })

  test('with no remote the tab says so and offers the sheet', async ({ page }) => {
    await open(page, { remotes: [], url: '/app/models?view=remotes' })
    await expect(page.getByText('No remotes connected')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('remotes-empty-connect').click()
    await expect(page.getByRole('dialog', { name: 'Connect a remote' })).toBeVisible()
  })

  test('a list that cannot load shows an error with Try again, not the empty state', async ({ page }) => {
    await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes', status: { 'GET /': { status: 500, message: 'boom' } } })
    await expect(page.getByTestId('load-error')).toContainText('Could not load your remotes.', { timeout: 15_000 })
    await expect(page.getByText('No remotes connected')).toHaveCount(0)
  })
})

test.describe('Remotes tab: a remote that answers but gives nothing to use', () => {
  const EMPTY = remote({ id: 'remotes-9', name: 'Empty box', entries: [], new: [] })
  const ERRORED = remote({ id: 'remotes-8', name: 'Odd box', last_error: 'The server answered with a list this server cannot read.' })

  test('the card shows a warning state, not the green one', async ({ page }) => {
    await open(page, { remotes: [EMPTY, ERRORED], url: '/app/models?view=remotes' })
    const c = card(page, 'Empty box')
    await expect(c).toBeVisible({ timeout: 15_000 })
    await expect(c).toHaveAttribute('data-warning', 'true')
    await expect(c.getByTestId('remote-status')).toHaveText('Connected · no usable models')
    await expect(c.locator('.dk-dot--warn')).toHaveCount(1)
    await expect(c.locator('.dk-dot--ok')).toHaveCount(0)
    await expect(c.getByTestId('remote-no-usable')).toContainText('No usable models.')
    await expect(c.getByTestId('remote-no-usable')).toContainText('lists no models this server can use')
    // An error text from the refresh is shown as it is.
    await expect(card(page, 'Odd box').getByTestId('remote-no-usable')).toContainText('a list this server cannot read')
    // A healthy remote has none of it.
    await expect(page.getByTestId('remote-no-usable')).toHaveCount(2)
  })

  test('a refresh that works but leaves nothing to use toasts a warning and shows the state', async ({ page }) => {
    await open(page, {
      remotes: [CONNECTED], url: '/app/models?view=remotes',
      refresh: () => ({ json: { entries: [], new: [], unavailable: [], last_error: '' } }),
    })
    await page.getByTestId('remote-refresh').click()
    await expect(page.getByText('Refreshed Office server, but it has no usable models.')).toBeVisible()
    await expect(page.getByTestId('remote-no-usable')).toBeVisible()
    await expect(page.getByTestId('remote-status')).toHaveText('Connected · no usable models')
  })

  test('the edit sheet shows the same warning', async ({ page }) => {
    await open(page, { remotes: [EMPTY], url: '/app/models?view=remotes' })
    await page.getByTestId('remote-edit').click()
    const sheet = page.getByRole('dialog', { name: 'Edit remote' })
    await expect(sheet.getByTestId('remote-no-usable')).toContainText('No usable models.')
  })

  test('a new key that still leaves nothing to use says so after saving', async ({ page }) => {
    await open(page, { remotes: [EMPTY], url: '/app/models?view=remotes' })
    await page.getByTestId('remote-edit').click()
    const sheet = page.getByRole('dialog', { name: 'Edit remote' })
    await sheet.getByTestId('edit-key').fill('new-key')
    await sheet.getByTestId('edit-save').click()
    await expect(page.getByText('Saved Empty box, but it still has a problem.')).toBeVisible()
  })

  test('adding models from the banner shows the skipped ones and their reasons on the card', async ({ page }) => {
    await open(page, {
      remotes: [CONNECTED], url: '/app/models?view=remotes',
      adopt: () => ({ json: { remote: CONNECTED, created: ['chat-small'], skipped: [{ id: 'text-to-speech', reason: 'a model with that name exists' }], warning: 'Some models could not be added.' } }),
    })
    await page.getByTestId('remote-new-add').click()
    const note = page.getByTestId('remote-added-note')
    await expect(note).toContainText('Some models were not added.')
    await note.getByText('1 model not added').click()
    await expect(note).toContainText('text-to-speech')
    await expect(note).toContainText('A model with that name exists')
    await expect(page.getByText(/1 model not added: text-to-speech/)).toBeVisible()
  })

  test('cloud models of the banner carry the Chat tag', async ({ page }) => {
    const cloud = remote({ id: 'remotes-7', name: 'Hosted', kind: 'openai', url: 'https://api.example.com/v1', entries: [], new: CLOUD_BARE })
    const state = await open(page, { remotes: [cloud], url: '/app/models?view=remotes' })
    const banner = page.getByTestId('remote-new')
    await expect(banner.locator('li').first()).toContainText('Chat', { timeout: 15_000 })
    await banner.getByRole('checkbox').first().check()
    await page.getByTestId('remote-new-add').click()
    await expect.poll(() => state.calls.find(c => c.path === '/remotes-7/adopt')?.body).toEqual({ models: [{ id: 'cloud-model-a', usecases: ['chat'] }] })
  })

  test('a LocalAI remote whose key is rejected asks for a key that can list models', async ({ page }) => {
    const rejected = remote({ id: 'remotes-6', name: 'Locked box', status: 'auth_failed', last_error: '' })
    await open(page, { remotes: [rejected], url: '/app/models?view=remotes' })
    await expect(page.getByTestId('remote-error')).toContainText('Use a key that can list the models of that server', { timeout: 15_000 })
  })
})

test.describe('A server without the remote routes', () => {
  const missing = { 'GET /': { status: 404, message: 'Not Found' } }

  test('the tab and the button are not offered', async ({ page }) => {
    await open(page, { remotes: [], url: '/app/models', status: missing })
    await expect(page.locator('.models-bar__actions').getByRole('button', { name: 'Import model' })).toBeVisible()
    await expect(page.getByTestId('models-connect-remote')).toHaveCount(0)
    await expect(page.getByTestId('models-tab-remotes')).toHaveCount(0)
  })

  test('a link to the tab explains instead of showing an error', async ({ page }) => {
    await open(page, { remotes: [], url: '/app/models?view=remotes', status: missing })
    await expect(page.getByTestId('remotes-unsupported')).toContainText('Remotes are not available', { timeout: 15_000 })
    await expect(page.getByTestId('remotes-unsupported')).toContainText('runs without the model gallery')
    await expect(page.getByTestId('load-error')).toHaveCount(0)
    await expect(page.getByText('Not Found')).toHaveCount(0)
  })
})

test.describe('Edit and remove', () => {
  const edit = async (page) => {
    await page.getByTestId('remote-edit').click()
    return page.getByRole('dialog', { name: 'Edit remote' })
  }

  test('the key is write-only: the field is empty, the hint says a key is set, empty keeps it', async ({ page }) => {
    const state = await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    const sheet = await edit(page)
    await expect(sheet.getByTestId('edit-key')).toHaveValue('')
    await expect(sheet.getByTestId('edit-key')).toHaveAttribute('type', 'password')
    await expect(sheet.getByTestId('edit-key-hint')).toContainText('A key is set')
    await expect(sheet.getByTestId('edit-save')).toBeDisabled()
    await sheet.getByTestId('edit-name').fill('Main server')
    await sheet.getByTestId('edit-save').click()
    await expect(page.getByText('Saved Main server.')).toBeVisible()
    const patch = state.calls.find(c => c.method === 'PATCH')
    expect(patch.path).toBe('/remotes-1')
    expect(patch.body).toEqual({ name: 'Main server' })
    await expect(card(page, 'Main server')).toBeVisible()
  })

  test('a new key is sent and says the loaded models restart', async ({ page }) => {
    const state = await open(page, { remotes: [REJECTED], url: '/app/models?view=remotes' })
    await cards(page).first().getByRole('button', { name: 'Update key' }).click()
    const sheet = page.getByRole('dialog', { name: 'Edit remote' })
    await sheet.getByTestId('edit-key').fill('new-key-value')
    await sheet.getByTestId('edit-save').click()
    await expect(page.getByText(/restart with the new key/)).toBeVisible()
    expect(state.calls.find(c => c.method === 'PATCH').body).toEqual({ api_key: 'new-key-value' })
  })

  test('a name that is taken stays in the sheet with a message', async ({ page }) => {
    await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes', status: { 'PATCH /remotes-1': { status: 409, message: 'name taken' } } })
    const sheet = await edit(page)
    await sheet.getByTestId('edit-name').fill('Taken')
    await sheet.getByTestId('edit-save').click()
    await expect(sheet.getByTestId('edit-error')).toContainText('already exists')
  })

  test('Anthropic has no address field', async ({ page }) => {
    await open(page, { remotes: [NEVER], url: '/app/models?view=remotes' })
    const sheet = await edit(page)
    await expect(sheet.getByTestId('edit-url')).toHaveCount(0)
  })

  test('removing asks what happens to the models and keeps them by default', async ({ page }) => {
    const state = await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    const sheet = await edit(page)
    await sheet.getByTestId('edit-remove').click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Remove Office server?')
    await expect(dialog.getByTestId('remove-choice-keep')).toHaveAttribute('aria-checked', 'true')
    await expect(dialog.getByTestId('remove-explain')).toContainText('model editor')
    await dialog.getByRole('button', { name: 'Remove remote' }).click()
    await expect.poll(() => state.calls.find(c => c.method === 'DELETE')?.query).toEqual({ entries: 'keep' })
    await expect(page.getByText('Removed Office server. Its models stay.')).toBeVisible()
    await expect(page.getByText('No remotes connected')).toBeVisible()
  })

  test('removing the models too sends entries=remove, chosen with the keyboard', async ({ page }) => {
    const state = await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    const sheet = await edit(page)
    await sheet.getByTestId('edit-remove').click()
    const dialog = page.getByRole('alertdialog')
    await dialog.getByTestId('remove-choice-keep').focus()
    await page.keyboard.press('ArrowRight')
    await expect(dialog.getByTestId('remove-choice-remove')).toHaveAttribute('aria-checked', 'true')
    await expect(dialog.getByTestId('remove-explain')).toContainText('cannot be undone')
    await dialog.getByRole('button', { name: 'Remove remote' }).click()
    await expect.poll(() => state.calls.find(c => c.method === 'DELETE')?.query).toEqual({ entries: 'remove' })
    await expect(page.getByText('Removed Office server and its models.')).toBeVisible()
  })

  test('cancelling the removal deletes nothing', async ({ page }) => {
    const state = await open(page, { remotes: [CONNECTED], url: '/app/models?view=remotes' })
    const sheet = await edit(page)
    await sheet.getByTestId('edit-remove').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    expect(state.calls.some(c => c.method === 'DELETE')).toBe(false)
    await expect(sheet).toBeVisible()
  })
})

test.describe('Remotes on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the tab, a card with errors and the sheet fit the screen', async ({ page }) => {
    await open(page, { remotes: [CONNECTED, REJECTED, UNREACHABLE], url: '/app/models?view=remotes' })
    await expect(cards(page)).toHaveCount(3, { timeout: 15_000 })
    const over = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(await over()).toBeLessThanOrEqual(0)
    await page.getByTestId('models-connect-remote').click()
    const sheet = page.getByRole('dialog', { name: 'Connect a remote' })
    await expect(sheet).toBeVisible()
    await sheet.getByTestId('remote-url').fill('https://office.example.com')
    await sheet.getByTestId('remote-test').click()
    await expect(sheet.getByTestId('remote-ok')).toBeVisible()
    expect(await over()).toBeLessThanOrEqual(0)
    const box = await sheet.boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
  })

  test('the Installed list with remote rows fits the screen', async ({ page }) => {
    await open(page, { url: '/app/models?view=installed&state=remote' })
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)
  })
})

// Remotes are not part of the model gallery. A server started with the gallery
// endpoints off answers 404 on the gallery routes and still serves /api/remotes.
test.describe('A server with the gallery endpoints off', () => {
  const galleryOff = async (page) => {
    for (const pattern of ['**/models/available*', '**/models/galleries', '**/models/jobs*', '**/api/models/search*']) {
      await page.route(pattern, route => route.fulfill({ status: 404, json: { error: 'not found' } }))
    }
  }

  test('the Remotes tab, the list and the Connect button still show', async ({ page }) => {
    await galleryOff(page)
    await open(page, { url: '/app/models?view=remotes' })
    await expect(page.getByTestId('models-tab-remotes')).toBeVisible()
    await expect(page.getByTestId('models-connect-remote')).toBeVisible()
    await expect(cards(page)).toHaveCount(1, { timeout: 15_000 })
    await expect(page.getByTestId('models-tab-remotes')).toHaveAttribute('aria-current', 'page')
  })

  test('the Installed tab is still there next to Remotes', async ({ page }) => {
    await galleryOff(page)
    await open(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('models-tab-remotes')).toBeVisible()
    await expect(page.getByTestId('models-connect-remote')).toBeVisible()
  })
})
