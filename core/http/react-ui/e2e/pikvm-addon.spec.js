import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, openPane } from './connector-fixtures.js'

// The page of the PiKVM add-on (the real built bundle, inside the add-on
// shell): the choice of what is still asked before a power action or a key
// press, and the connection row that says which tools run without asking. The
// core API and the add-on's tools are stubbed; the choice is a user setting of
// the signed-in person, so the stub keeps it the way the host does.

const PAGE = '/app/addons/pikvm/apps'
const SHOTS = process.env.UI_SHOTS_DIR
const BINDING = { id: 'b1', name: 'PiKVM: pikvm.local', kind: 'pikvm', agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: ['https://pikvm.local'], write_mode: 'propose', has_token: true }
const KIND = { capabilities: [], kindExtra: { title: 'PiKVM', files: undefined, agent_optional: true, tool_only: true } }
const api = (page, initial = []) => connectorApi(page, initial, 'pikvm', [], undefined, KIND)

const DECLARED = ['power-action', 'send-key'].map(tool => ({ key: `approval-${tool}`, values: ['ask', 'skip'], default: 'ask' }))

async function shot(page, name) {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true })
}

// The user settings of the add-on and the access view that follows them. The
// host reports a tool as running without asking only while the write mode of
// the connection is not off.
async function host(page, { declared = DECLARED, values = {}, failSave = false, writeMode = 'propose' } = {}) {
  const state = { values: { ...values }, puts: [], accessReads: 0 }
  const view = () => declared.map(d => ({ ...d, value: state.values[d.key] || d.default }))
  await page.route('**/api/addons/pikvm/user-settings', route => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON()
      state.puts.push(body)
      if (failSave) return route.fulfill({ status: 503, json: { error: { message: 'Settings unavailable' } } })
      state.values[body.key] = body.value
    }
    return route.fulfill({ json: { settings: view() } })
  })
  await page.route(/\/api\/chats\/connectors\/[^/?]+\/access(\?.*)?$/, route => {
    state.accessReads += 1
    const approvals = ['power-action', 'send-key'].map(tool => {
      const skip = state.values[`approval-${tool}`] === 'skip'
      return { tool, skip, ...(skip && writeMode !== 'off' ? { runs_without_asking: ['owner', 'paired'] } : {}) }
    })
    return route.fulfill({ json: { binding: 'b1', subjects: [], recent: [], approvals } })
  })
  return state
}

// Gets and sets of the add-on's settings (switches by device) go through the gateway.
const tools = ({ admin = true } = {}) => ({
  dispatch: (body) => {
    const a = body.arguments || {}
    const ok = (data) => ({ status: 200, contentType: 'application/json', body: JSON.stringify({ payload: { data } }) })
    if (a.tool === 'get-settings') return ok({ devices: [], defaults: { allowTyping: false, allowHardPower: false, maxTypeChars: 200 }, admin, broken: false, keys: ['a', 'enter'] })
    return null
  },
})

