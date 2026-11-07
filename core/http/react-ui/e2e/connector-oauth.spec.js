import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { ADMIN, MEMBER, app, mockAddons, sessionFor } from './addons-fixtures.js'

// Connections that log in with OAuth: the row of each state on the Connectors
// page, the start that sends the browser to the service, the result of a login
// the page comes back to, the administrator's approval form on the add-on's
// Settings tab, and the Reconnect link of the chat Tools menu. The add-on and
// the service are stand-ins (providerA, example.test): the host names no
// service, and every label comes from the API. The API is stubbed.

const SHOTS = process.env.OAUTH_SHOTS_DIR
const WIDTHS = { desktop: { width: 1280, height: 860 }, phone: { width: 390, height: 844 } }

// Screenshots for review, in each theme and width asked for. The theme is
// switched in place, so the state on the page stays as it is. `tall` makes the
// window tall enough for a pane that scrolls on its own to show all of it.
async function shoot(page, name, { themes = ['light', 'dark'], widths = ['desktop', 'phone'], locator, tall = false } = {}) {
  if (!SHOTS) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  for (const width of widths) {
    await page.setViewportSize(tall ? { ...WIDTHS[width], height: width === 'phone' ? 2300 : 1500 } : WIDTHS[width])
    for (const theme of themes) {
      await page.evaluate((t) => { localStorage.setItem('localai-theme', t); document.documentElement.setAttribute('data-theme', t) }, theme)
      await page.waitForTimeout(300)
      const target = locator ? page.locator(locator).first() : page
      await target.screenshot({ path: `${SHOTS}/${name}-${theme}-${width}.png`, ...(locator ? {} : { fullPage: true }) })
    }
  }
  await page.setViewportSize(WIDTHS.desktop)
  await page.evaluate(() => { localStorage.setItem('localai-theme', 'light'); document.documentElement.setAttribute('data-theme', 'light') })
}

const LOGIN = { requiredScopes: ['read'], optionalScopes: ['write', 'admin'] }
const PROVIDER = app({
  id: 'providera', name: 'providera', title: 'Provider A', description: 'Read items and propose changes.', icon: undefined,
  mainPage: undefined, hasUI: false, tools: ['list', 'change'], toolCount: 2, credentialed: true, oauth: LOGIN,
})
const KIND = { addon: 'providera', kind: 'providera', title: 'Provider A', description: 'Read items and propose changes in your Provider A account.', agent_optional: true, auth_kind: 'oauth2', files: {} }
const binding = (oauth, over = {}) => ({
  id: 'b1', name: 'Work account', kind: 'providera', addon: 'providera', agent_id: '', credentials_only: true, enabled: true, accept_any: false,
  allow: [], has_token: false, auth_kind: 'oauth2', oauth: { scopes: [], has_client: true, ...oauth }, ...over,
})
const CONNECTED = { state: 'connected', account_label: 'ana@example.test', scopes: ['read', 'write'], expires_at: '2026-11-04T18:30:00Z' }

async function signIn(page, user) {
  await page.route('**/api/auth/status', (route) => route.fulfill({ json: sessionFor(user, { addons: true, connectors: true, chats: true }) }))
}

