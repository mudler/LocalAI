import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons, packagedApp } from './studio-addon.js'
import { ADMIN, MEMBER, sessionFor } from './addons-fixtures.js'
import { connectorApi, openEdit, openPane, saveSheet } from './connector-fixtures.js'

// The page of the Gmail add-on (the real built bundle, inside the add-on
// shell) with the core API stubbed. The page makes its own
// connections with Connect with Google (the host's login) and edits the labels
// and the write mode of the ones that exist.

const PAGE = '/app/addons/gmail/apps'
const BINDING = { id: 'b1', name: 'Mail', kind: 'gmail', auth_kind: 'oauth2', agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: ['Work', 'INBOX'], write_mode: 'propose' }
const KIND = { capabilities: [], kindExtra: { title: 'Gmail', agent_optional: true, auth_kind: 'oauth2', files: undefined } }
const api = (page, initial = []) => connectorApi(page, initial, 'gmail', [], undefined, KIND)

test.describe('Gmail add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page) => installStudioAddons(page, { ids: ['gmail'] })

  test('the page lists the connections with their labels and write mode, and has no token sheet, only Connect with Google', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('gmail-limits')).toContainText('Connect with Google')
    await expect(page.getByTestId('connector-row')).toContainText('Mail')
    await openPane(page)
    await expect(page.getByTestId('gmail-row-labels')).toContainText('2 labels: Work, INBOX')
    await expect(page.getByTestId('gmail-row-write')).toHaveText('Asks you first')
    await expect(page.getByTestId('gmail-page').getByRole('button', { name: /New/ })).toHaveCount(0)
    await expect(page.getByTestId('oauth-add-open')).toHaveText('Connect with Google')
  })

  test('a connection with ALL says in the row that it reaches the whole mailbox', async ({ page }) => {
    await install(page)
    await api(page, [{ ...BINDING, resources: ['ALL'] }]).install()
    await page.goto(PAGE)
    await openPane(page)
    await expect(page.getByTestId('gmail-row-labels')).toHaveText('The whole mailbox')
  })

  test('without a connection it says how to add one', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByText('Select Connect with Google to add one.')).toBeVisible()
  })

  test('the label list takes names with spaces as dashes, names the bad line and warns about ALL', async ({ page }) => {
    await install(page)
    const a = api(page, [BINDING])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    const labels = page.getByLabel('Labels the agent may use')
    await labels.fill('Work\nbad<label')
    await expect(labels).toHaveAccessibleDescription(/"bad<label" is not a label/)
    await expect(page.getByTestId('connector-save')).toBeDisabled()
    await labels.fill('Private notes\nWork')
    await expect(page.getByTestId('connector-save')).toBeEnabled()
    await expect(page.getByTestId('gmail-all-note')).toHaveCount(0)
    await labels.fill('Private notes\nall')
    await expect(page.getByTestId('gmail-all-note')).toContainText('every mail this account can read')
    await saveSheet(page)
    if (!(await page.getByTestId('connector-pane').isVisible())) await openPane(page)
    await expect(page.getByTestId('gmail-row-labels')).toHaveText('The whole mailbox')
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.resources).toEqual(['Private-notes', 'all'])
    expect(put.body.write_mode).toBe('propose')
    expect(put.body.token).toBeUndefined()
  })

  test('the write mode offers read only and asks you first, and says every draft and send waits', async ({ page }) => {
    await install(page)
    const a = api(page, [{ ...BINDING, write_mode: 'off' }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    const group = page.getByRole('radiogroup', { name: 'What the agent may do with your mail' })
    await expect(group.getByRole('radio', { name: /Read only/ })).toBeChecked()
    await expect(group.getByRole('radio', { name: /Acts without asking/ })).toHaveCount(0)
    await group.getByRole('radio', { name: /Asks you first/ }).check()
    await expect(page.getByTestId('gmail-write-note')).toContainText('optional write access')
    await saveSheet(page)
    expect(a.s.calls.find(c => c.method === 'PUT').body.write_mode).toBe('propose')
  })

  // The login of the page: the host's component adds the connection, starts the
  // login with this page as the place to come back to, and says when no client
  // is approved. The service is a stand-in (auth.example.test).
  const LOGIN = { requiredScopes: ['read'], optionalScopes: ['write'] }
  const loginApp = () => packagedApp('gmail', { credentialed: true, oauth: LOGIN })
  const signIn = (page, user) => page.route('**/api/auth/status', (route) => route.fulfill({ json: sessionFor(user, { addons: true, connectors: true, chats: true }) }))
  async function loginApi(page, { initial = [], start } = {}) {
    await installStudioAddons(page, { ids: ['gmail'], apps: [loginApp()] })
    const a = api(page, initial)
    await a.install()
    a.s.starts = []
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/oauth\/start$/, (r) => {
      a.s.starts.push(r.request().postDataJSON())
      if (start) return r.fulfill(start)
      return r.fulfill({ json: { authorize_url: 'https://auth.example.test/authorize?state=y', expires: '2026-11-04T10:10:00Z' } })
    })
    await page.route('https://auth.example.test/**', (r) => r.fulfill({ contentType: 'text/html', body: '<p>service</p>' }))
    return a
  }

  test('Connect with Google adds the connection, then sends the browser to the service and back to this page', async ({ page }) => {
    await signIn(page, MEMBER)
    const a = await loginApi(page)
    await page.goto(PAGE)
    await page.getByTestId('oauth-add-open').click()
    await expect(page.getByTestId('oauth-add-name')).toHaveValue('Gmail')
    await page.getByTestId('oauth-add-name').fill('Work')
    await page.getByTestId('oauth-add-submit').click()
    await page.waitForURL('https://auth.example.test/**')
    const create = a.s.calls.find((c) => c.method === 'POST')
    expect(create.body).toMatchObject({ kind: 'gmail', name: 'Work', auth_kind: 'oauth2' })
    expect(a.s.starts).toEqual([{ return_to: '/app/addons/gmail/apps' }])
  })

  test('a person comes back to the page with the result of the login, and the query is cleaned', async ({ page }) => {
    await signIn(page, MEMBER)
    await loginApi(page, { initial: [{ ...BINDING, oauth: { state: 'connected', account_label: 'ana@example.test', scopes: ['read'], has_client: true } }] })
    await page.goto(PAGE + '?oauth=ok')
    await expect(page.getByTestId('oauth-returned')).toHaveAttribute('data-outcome', 'ok')
    await expect(page.getByTestId('oauth-account')).toContainText('ana@example.test')
    await expect(page).toHaveURL(/\/app\/addons\/gmail\/apps$/)
  })

  test('with no approved client an administrator gets a link to Settings', async ({ page }) => {
    const refuse = { status: 409, json: { error: { type: 'needs_approval', message: 'needs_approval' } } }
    await signIn(page, ADMIN)
    await loginApi(page, { start: refuse })
    await page.goto(PAGE)
    await page.getByTestId('oauth-add-open').click()
    await page.getByTestId('oauth-add-submit').click()
    await expect(page.getByTestId('connection-login')).toHaveAttribute('data-approval', 'missing')
    await expect(page.getByTestId('connection-login-settings')).toHaveAttribute('href', /\/app\/addons\?detail=gmail&view=settings/)
    await expect(page.getByTestId('oauth-add-open')).toHaveCount(0)
  })

  test('a person who is not an administrator is told to ask one', async ({ page }) => {
    await signIn(page, MEMBER)
    await loginApi(page, { initial: [{ ...BINDING, oauth: { state: 'needs_approval', scopes: [], has_client: false } }] })
    await page.goto(PAGE)
    await expect(page.getByTestId('oauth-approval')).toContainText('An administrator must approve this add-on first.')
    await expect(page.getByTestId('oauth-approval-link')).toHaveCount(0)
    await expect(page.getByTestId('oauth-add-open')).toHaveCount(0)
  })
})