test.describe('PiKVM add-on approvals', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page) => installStudioAddons(page, { ids: ['pikvm'], dispatch: tools().dispatch })

  test('every action asks by default, and typed text has no choice at all', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await host(page)
    await page.goto(PAGE)
    const section = page.getByTestId('pikvm-approval')
    await expect(section).toBeVisible()
    for (const tool of ['power-action', 'send-key']) {
      const group = page.getByTestId(`pikvm-approval-${tool}`)
      await expect(group.getByRole('radio', { name: /Ask each time/ })).toBeChecked()
      await expect(group.getByRole('radio', { name: /Don't ask/ })).not.toBeChecked()
    }
    await expect(section.getByRole('radiogroup')).toHaveCount(2)
    await expect(page.getByTestId('pikvm-approval-text')).toContainText('Typed text always asks')
    await expect(page.getByTestId('pikvm-approval-text')).toContainText('no Don\'t ask')
    await expect(page.getByTestId('pikvm-approval-who')).toContainText('Everyone else is still asked')
    await expect(page.getByTestId('pikvm-approval-audit')).toContainText('written to the log')
  })

  test('the power action warning is on the page before the choice is made, in plain words', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await host(page)
    await page.goto(PAGE)
    const warn = page.getByTestId('pikvm-approval-warn-power-action')
    await expect(warn).toBeVisible()
    await expect(warn).toContainText('switch this machine off or reset it at once')
    await expect(warn).toContainText('unsaved work on it is lost')
    await expect(warn).toContainText('hard power actions')
    await expect(page.getByTestId('pikvm-approval-warn-send-key')).toContainText('no card to confirm')
  })

  test('choosing Don\'t ask saves that one tool as the person\'s own setting and the row says it runs without asking', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    const h = await host(page)
    await page.goto(PAGE)
    await openPane(page)
    const row = page.getByTestId('connector-pane')
    await expect(row.getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'asks')
    await shot(page, 'pikvm-1-default')
    await page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Don't ask/ }).click()
    await expect(page.getByTestId('pikvm-approval-status')).toHaveText('Saved. The next call follows it.')
    expect(h.puts).toEqual([{ key: 'approval-power-action', value: 'skip' }])
    await expect(page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Don't ask/ })).toBeChecked()
    await expect(page.getByTestId('pikvm-approval-send-key').getByRole('radio', { name: /Ask each time/ })).toBeChecked()
    // The effective view of the connection follows without a reload.
    await expect(row.getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'runs')
    await expect(row.getByTestId('connector-approval-power-action')).toContainText('For the owner\'s own chats and for verified people')
    await expect(row.getByTestId('connector-approval-send-key')).toHaveAttribute('data-state', 'asks')
    await shot(page, 'pikvm-2-power-skip')
    // Back to asking.
    await page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Ask each time/ }).click()
    await expect(row.getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'asks')
    expect(h.puts.at(-1)).toEqual({ key: 'approval-power-action', value: 'ask' })
  })

  test('a saved choice is read back when the page opens', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await host(page, { values: { 'approval-send-key': 'skip' } })
    await page.goto(PAGE)
    await expect(page.getByTestId('pikvm-approval-send-key').getByRole('radio', { name: /Don't ask/ })).toBeChecked()
    await expect(page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Ask each time/ })).toBeChecked()
    await openPane(page)
    await expect(page.getByTestId('connector-pane').getByTestId('connector-approval-send-key')).toHaveAttribute('data-state', 'runs')
  })

  test('a connection that is read only says that the choice does nothing', async ({ page }) => {
    await install(page)
    await api(page, [{ ...BINDING, write_mode: 'off' }]).install()
    await host(page, { values: { 'approval-power-action': 'skip' }, writeMode: 'off' })
    await page.goto(PAGE)
    await openPane(page)
    const row = page.getByTestId('connector-pane').getByTestId('connector-approval-power-action')
    await expect(row).toHaveAttribute('data-state', 'idle')
    await expect(row).toContainText('Read only')
  })

  test('a save that fails keeps the earlier choice on screen and says so', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    const h = await host(page, { failSave: true })
    await page.goto(PAGE)
    await page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Don't ask/ }).click()
    await expect(page.getByTestId('pikvm-approval-failed')).toContainText('The earlier choice is still in force')
    expect(h.puts).toHaveLength(1)
    await expect(page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Ask each time/ })).toBeChecked()
    await openPane(page)
    await expect(page.getByTestId('connector-pane').getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'asks')
  })

  test('a package that declares no approval setting shows nothing about it', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await host(page, { declared: [] })
    await page.goto(PAGE)
    await expect(page.getByTestId('pikvm-settings')).toBeVisible()
    await expect(page.getByTestId('pikvm-approval')).toHaveCount(0)
  })

  test('the page follows the interface language: Italian words, no raw keys', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-language', 'it'))
    await install(page)
    await api(page, [BINDING]).install()
    await host(page)
    await page.goto(PAGE)
    const section = page.getByTestId('pikvm-approval')
    await expect(section.getByRole('heading', { name: 'Chiedere prima di agire' })).toBeVisible()
    await expect(page.getByTestId('pikvm-approval-power-action').getByRole('radio', { name: /Non chiedere/ })).toBeVisible()
    await expect(page.getByTestId('pikvm-approval-warn-power-action')).toContainText('spegnere questa macchina')
    await expect(section).not.toContainText('approval.')
    await shot(page, 'pikvm-3-italian')
  })
})