// The connectors API, with the login routes. `state.calls` records what the
// page sent.
async function connectors(page, { items = [binding({ state: 'not_connected' })], start, disconnect, create, kinds = [KIND] } = {}) {
  const state = { items: structuredClone(items), calls: [] }
  await page.route('**/api/chats/agents*', (r) => r.fulfill({ json: { agents: [] } }))
  await page.route(/\/api\/chats\/connectors\/kinds$/, (r) => r.fulfill({ json: { kinds } }))
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, (r) => {
    if (r.request().method() !== 'POST') return r.fulfill({ json: { connectors: state.items } })
    const body = r.request().postDataJSON()
    state.calls.push({ op: 'create', body })
    if (create) return r.fulfill(create)
    const made = binding({ state: 'not_connected' }, { id: `b${state.items.length + 1}`, name: body.name })
    state.items = [...state.items, made]
    return r.fulfill({ status: 201, json: made })
  })
  await page.route(/\/api\/chats\/connectors\/[^/]+\/oauth\/start$/, (r) => {
    state.calls.push({ op: 'start', body: r.request().postDataJSON() })
    if (start) return r.fulfill(start)
    return r.fulfill({ json: { authorize_url: 'https://auth.example.test/authorize?client_id=x&state=y', expires: '2026-11-04T10:10:00Z' } })
  })
  await page.route(/\/api\/chats\/connectors\/[^/]+\/oauth\/disconnect$/, (r) => {
    state.calls.push({ op: 'disconnect' })
    state.items = state.items.map((c) => ({ ...c, oauth: { state: 'not_connected', scopes: [], has_client: true } }))
    return r.fulfill({ json: { revoked: disconnect?.revoked ?? true } })
  })
  await page.route(/\/api\/chats(\?.*)?$/, (r) => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
  await page.route('**/api/chats/notifications*', (r) => r.fulfill({ json: { notifications: [], unread: 0 } }))
  return state
}

async function openConnectors(page, { user = ADMIN, path = '/app/chats/connectors', ...rest } = {}) {
  await mockChat(page)
  await signIn(page, user)
  await mockAddons(page, { apps: [PROVIDER] })
  const state = await connectors(page, rest)
  await page.goto(path)
  await expect(page.getByTestId('oauth-connection')).toBeVisible()
  return state
}

test.describe('Adding a connection that logs in with OAuth', () => {
  async function open(page, rest = {}) {
    await mockChat(page)
    await signIn(page, MEMBER)
    await mockAddons(page, { apps: [PROVIDER] })
    const state = await connectors(page, { items: [], ...rest })
    await page.goto('/app/chats/connectors')
    await expect(page.getByTestId('connector-group').first()).toBeVisible()
    return state
  }

  test('adds a connection with a name only, then offers Connect on its row', async ({ page }) => {
    const state = await open(page)
    const group = page.getByTestId('connector-group')
    await expect(group).toContainText('No connections yet. Add one, then connect your account.')
    await expect(group.getByRole('link', { name: 'Edit connections' })).toBeVisible()
    await shoot(page, 'add-empty')
    await group.getByTestId('oauth-add-open').click()
    const form = group.getByTestId('oauth-add-form')
    await expect(form.getByRole('heading')).toHaveText('New Provider A connection')
    const name = form.getByLabel('Name')
    await expect(name).toBeFocused()
    await expect(form.getByTestId('oauth-add-submit')).toBeDisabled()
    await name.fill('  Work account  ')
    await shoot(page, 'add-form')
    await form.getByTestId('oauth-add-submit').click()
    await expect(page.getByTestId('oauth-notice')).toHaveText(/Work account is added\. Select Connect to log in to your account\./)
    await expect(form).toBeHidden()
    const row = page.getByTestId('connector-row')
    await expect(row).toHaveAttribute('data-auth', 'oauth2')
    await expect(row.getByTestId('oauth-connection')).toHaveAttribute('data-state', 'not_connected')
    await expect(row.getByTestId('oauth-connect')).toBeVisible()
    await shoot(page, 'add-done')
    expect(state.calls).toEqual([{ op: 'create', body: { kind: 'providera', name: 'Work account', auth_kind: 'oauth2' } }])
  })

  test('shows why the server refused the name, and Cancel closes the form', async ({ page }) => {
    const message = 'you already have a connection named "Work" for this add-on; choose another name'
    const state = await open(page, { create: { status: 400, json: { error: { message, type: 'invalid_request_error', code: 400 } } } })
    const group = page.getByTestId('connector-group')
    await group.getByTestId('oauth-add-open').click()
    await group.getByLabel('Name').fill('Work')
    await group.getByTestId('oauth-add-submit').click()
    await expect(group.getByTestId('oauth-add-error')).toHaveText(message)
    await shoot(page, 'add-refused', { widths: ['desktop', 'phone'] })
    await group.getByTestId('oauth-add-cancel').click()
    await expect(group.getByTestId('oauth-add-form')).toBeHidden()
    await expect(group.getByTestId('oauth-add-open')).toBeVisible()
    expect(state.calls).toHaveLength(1)
  })

  test('a kind whose add-on takes a token, or needs an agent, offers no form here', async ({ page }) => {
    await open(page, { kinds: [{ ...KIND, auth_kind: undefined }, { ...KIND, addon: 'agentful', kind: 'agentful', title: 'Agentful', agent_optional: false }] })
    // The kind that holds a token is on Accounts, the one that needs an agent on Messaging.
    await expect(page.getByTestId('connector-group')).toHaveCount(1)
    await expect(page.getByTestId('oauth-add-open')).toHaveCount(0)
    await expect(page.getByTestId('connector-group').first()).toContainText('No connectors yet.')
    await page.getByTestId('connections-tab-messaging').click()
    await expect(page.getByTestId('connector-group')).toHaveCount(1)
    await expect(page.getByTestId('connector-group').first()).toContainText('Agentful')
    await expect(page.getByTestId('oauth-add-open')).toHaveCount(0)
  })
})

