import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi } from './connector-fixtures.js'

// The page of the Google Drive add-on (the real built bundle, inside the
// add-on shell) with the core API stubbed. A person signs in on the Connectors
// page, so this page has no New button: it edits the folders and the write
// mode of the connections that exist.

const PAGE = '/app/addons/gdrive/apps'
const BINDING = { id: 'b1', name: 'Work', kind: 'gdrive', auth_kind: 'oauth2', agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: ['1AbC_d', 'root'], write_mode: 'propose' }
const KIND = { capabilities: [], kindExtra: { title: 'Google Drive', agent_optional: true, auth_kind: 'oauth2', files: undefined } }
const api = (page, initial = []) => connectorApi(page, initial, 'gdrive', [], undefined, KIND)

test.describe('Google Drive add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page) => installStudioAddons(page, { ids: ['gdrive'] })

  test('the page lists the connections with their folders and write mode, and has no button to add one', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('gdrive-limits')).toContainText('Connectors page')
    await expect(page.getByTestId('connector-row')).toContainText('Work')
    await expect(page.getByTestId('gdrive-row-folders')).toContainText('2 folders: 1AbC_d, root')
    await expect(page.getByTestId('gdrive-row-write')).toHaveText('Asks you first')
    await expect(page.getByTestId('gdrive-page').getByRole('button', { name: /New/ })).toHaveCount(0)
  })

  test('without a connection it says where to add one', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByText('Add one on the Connectors page.')).toBeVisible()
  })

  test('the folder list takes an address, names the bad line and keeps the case of an ID', async ({ page }) => {
    await install(page)
    const a = api(page, [BINDING])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    const folders = page.getByLabel('Folders the agent may use')
    await folders.fill('1AbC_d\nnot a folder')
    await expect(folders).toHaveAccessibleDescription(/"not a folder" is not a folder ID/)
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await folders.fill('https://drive.google.com/drive/folders/1XyZ_9?usp=sharing\n1AbC_d')
    await expect(page.getByRole('button', { name: 'Save' })).toBeEnabled()
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('gdrive-row-folders')).toContainText('1XyZ_9, 1AbC_d')
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.resources).toEqual(['1XyZ_9', '1AbC_d'])
    expect(put.body.write_mode).toBe('propose')
    expect(put.body.token).toBeUndefined()
  })

  test('the write mode offers read only and asks you first, and says every change waits', async ({ page }) => {
    await install(page)
    const a = api(page, [{ ...BINDING, write_mode: 'off' }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    const group = page.getByRole('radiogroup', { name: 'What the agent may change in Drive' })
    await expect(group.getByRole('radio', { name: /Read only/ })).toBeChecked()
    await expect(group.getByRole('radio', { name: /Acts without asking/ })).toHaveCount(0)
    await group.getByRole('radio', { name: /Asks you first/ }).check()
    await expect(page.getByTestId('gdrive-write-note')).toContainText('optional write access')
    await page.getByRole('button', { name: 'Save' }).click()
    expect(a.s.calls.find(c => c.method === 'PUT').body.write_mode).toBe('propose')
  })
})
