import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons, packagedApp } from './studio-addon.js'
import { ADMIN, MEMBER, sessionFor } from './addons-fixtures.js'
import { connectorApi, openEdit, openPane, saveSheet } from './connector-fixtures.js'

// The page of the Google Drive add-on (the real built bundle, inside the
// add-on shell) with the core API stubbed. The page makes its own
// connections with Connect with Google (the host's login) and edits the folders
// and the write mode of the ones that exist.

const PAGE = '/app/addons/gdrive/apps'
const BINDING = { id: 'b1', name: 'Work', kind: 'gdrive', auth_kind: 'oauth2', agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: ['1AbC_d', 'root'], write_mode: 'propose' }
const KIND = { capabilities: [], kindExtra: { title: 'Google Drive', agent_optional: true, auth_kind: 'oauth2', files: undefined } }
const api = (page, initial = []) => connectorApi(page, initial, 'gdrive', [], undefined, KIND)

test.describe('Google Drive add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page) => installStudioAddons(page, { ids: ['gdrive'] })

  test('the page lists the connections with their folders and write mode, and has no token sheet, only Connect with Google', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('gdrive-limits')).toContainText('Connect with Google')
    await expect(page.getByTestId('connector-row')).toContainText('Work')
    await openPane(page)
    await expect(page.getByTestId('gdrive-row-folders')).toContainText('2 folders: 1AbC_d, root')
    await expect(page.getByTestId('gdrive-row-write')).toHaveText('Asks you first')
    await expect(page.getByTestId('gdrive-page').getByRole('button', { name: /New/ })).toHaveCount(0)
    await expect(page.getByTestId('oauth-add-open')).toHaveText('Connect with Google')
  })

  test('without a connection it says how to add one', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByText('Select Connect with Google to add one.')).toBeVisible()
  })

  test('the folder list takes an address, names the bad line and keeps the case of an ID', async ({ page }) => {
    await install(page)
    const a = api(page, [BINDING])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    const folders = page.getByLabel('Folders the agent may use')
    await folders.fill('1AbC_d\nnot a folder')
    await expect(folders).toHaveAccessibleDescription(/"not a folder" is not a folder ID/)
    await expect(page.getByTestId('connector-save')).toBeDisabled()
    await folders.fill('https://drive.google.com/drive/folders/1XyZ_9?usp=sharing\n1AbC_d')
    await expect(page.getByTestId('connector-save')).toBeEnabled()
    await saveSheet(page)
    if (!(await page.getByTestId('connector-pane').isVisible())) await openPane(page)
    await expect(page.getByTestId('gdrive-row-folders')).toContainText('1XyZ_9, 1AbC_d')
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.resources).toEqual(['1XyZ_9', '1AbC_d'])
    expect(put.body.write_mode).toBe('propose')
    expect(put.body.token).toBeUndefined()
  })

  test('a connection with no agent and no trigger saves: the request holds nothing that needs an agent', async ({ page }) => {
    await install(page)
    const a = api(page, [{ ...BINDING, resources: ['1AbC_d'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expect(page.getByTestId('connector-save')).toBeEnabled()
    await saveSheet(page)
    await expect(page.getByTestId('connector-error')).toHaveCount(0)
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.agent_id).toBe('')
    expect(put.body.enabled).toBe(false)
    expect(put.body.accept_any).toBe(false)
    expect(put.body.allow).toEqual([])
    expect(put.body.caps).toEqual([])
    expect(put.body.approval).toMatchObject({ mode: 'ask', acts: [], max_pending: 0, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('All folders is a choice of its own: it needs a tick, saves the list *, hides the folder box and shows on the row', async ({ page }) => {
    await install(page)
    const a = api(page, [{ ...BINDING, resources: ['1AbC_d'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    const all = page.getByTestId('gdrive-all-folders')
    await expect(all).not.toBeChecked()
    await expect(page.getByTestId('gdrive-folders')).toBeVisible()
    await all.check()
    await expect(page.getByTestId('gdrive-folders')).toHaveCount(0)
    await expect(page.getByTestId('gdrive-all-warning')).toContainText('every file that your Google login can reach')
    await expect(page.getByTestId('connector-save')).toBeDisabled()
    await page.getByTestId('gdrive-all-ack').check()
    await expect(page.getByTestId('connector-save')).toBeEnabled()
    await saveSheet(page)
    if (!(await page.getByTestId('connector-pane').isVisible())) await openPane(page)
    await expect(page.getByTestId('gdrive-row-folders')).toHaveText('All folders (My Drive and shared drives)')
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.resources).toEqual(['*'])
    expect(put.body.confirm_any_resource).toBe('allow any resource')
    expect(put.body.write_mode).toBe('propose')
  })

  test('a saved All folders choice reads back ticked, needs no new tick, and turning it off gives the list back', async ({ page }) => {
    await install(page)
    const a = api(page, [{ ...BINDING, resources: ['*'] }])
    await a.install()
    await page.goto(PAGE)
    await openPane(page)
    await expect(page.getByTestId('gdrive-row-folders')).toHaveText('All folders (My Drive and shared drives)')
    await openEdit(page)
    await expect(page.getByTestId('gdrive-all-folders')).toBeChecked()
    await expect(page.getByTestId('gdrive-all-ack')).toHaveCount(0)
    await expect(page.getByTestId('connector-save')).toBeEnabled()
    await page.getByTestId('gdrive-all-folders').uncheck()
    await page.getByTestId('gdrive-folders').fill('1XyZ_9')
    await saveSheet(page)
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.resources).toEqual(['1XyZ_9'])
    expect(put.body.confirm_any_resource).toBeUndefined()
  })

  test('the write mode offers read only and asks you first, and says every change waits', async ({ page }) => {
    await install(page)
    const a = api(page, [{ ...BINDING, write_mode: 'off' }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    const group = page.getByRole('radiogroup', { name: 'What the agent may change in Drive' })
    await expect(group.getByRole('radio', { name: /Read only/ })).toBeChecked()
    await expect(group.getByRole('radio', { name: /Acts without asking/ })).toHaveCount(0)
    await group.getByRole('radio', { name: /Asks you first/ }).check()
    await expect(page.getByTestId('gdrive-write-note')).toContainText('optional write access')
    await saveSheet(page)
    expect(a.s.calls.find(c => c.method === 'PUT').body.write_mode).toBe('propose')
  })

  // The login of the page: the host's component adds the connection, starts the
  // login with this page as the place to come back to, and says when no client
  // is approved. The service is a stand-in (auth.example.test).
  const LOGIN = { requiredScopes: ['read'], optionalScopes: ['write'] }
  const loginApp = () => packagedApp('gdrive', { credentialed: true, oauth: LOGIN })
  const signIn = (page, user) => page.route('**/api/auth/status', (route) => route.fulfill({ json: sessionFor(user, { addons: true, connectors: true, chats: true }) }))
  async function loginApi(page, { initial = [], start } = {}) {
    await installStudioAddons(page, { ids: ['gdrive'], apps: [loginApp()] })
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
    await expect(page.getByTestId('oauth-add-name')).toHaveValue('Google Drive')
    await page.getByTestId('oauth-add-name').fill('Work')
    await page.getByTestId('oauth-add-submit').click()
    await page.waitForURL('https://auth.example.test/**')
    const create = a.s.calls.find((c) => c.method === 'POST')
    expect(create.body).toMatchObject({ kind: 'gdrive', name: 'Work', auth_kind: 'oauth2' })
    expect(a.s.starts).toEqual([{ return_to: '/app/addons/gdrive/apps' }])
  })

  test('a person comes back to the page with the result of the login, and the query is cleaned', async ({ page }) => {
    await signIn(page, MEMBER)
    await loginApi(page, { initial: [{ ...BINDING, oauth: { state: 'connected', account_label: 'ana@example.test', scopes: ['read'], has_client: true } }] })
    await page.goto(PAGE + '?oauth=ok')
    await expect(page.getByTestId('oauth-returned')).toHaveAttribute('data-outcome', 'ok')
    await expect(page.getByTestId('oauth-account')).toContainText('ana@example.test')
    await expect(page).toHaveURL(/\/app\/addons\/gdrive\/apps$/)
  })

  test('with no approved client an administrator gets a link to Settings', async ({ page }) => {
    const refuse = { status: 409, json: { error: { type: 'needs_approval', message: 'needs_approval' } } }
    await signIn(page, ADMIN)
    await loginApi(page, { start: refuse })
    await page.goto(PAGE)
    await page.getByTestId('oauth-add-open').click()
    await page.getByTestId('oauth-add-submit').click()
    await expect(page.getByTestId('connection-login')).toHaveAttribute('data-approval', 'missing')
    await expect(page.getByTestId('connection-login-settings')).toHaveAttribute('href', /\/app\/addons\?detail=gdrive&view=settings/)
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