test.describe('Connections that log in with OAuth', () => {
  test('not connected: Connect starts the login with the ticked scopes and sends the browser to the service', async ({ page }) => {
    const state = await openConnectors(page)
    const row = page.getByTestId('oauth-connection')
    await expect(row).toHaveAttribute('data-state', 'not_connected')
    await expect(row).toContainText('Not connected yet.')
    const scopes = row.getByTestId('oauth-scopes')
    await expect(scopes).toContainText('Also allow:')
    await expect(scopes.getByRole('checkbox')).toHaveCount(2)
    await expect(scopes).not.toContainText('read')
    await shoot(page, 'user-not-connected')
    await scopes.getByRole('checkbox', { name: 'write' }).check()
    // The start is held, so the page shows its busy state until the answer.
    let release
    const held = new Promise((resolve) => { release = resolve })
    await page.route(/\/api\/chats\/connectors\/[^/]+\/oauth\/start$/, async (r) => { await held; return r.fallback() })
    await page.route('https://auth.example.test/**', (r) => r.fulfill({ contentType: 'text/html', body: '<h1>Sign in at the service</h1>' }))
    await row.getByTestId('oauth-connect').click()
    await expect(row.getByTestId('oauth-connect')).toHaveAttribute('aria-busy', 'true')
    await shoot(page, 'user-starting', { themes: ['light'], widths: ['desktop'], locator: '[data-testid="connector-group"]' })
    release()
    await expect(page).toHaveURL(/^https:\/\/auth\.example\.test\/authorize\?client_id=x&state=y$/)
    expect(state.calls).toEqual([{ op: 'start', body: { scopes: ['write'], return_to: '/app/chats/connectors' } }])
  })

  test('connected: who, the access and the expiry; Disconnect asks first and says the service was told', async ({ page }) => {
    const state = await openConnectors(page, { items: [binding(CONNECTED)] })
    const row = page.getByTestId('oauth-connection')
    await expect(row).toHaveAttribute('data-state', 'connected')
    await expect(row.getByTestId('oauth-account')).toHaveText('Connected as ana@example.test')
    await expect(row.getByTestId('oauth-granted')).toHaveText('Access: read, write')
    await expect(row.getByTestId('oauth-expires')).toContainText('Expires')
    await shoot(page, 'user-connected')
    await row.getByTestId('oauth-disconnect').click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Disconnect Work account?')
    await shoot(page, 'user-disconnect-confirm', { widths: ['desktop'] })
    await dialog.getByRole('button', { name: 'Disconnect' }).click()
    await expect(page.getByTestId('oauth-notice')).toContainText('The service revoked the login.')
    await expect(page.getByTestId('oauth-connection')).toHaveAttribute('data-state', 'not_connected')
    expect(state.calls).toEqual([{ op: 'disconnect' }])
  })

  test('a disconnect the service did not confirm says so', async ({ page }) => {
    await openConnectors(page, { items: [binding(CONNECTED)], disconnect: { revoked: false } })
    await page.getByTestId('oauth-disconnect').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Disconnect' }).click()
    const notice = page.getByTestId('oauth-notice')
    await expect(notice).toHaveAttribute('data-tone', 'warn')
    await expect(notice).toContainText('did not confirm that it revoked the login')
    await shoot(page, 'user-disconnected-not-told', { widths: ['desktop'] })
  })

  test('reconnect needed: a warning with the reason in words and Reconnect', async ({ page }) => {
    const state = await openConnectors(page, { items: [binding({ state: 'reconnect_needed', reason: 'revoked', scopes: ['read', 'write'] })] })
    const callout = page.getByTestId('oauth-reconnect')
    await expect(callout).toContainText('Connect again.')
    await expect(callout).toContainText('The service ended the login, or the access was revoked.')
    await expect(callout).not.toContainText('revoked"')
    await expect(page.getByTestId('oauth-scopes').getByRole('checkbox', { name: 'write' })).toBeChecked()
    await shoot(page, 'user-reconnect-needed')
    await page.route('https://auth.example.test/**', (r) => r.fulfill({ contentType: 'text/html', body: '<h1>Sign in</h1>' }))
    await page.getByTestId('oauth-connect').click()
    await expect(page).toHaveURL(/auth\.example\.test/)
    expect(state.calls[0].body).toEqual({ scopes: ['write'], return_to: '/app/chats/connectors' })
  })

  test('not approved: a person who is not an administrator is told to wait for one', async ({ page }) => {
    await openConnectors(page, { user: MEMBER, items: [binding({ state: 'needs_approval', has_client: false })] })
    const callout = page.getByTestId('oauth-approval')
    await expect(callout).toHaveText('An administrator must approve this add-on first.')
    await expect(page.getByTestId('oauth-approval-link')).toHaveCount(0)
    await expect(page.getByTestId('oauth-connect')).toHaveCount(0)
    await shoot(page, 'user-not-approved-member')
  })

  test('not approved: an administrator gets a link to the approval form', async ({ page }) => {
    await openConnectors(page, { items: [binding({ state: 'needs_approval', has_client: false })] })
    await expect(page.getByTestId('oauth-approval')).toContainText('Nobody can connect until you approve the login of this add-on.')
    await expect(page.getByTestId('oauth-approval-link')).toHaveAttribute('href', '/app/addons?detail=providera&view=settings')
    await shoot(page, 'user-not-approved-admin')
  })

  test('a start the server cannot send to the service says why', async ({ page }) => {
    await openConnectors(page, { start: { status: 409, json: { error: { type: 'base_url_required', message: 'the server cannot tell its own address' } } } })
    await page.getByTestId('oauth-connect').click()
    await expect(page.getByRole('alert')).toContainText('Ask an administrator to set the external base URL of the server.')
    await expect(page.getByTestId('oauth-connect')).not.toHaveAttribute('aria-busy', 'true')
    await shoot(page, 'user-start-base-url-required', { widths: ['desktop'] })
  })

  for (const [query, outcome, text, name] of [
    ['oauth=ok', 'ok', 'Connected. The connection works now.', 'user-returned-ok'],
    ['oauth=denied', 'denied', 'You declined at the service. Nothing changed.', 'user-returned-denied'],
    ['oauth=error&class=scopes_missing', 'error', 'did not grant all the access this add-on needs', 'user-returned-error'],
  ]) {
    test(`back from the service with ${query}: one message, and the query is stripped`, async ({ page }) => {
      await openConnectors(page, { items: [binding(outcome === 'ok' ? CONNECTED : { state: 'not_connected' })], path: `/app/chats/connectors?${query}` })
      const message = page.getByTestId('oauth-returned')
      await expect(message).toHaveAttribute('data-outcome', outcome)
      await expect(message).toContainText(text)
      await expect(page).toHaveURL(/\/app\/chats\/connectors$/)
      await shoot(page, name)
      await message.getByRole('button', { name: 'Dismiss' }).click()
      await expect(message).toHaveCount(0)
    })
  }

  test('back with a class the page does not know: the generic text, never the query', async ({ page }) => {
    await openConnectors(page, { path: '/app/chats/connectors?oauth=error&class=made_up' })
    await expect(page.getByTestId('oauth-returned')).toHaveText(/The login could not be finished\. Connect again\./)
    await expect(page.getByTestId('oauth-returned')).not.toContainText('made_up')
  })
})

