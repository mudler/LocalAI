import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, openEdit, openPane, saveSheet } from './connector-fixtures.js'

// The page of the Gmail add-on (the real built bundle, inside the add-on
// shell) with the core API stubbed. A person signs in on the Connectors page,
// so this page has no New button: it edits the labels and the write mode of
// the connections that exist.

const PAGE = '/app/addons/gmail/apps'
const BINDING = { id: 'b1', name: 'Mail', kind: 'gmail', auth_kind: 'oauth2', agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: ['Work', 'INBOX'], write_mode: 'propose' }
const KIND = { capabilities: [], kindExtra: { title: 'Gmail', agent_optional: true, auth_kind: 'oauth2', files: undefined } }
const api = (page, initial = []) => connectorApi(page, initial, 'gmail', [], undefined, KIND)

test.describe('Gmail add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page) => installStudioAddons(page, { ids: ['gmail'] })

  test('the page lists the connections with their labels and write mode, and has no button to add one', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('gmail-limits')).toContainText('Connectors page')
    await expect(page.getByTestId('connector-row')).toContainText('Mail')
    await openPane(page)
    await expect(page.getByTestId('gmail-row-labels')).toContainText('2 labels: Work, INBOX')
    await expect(page.getByTestId('gmail-row-write')).toHaveText('Asks you first')
    await expect(page.getByTestId('gmail-page').getByRole('button', { name: /New/ })).toHaveCount(0)
  })

  test('a connection with ALL says in the row that it reaches the whole mailbox', async ({ page }) => {
    await install(page)
    await api(page, [{ ...BINDING, resources: ['ALL'] }]).install()
    await page.goto(PAGE)
    await openPane(page)
    await expect(page.getByTestId('gmail-row-labels')).toHaveText('The whole mailbox')
  })

  test('without a connection it says where to add one', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByText('Add one on the Connectors page.')).toBeVisible()
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
})