// The administrator's form on the add-on's Settings tab.
const CLIENT = (over = {}) => ({
  addon: 'providera', configured: false, client_id: '', has_secret: false, approved: false, current_digest: 'sha256:' + 'a'.repeat(64),
  descriptor: {
    authorize_url: 'https://auth.example.test/authorize', token_url: 'https://auth.example.test/token', revoke_url: 'https://auth.example.test/revoke',
    hosts: ['auth.example.test', '*.example.test'], required_scopes: ['read'], optional_scopes: ['write', 'admin'], pkce: 'S256', client_auth: 'basic',
    authorize_params: [{ name: 'access_type', value: 'offline' }],
  },
  callback_url: 'http://127.0.0.1:8089/api/chats/connectors/oauth/callback',
  ...over,
})

async function openSettings(page, { client = CLIENT(), save, provider = PROVIDER } = {}) {
  await signIn(page, ADMIN)
  await mockAddons(page, { apps: [provider] })
  const state = { client: structuredClone(client), puts: [], gets: 0 }
  await page.route(/\/api\/chats\/admin\/oauth-clients\/providera$/, (r) => {
    if (r.request().method() === 'GET') { state.gets += 1; return r.fulfill({ json: state.client }) }
    const body = r.request().postDataJSON()
    state.puts.push(body)
    if (save) {
      const custom = save(body, state)
      if (custom) return r.fulfill(custom)
    }
    state.client = { ...state.client, configured: true, approved: true, client_id: body.client_id, has_secret: state.client.has_secret || !!body.client_secret,
      approved_digest: body.descriptor_digest, approved_by: 'alice', approved_at: '2026-11-04T09:00:00Z' }
    return r.fulfill({ json: state.client })
  })
  await page.goto('/app/addons?detail=providera&view=settings')
  await expect(page.getByTestId('oauth-client-settings')).toBeVisible()
  return state
}

test.describe('The approval of an OAuth login', () => {
  test('warns above the form when the add-on does not come from the official library, and not when it does', async ({ page }) => {
    await openSettings(page, { provider: { ...PROVIDER, admin: { canStart: false, canStop: true, managementUrl: '/app/addons?manage=providera', sourceId: 'community', official: false } } })
    const form = page.getByTestId('oauth-client-settings')
    const notice = form.getByTestId('oauth-client-untrusted')
    await expect(notice).toContainText('This add-on comes from community, not the official library.')
    await expect(notice).toContainText('Install it only if you trust its author.')
    // The warning comes before the form that approves the login.
    const before = await notice.evaluate((n) => !!(n.compareDocumentPosition(document.querySelector('[data-testid="oauth-client-form"]')) & Node.DOCUMENT_POSITION_FOLLOWING))
    expect(before).toBe(true)
    await shoot(page, 'admin-untrusted-source', { tall: true })
  })

  test('shows no warning for an add-on of the official library', async ({ page }) => {
    await openSettings(page, { provider: { ...PROVIDER, admin: { canStart: false, canStop: true, managementUrl: '/app/addons?manage=providera', sourceId: 'official', official: true } } })
    await expect(page.getByTestId('oauth-client-form')).toBeVisible()
    await expect(page.getByTestId('oauth-client-untrusted')).toHaveCount(0)
  })

  test('not configured: the login in plain words, the callback to copy, and Approve and save sends the digest shown', async ({ page }) => {
    const state = await openSettings(page)
    const form = page.getByTestId('oauth-client-settings')
    await expect(form.getByTestId('oauth-client-status')).toHaveAttribute('data-status', 'unconfigured')
    await expect(form.getByTestId('oauth-client-authorize')).toHaveText('https://auth.example.test/authorize')
    await expect(form.getByTestId('oauth-client-required')).toHaveText('read')
    await expect(form.getByTestId('oauth-client-optional')).toHaveText('write, admin')
    await expect(form).toContainText('Client ID and secret, sent in the request header')
    await expect(form).toContainText('access_type=offline')
    await expect(form.getByTestId('oauth-client-callback')).toHaveValue('http://127.0.0.1:8089/api/chats/connectors/oauth/callback')
    await expect(form.getByTestId('oauth-client-approve')).toBeDisabled()
    await shoot(page, 'admin-not-configured', { tall: true })
    await form.getByLabel('Client ID').fill('client-123')
    await form.getByLabel('Client secret').fill('s3cret-value')
    await form.getByTestId('oauth-client-approve').click()
    await expect(form.getByTestId('oauth-client-saved')).toBeVisible()
    expect(state.puts).toEqual([{ client_id: 'client-123', client_secret: 's3cret-value', descriptor_digest: CLIENT().current_digest }])
    // The secret never comes back: the field is empty and says one is saved.
    await expect(form.getByLabel('Client secret')).toHaveValue('')
    await expect(form.getByTestId('oauth-client-secret-hint')).toHaveAttribute('data-saved', 'true')
    await expect(form.getByTestId('oauth-client-status')).toContainText('Approved by alice')
  })

  test('configured with a saved secret: the secret is never shown and an empty field keeps it', async ({ page }) => {
    const state = await openSettings(page, { client: CLIENT({ configured: true, approved: true, client_id: 'client-123', has_secret: true, approved_by: 'alice', approved_at: '2026-11-03T09:00:00Z', approved_digest: 'sha256:' + 'a'.repeat(64) }) })
    const form = page.getByTestId('oauth-client-settings')
    await expect(form.getByTestId('oauth-client-status')).toHaveAttribute('data-status', 'approved')
    await expect(form.getByLabel('Client ID')).toHaveValue('client-123')
    await expect(form.getByLabel('Client secret')).toHaveValue('')
    await expect(form.getByTestId('oauth-client-secret-hint')).toHaveText('A secret is saved. Leave the field empty to keep it.')
    await shoot(page, 'admin-configured', { tall: true })
    await form.getByTestId('oauth-client-approve').click()
    await expect(form.getByTestId('oauth-client-saved')).toBeVisible()
    expect(state.puts).toEqual([{ client_id: 'client-123', descriptor_digest: CLIENT().current_digest }])
  })

  test('a login that changed while the form was open: nothing is saved, and the new login can be shown', async ({ page }) => {
    const fresh = 'sha256:' + 'b'.repeat(64)
    const state = await openSettings(page, {
      save: (body, s) => {
        if (body.descriptor_digest === fresh) return null
        s.client = { ...s.client, current_digest: fresh, descriptor: { ...s.client.descriptor, optional_scopes: ['write', 'admin', 'delete'] } }
        return { status: 409, json: { error: { type: 'approval_stale', message: 'the login changed', code: 409 }, current_digest: fresh } }
      },
    })
    const form = page.getByTestId('oauth-client-settings')
    await form.getByLabel('Client ID').fill('client-123')
    await form.getByLabel('Client secret').fill('s3cret-value')
    await form.getByTestId('oauth-client-approve').click()
    await expect(form.getByTestId('oauth-client-stale')).toContainText('Nothing was saved.')
    await expect(form.getByTestId('oauth-client-approve')).toBeDisabled()
    await shoot(page, 'admin-stale-digest', { tall: true })
    await form.getByTestId('oauth-client-stale').getByRole('button', { name: 'Show the new login' }).click()
    await expect(form.getByTestId('oauth-client-optional')).toHaveText('write, admin, delete')
    await expect(form.getByLabel('Client ID')).toHaveValue('client-123')
    await form.getByTestId('oauth-client-approve').click()
    await expect(form.getByTestId('oauth-client-saved')).toBeVisible()
    expect(state.puts.map((p) => p.descriptor_digest)).toEqual([CLIENT().current_digest, fresh])
  })

  test('a server that cannot tell its own address shows no callback and says what to set', async ({ page }) => {
    await openSettings(page, { client: CLIENT({ callback_url: undefined }) })
    const form = page.getByTestId('oauth-client-settings')
    await expect(form.getByTestId('oauth-client-base-url')).toContainText('Set the external base URL of the server')
    await expect(form.getByTestId('oauth-client-callback')).toHaveCount(0)
    await shoot(page, 'admin-base-url-required', { tall: true })
  })

  test('a public client has no secret field', async ({ page }) => {
    const state = await openSettings(page, { client: CLIENT({ descriptor: { ...CLIENT().descriptor, client_auth: 'none' } }) })
    const form = page.getByTestId('oauth-client-settings')
    await expect(form.getByLabel('Client secret')).toHaveCount(0)
    await expect(form).toContainText('This login uses no client secret.')
    await form.getByLabel('Client ID').fill('public-1')
    await form.getByTestId('oauth-client-approve').click()
    await expect.poll(() => state.puts).toEqual([{ client_id: 'public-1', descriptor_digest: CLIENT().current_digest }])
  })
})

// The chat Tools menu row of an add-on whose connection must log in again.
test.describe('Reconnect in the chat Tools menu', () => {
  test('a connection whose login ended says so and links to the Connectors page', async ({ page }) => {
    await mockChat(page)
    const detail = { id: 'c1', title: 'Items', model: 'qwen3-8b', kind: 'chat', created: '2026-11-04T10:00:00Z', updated: '2026-11-04T10:00:00Z', running: false, last_turn: 'done', pending: [] }
    await page.route('**/api/chats/c1/events', (route) => route.abort())
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, (route) => route.fulfill({ json: { messages: [] } }))
    await page.route('**/api/chats/c1', (route) => route.fulfill({ json: detail }))
    await page.route(/\/api\/chats(\?.*)?$/, (route) => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, files_reason: '' } }))
    await page.route('**/api/chats/c1/addons', (route) => route.fulfill({
      json: {
        commands: 'none',
        addons: [{
          id: 'providera', tools: 2, off: false, title: 'Provider A', description: 'Read items and propose changes.', credentialed: true,
          connections: [{ id: 'b1', name: 'Work account', handle: 'work', connection_state: 'reconnect_needed' }],
          enabled_connections: ['b1'], connection_by: 'only', connection_state: 'reconnect_needed',
        }],
      },
    }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const item = page.getByTestId('chat-tools-item')
    await expect(item).toContainText('Connect again: Work account')
    const link = page.getByTestId('chat-tools-pick-link')
    await expect(link).toHaveText('Reconnect')
    await expect(link).toHaveAttribute('href', '/app/chats/connectors')
    await shoot(page, 'tools-menu-reconnect')
    await link.click()
    await expect(page).toHaveURL(/\/app\/chats\/connectors$/)
  })
})
