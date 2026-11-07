import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, openEdit, openMatrix, openPane, saveSheet } from './connector-fixtures.js'

// The page of a connector add-on (drawn here by Telegram): rows with a state
// chip, a pane beside the list, and a side sheet for add and edit with three
// permission presets. The core API is stubbed.

const PAGE = '/app/addons/telegram/bots'
const USUAL = ['files.upload', 'memory.write', 'proposals.confirm', 'talkback.channels', 'triggers.propose', 'triggers.run']
const RUNNING = { id: 'b1', name: 'Support bot', kind: 'telegram', addon: 'telegram', active: true, agent_id: 'a1', enabled: true, accept_any: false, allow: ['42', '77'], has_token: true, caps: ['memory.write'], grants: [], channels: [], paired: [] }
const OFF = { ...RUNNING, id: 'b2', name: 'Night desk', enabled: false, allow: [], caps: [] }
const NO_TOKEN = { ...RUNNING, id: 'b3', name: 'Field team', accept_any: true, allow: [], has_token: false }

test.describe('Connector rows, pane and sheet', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['telegram'] })
  })

  test('a row says Running, Off or Needs attention, with the facts the API returns and no activity figures', async ({ page }) => {
    await connectorApi(page, [RUNNING, OFF, NO_TOKEN]).install()
    await page.goto(PAGE)
    const rows = page.getByTestId('connector-row')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0).getByTestId('connector-state')).toHaveText('Running')
    await expect(rows.nth(1).getByTestId('connector-state')).toHaveText('Off')
    await expect(rows.nth(2).getByTestId('connector-state')).toHaveText('Needs attention')
    await expect(rows.nth(0).getByTestId('connector-facts')).toHaveText('telegram · Agent: Support · Accepts: 2 listed senders')
    await expect(rows.nth(2).getByTestId('connector-facts')).toContainText('Accepts: anyone')
    // The fix of a missing token is the main button of the row, in place of Test.
    await expect(rows.nth(2).getByTestId('connector-attention')).toHaveText('No token is saved.')
    await expect(rows.nth(2).getByRole('button', { name: 'Fix token' })).toBeVisible()
    await expect(rows.nth(2).getByTestId('connector-test')).toHaveCount(0)
    await expect(rows.nth(0).getByTestId('connector-test')).toBeVisible()
    await expect(rows.nth(0)).not.toContainText(/today|last reply|messages/i)
  })

  test('the switch of a row applies at once with no Save', async ({ page }) => {
    const a = connectorApi(page, [RUNNING])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('switch', { name: 'Support bot on' }).click()
    await expect.poll(() => a.s.calls.some(c => c.url.endsWith('/disable'))).toBe(true)
    await expect(page.getByTestId('connector-state')).toHaveText('Off')
    await expect(page.getByTestId('connector-sheet')).toHaveCount(0)
  })

  test('a click on a row opens the pane, which keeps the last test in the browser, and Escape closes it', async ({ page }) => {
    const a = connectorApi(page, [RUNNING])
    await a.install()
    await page.goto(PAGE)
    await openPane(page)
    const pane = page.getByTestId('connector-pane')
    await expect(pane).toBeVisible()
    await expect(pane.getByTestId('connector-pane-never')).toHaveText('Not tested in this browser yet.')
    await expect(pane.getByTestId('connector-pane-facts')).toContainText('2 listed senders')
    // The sender the bot ignored is a refusal.
    await expect(pane.getByTestId('connector-pane-refusals')).toContainText('Newcomer')
    await pane.getByTestId('connector-pane-test').click()
    await expect(pane.getByTestId('connector-pane-ok')).toContainText('Passed')
    // The result stays after a reload.
    await page.reload()
    await openPane(page)
    await expect(page.getByTestId('connector-pane-ok')).toContainText('Passed')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('connector-pane')).toHaveCount(0)
    await expect(page.getByTestId('connector-open')).toBeFocused()
  })

  test('a failed test shows in the pane, and a rejected token turns the row into Fix token', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.route(/\/api\/chats\/connectors\/b1\/test$/, r => r.fulfill({ status: 400, json: { error: { message: 'Unauthorized: the token was refused' } } }))
    await page.goto(PAGE)
    await page.getByTestId('connector-test').click()
    await expect(page.getByTestId('connector-state')).toHaveText('Needs attention')
    await expect(page.getByTestId('connector-fix-token')).toBeVisible()
    await openPane(page)
    await expect(page.getByTestId('connector-pane-failed')).toContainText('token was refused')
  })

  test('Fix token opens the edit sheet on the token field', async ({ page }) => {
    await connectorApi(page, [NO_TOKEN]).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-fix-token').click()
    await expect(page.getByTestId('connector-sheet')).toBeVisible()
    await expect(page.getByLabel('Bot token')).toBeFocused()
  })

  test('the pane footer has Remove, Edit and Test connection, and Remove asks first', async ({ page }) => {
    const a = connectorApi(page, [RUNNING])
    await a.install()
    await page.goto(PAGE)
    await openPane(page)
    const pane = page.getByTestId('connector-pane')
    await expect(pane.getByTestId('connector-pane-delete')).toHaveText('Remove')
    await expect(pane.getByTestId('connector-pane-edit')).toHaveText('Edit')
    await expect(pane.getByTestId('connector-pane-test')).toHaveText('Test connection')
    await pane.getByTestId('connector-pane-delete').click()
    await expect(page.getByRole('alertdialog')).toContainText('Delete Support bot?')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    expect(a.s.calls.some(c => c.method === 'DELETE')).toBe(false)
    await pane.getByTestId('connector-pane-edit').click()
    await expect(page.getByTestId('connector-sheet')).toBeVisible()
  })

  test('the sheet has three sections, a primary Save and test, and a new connection starts on Standard', async ({ page }) => {
    const a = connectorApi(page)
    await a.install()
    await page.goto(PAGE)
    await page.getByTestId('connector-new').click()
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet).toBeVisible()
    await expect(sheet.locator('.cn-sheet__label')).toHaveText(['Connection', 'Who can talk to it', 'What it may do'])
    await expect(sheet.getByTestId('connector-save')).toHaveText('Save and test')
    await expect(sheet.getByTestId('connector-cancel')).toHaveText('Cancel')
    await expect(sheet.locator('.dk-btn--primary')).toHaveCount(1)
    // It opens on its first field.
    await expect(sheet.getByLabel('Name', { exact: true })).toBeFocused()
    await expect(sheet.getByTestId('connector-preset-standard')).toBeChecked()
    await expect(sheet.getByTestId('connector-matrix')).toHaveCount(0)
    await sheet.getByLabel('Name', { exact: true }).fill('Support bot')
    await sheet.getByLabel('Bot token').fill('555:secret-token')
    await sheet.getByLabel('Agent', { exact: true }).selectOption('a1')
    await saveSheet(page)
    // Save, then test with what was saved.
    await expect(page.getByText('Connected to @support_bot.')).toBeVisible()
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors').body
    expect(body.caps.slice().sort()).toEqual(USUAL.slice().sort())
    await expect(page.getByTestId('connector-sheet')).toHaveCount(0)
  })

  test('Answer only saves no permission, and Standard never ticks a sensitive one', async ({ page }) => {
    const a = connectorApi(page, [{ ...RUNNING, caps: [...USUAL, 'tools.write'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expect(page.getByTestId('connector-preset-custom')).toBeChecked()
    await page.getByTestId('connector-preset-answer').check()
    await expect(page.getByTestId('connector-matrix')).toHaveCount(0)
    await saveSheet(page)
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.caps).toEqual([])
    await openEdit(page)
    await expect(page.getByTestId('connector-preset-answer')).toBeChecked()
    await page.getByTestId('connector-preset-standard').check()
    await saveSheet(page)
    await expect.poll(() => a.s.calls.filter(c => c.method === 'PUT').at(-1)?.body.caps.length).toBeGreaterThan(0)
    const caps = a.s.calls.filter(c => c.method === 'PUT').at(-1).body.caps
    expect(caps).not.toContain('talkback.new_channel')
    expect(caps).not.toContain('tools.write')
  })

  test('Custom opens the whole matrix, and the matrix keeps its Reset to the usual set', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.goto(PAGE)
    await openEdit(page)
    const matrix = await openMatrix(page)
    await expect(matrix).toBeVisible()
    await expect(page.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
    await page.getByTestId('connector-usual').click()
    await expect(page.getByTestId('connector-cap-switch-files.upload')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('connector-cap-switch-talkback.new_channel')).toHaveAttribute('aria-checked', 'false')
  })

  test('a kind with no capabilities shows no presets', async ({ page }) => {
    await connectorApi(page, [], 'telegram', [], undefined, { capabilities: [] }).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-new').click()
    await expect(page.getByTestId('connector-sheet')).toBeVisible()
    await expect(page.getByTestId('connector-preset')).toHaveCount(0)
  })

  test('?new=1&agent= opens the add sheet at once with the agent fixed', async ({ page }) => {
    const a = connectorApi(page)
    await a.install()
    await page.goto(`${PAGE}?new=1&agent=a1`)
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet).toBeVisible()
    await expect(sheet.getByTestId('connector-agent-fixed')).toContainText('Answers with agent Support')
    await expect(sheet.getByTestId('connector-agent')).toHaveCount(0)
    await sheet.getByLabel('Name', { exact: true }).fill('Support bot')
    await sheet.getByLabel('Bot token').fill('1:x')
    await saveSheet(page)
    await expect.poll(() => a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors')?.body.agent_id).toBe('a1')
  })

  test('?new=1 with an unknown agent or an agent with no model opens the sheet with the agent to choose', async ({ page }) => {
    await connectorApi(page).install()
    await page.goto(`${PAGE}?new=1&agent=a2`)
    await expect(page.getByTestId('connector-sheet')).toBeVisible()
    await expect(page.getByTestId('connector-agent-fixed')).toHaveCount(0)
    await expect(page.getByTestId('connector-agent')).toHaveValue('')
  })

  test('?binding= opens the settings of that connection with its permissions in view', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.goto(`${PAGE}?binding=b1`)
    await expect(page.getByTestId('connector-sheet')).toBeVisible()
    await expect(page.getByTestId('connector-matrix')).toBeVisible()
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Support bot')
  })

  test('closing the sheet over a change asks first, and an untouched sheet closes at once', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.goto(PAGE)
    await openEdit(page)
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    // The menu of the row that opened it has the focus back.
    await expect(page.locator('.action-menu__trigger')).toBeFocused()

    await openEdit(page)
    await page.getByLabel('Name', { exact: true }).fill('Renamed')
    await page.keyboard.press('Escape')
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Discard your changes?')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(sheet).toBeVisible()
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Renamed')
    await sheet.getByTestId('connector-cancel').click()
    await dialog.getByRole('button', { name: 'Discard changes' }).click()
    await expect(sheet).toHaveCount(0)
  })

  test('Tab stays inside the sheet', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.goto(PAGE)
    await openEdit(page)
    for (let i = 0; i < 60; i++) {
      await page.keyboard.press('Tab')
      expect(await page.evaluate(() => Boolean(document.activeElement?.closest('[role="dialog"]')))).toBe(true)
    }
  })

  test('a save the server refuses keeps the sheet open and shows the reason inside it', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.route(/\/api\/chats\/connectors\/b1$/, r => (r.request().method() === 'PUT' ? r.fulfill({ status: 400, json: { error: { message: 'the platform refused the bot token' } } }) : r.fallback()))
    await page.goto(PAGE)
    await openEdit(page)
    await page.getByLabel('Name', { exact: true }).fill('Renamed')
    await saveSheet(page)
    await expect(page.getByTestId('connector-sheet-error')).toContainText('the platform refused the bot token')
    await expect(page.getByTestId('connector-sheet')).toBeVisible()
  })
  test('a new connection opens with one quiet line, no wall of warnings and never a Keep these changes action', async ({ page }) => {
    await connectorApi(page).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-new').click()
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet).toBeVisible()
    await expect(sheet.getByTestId('connector-readiness')).toHaveCount(0)
    const quiet = sheet.getByTestId('connector-ready-quiet')
    await expect(quiet).toHaveText(/^\d+ things? to set$/)
    await expect(quiet).toHaveAttribute('aria-expanded', 'false')
    await expect(sheet).not.toContainText('Not ready yet')
    await expect(sheet).not.toContainText('After you save')
    await expect(sheet).not.toContainText('Keep these changes')
    // Typing does not move the fields: the line stays a line.
    await sheet.getByLabel('Name', { exact: true }).fill('Support bot')
    await sheet.getByLabel('Bot token').fill('555:secret-token')
    await sheet.getByLabel('Agent', { exact: true }).selectOption('a1')
    await expect(sheet.getByTestId('connector-readiness')).toHaveCount(0)
    await expect(sheet.getByRole('button', { name: 'Keep these changes' })).toHaveCount(0)
    // Opening it lists the rows; the ones that wait for the save have no button.
    await quiet.click()
    await expect(quiet).toHaveAttribute('aria-expanded', 'true')
    await expect(sheet.getByTestId('connector-readiness')).toBeVisible()
    await expect(sheet.getByTestId('connector-ready-who')).toHaveAttribute('data-state', 'attention')
    // A row that only waits for the save is not a thing to set: it shows with the whole list, and has no button.
    await expect(sheet.getByTestId('connector-ready-token')).toHaveCount(0)
    await sheet.getByTestId('connector-ready-toggle').click()
    await expect(sheet.getByTestId('connector-ready-token')).toHaveAttribute('data-state', 'after_save')
    await expect(sheet.getByTestId('connector-ready-token').getByRole('button')).toHaveCount(0)
    await expect(sheet.getByRole('button', { name: 'Keep these changes' })).toHaveCount(0)
  })

  test('an existing connection keeps the status panel and its Keep these changes action', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.goto(PAGE)
    await openEdit(page)
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet.getByTestId('connector-ready-quiet')).toHaveCount(0)
    await expect(sheet.getByTestId('connector-readiness')).toBeVisible()
    await sheet.getByLabel('Bot token').fill('555:new-token')
    await expect(sheet.getByTestId('connector-ready-token').getByRole('button', { name: 'Keep these changes' })).toBeVisible()
  })

  test('Answer only says it adds no permission, and the note says where the agent side is set', async ({ page }) => {
    await connectorApi(page, [RUNNING]).install()
    await page.goto(PAGE)
    await openEdit(page)
    const presets = page.getByTestId('connector-preset')
    await expect(presets).toContainText('This connection adds no permission of its own')
    await expect(presets).not.toContainText('does nothing else')
    const note = page.getByTestId('connector-preset-note')
    await expect(note).toContainText('verified people can still use their write tools')
    await expect(note).toContainText('on the agent, under "What it can use"')
  })

  test('Save and test keeps the sheet open while it tests, and a failed test stays in the sheet with Test again', async ({ page }) => {
    const a = connectorApi(page)
    await a.install()
    let tests = 0
    let release
    const gate = new Promise(r => { release = r })
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, async r => {
      tests++
      if (tests === 1) { await gate; return r.fulfill({ status: 400, json: { error: { message: 'Unauthorized: the token was refused' } } }) }
      return r.fulfill({ json: { bot_id: 1, bot_username: 'support_bot' } })
    })
    await page.goto(PAGE)
    await page.getByTestId('connector-new').click()
    const sheet = page.getByTestId('connector-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Support bot')
    await sheet.getByLabel('Bot token').fill('555:secret-token')
    await sheet.getByLabel('Agent', { exact: true }).selectOption('a1')
    await saveSheet(page)
    // The test is running: the sheet stays, and the primary button is busy and disabled.
    const save = sheet.getByTestId('connector-save')
    await expect(save).toBeDisabled()
    await expect(save).toHaveAttribute('aria-busy', 'true')
    await expect(save).toHaveText('Testing...')
    await expect(sheet).toBeVisible()
    release()
    // It failed: the sheet stays with the reason, near the buttons, and says the connection is saved.
    const failed = sheet.getByTestId('connector-test-failed')
    await expect(failed).toBeVisible()
    await expect(failed).toContainText('The connection is saved, but the test failed.')
    await expect(failed).toContainText('the token was refused')
    await expect(failed.getByRole('alert')).toBeVisible()
    await expect(failed).toBeFocused()
    await expect(save).toBeEnabled()
    await expect(save).toHaveText('Save and test')
    // The token is saved and sealed: the form no longer holds it, so nothing offers to keep it.
    await expect(sheet.getByLabel('Bot token')).toHaveValue('')
    await expect(sheet.getByRole('button', { name: 'Keep these changes' })).toHaveCount(0)
    expect(a.s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/connectors')).toHaveLength(1)
    // The sheet is now the saved connection: the page keeps the failure of the test as it does for a row.
    await expect(page.getByTestId('connector-row')).toContainText('Support bot')
    await expect(sheet.locator('.dk-btn--primary')).toHaveCount(1)
    // Test again passes: the sheet closes with the result on the page, and nothing was saved twice.
    await failed.getByTestId('connector-test-again').click()
    await expect(page.getByText('Connected to @support_bot.')).toBeVisible()
    await expect(sheet).toHaveCount(0)
    expect(tests).toBe(2)
    expect(a.s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/connectors')).toHaveLength(1)
    expect(a.s.calls.filter(c => c.method === 'PUT')).toHaveLength(0)
  })

  test('after a failed test, Save and test updates the saved connection instead of creating another, and closing asks nothing', async ({ page }) => {
    const a = connectorApi(page)
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ status: 400, json: { error: { message: 'Unauthorized: the token was refused' } } }))
    await page.goto(PAGE)
    await page.getByTestId('connector-new').click()
    const sheet = page.getByTestId('connector-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Support bot')
    await sheet.getByLabel('Bot token').fill('555:secret-token')
    await sheet.getByLabel('Agent', { exact: true }).selectOption('a1')
    await saveSheet(page)
    await expect(sheet.getByTestId('connector-test-failed')).toBeVisible()
    await sheet.getByLabel('Bot token').fill('555:other-token')
    await saveSheet(page)
    await expect.poll(() => a.s.calls.filter(c => c.method === 'PUT').length).toBe(1)
    expect(a.s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/connectors')).toHaveLength(1)
    await expect(sheet.getByTestId('connector-test-failed')).toBeVisible()
    await sheet.getByTestId('connector-cancel').click()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    await expect(sheet).toHaveCount(0)
    await expect(page.getByTestId('connector-state')).toHaveText('Needs attention')
  })
})

// A tool-only kind (the kind says tool_only) has no agent: many agents use one
// connection through their own sets. The same page, drawn for a kind that is
// tool-only, hides everything that talks of one agent.
test.describe('A connection of a tool-only kind', () => {
  const KIND = { kindExtra: { agent_optional: true, tool_only: true }, capabilities: [] }
  const TOOLS = { ...OFF, id: 'b9', name: 'Work tools', agent_id: '', credentials_only: true, enabled: false, caps: [], used_by: 2 }
  const UNUSED = { ...TOOLS, id: 'b8', name: 'Spare tools', used_by: 0 }
  const ONE = { ...TOOLS, id: 'b7', name: 'Solo tools', used_by: 1 }

  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['telegram'] })
  })

  test('the row and the pane say how many agents use it, not which agent answers', async ({ page }) => {
    await connectorApi(page, [TOOLS, UNUSED, ONE], 'telegram', [], undefined, KIND).install()
    await page.goto(PAGE)
    const rows = page.getByTestId('connector-row')
    await expect(rows.nth(0).getByTestId('connector-facts')).toHaveText('telegram · Used by 2 agents')
    await expect(rows.nth(1).getByTestId('connector-facts')).toHaveText('telegram · Not used by any agent')
    await expect(rows.nth(2).getByTestId('connector-facts')).toHaveText('telegram · Used by 1 agent')
    await expect(rows.nth(0)).not.toContainText(/Agent:|answers/i)
    await expect(rows.nth(0).getByTestId('connector-switch')).toHaveCount(0)
    await openPane(page, 'Work tools')
    await expect(page.getByTestId('connector-used-by')).toHaveText('Used by 2 agents')
  })

  test('the sheet has no agent field and saves no agent, even when the page is opened for one', async ({ page }) => {
    const a = connectorApi(page, [], 'telegram', [], undefined, KIND)
    await a.install()
    await page.goto(`${PAGE}?new=1&agent=a1`)
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet).toBeVisible()
    await expect(sheet.getByTestId('connector-agent')).toHaveCount(0)
    await expect(sheet.getByTestId('connector-agent-fixed')).toHaveCount(0)
    await expect(sheet.getByTestId('connector-agent-link')).toHaveCount(0)
    await expect(sheet).not.toContainText(/without an agent|answers no message/i)
    await sheet.getByLabel('Name', { exact: true }).fill('Work tools')
    await sheet.getByLabel('Bot token').fill('1:x')
    await saveSheet(page)
    await expect.poll(() => a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors')?.body.agent_id).toBe('')
  })

  test('editing a saved connection shows no agent either', async ({ page }) => {
    await connectorApi(page, [TOOLS], 'telegram', [], undefined, KIND).install()
    await page.goto(PAGE)
    await openEdit(page)
    const sheet = page.getByTestId('connector-sheet')
    await expect(sheet.getByTestId('connector-agent')).toHaveCount(0)
    await expect(sheet.getByTestId('connector-readiness')).toHaveCount(0)
  })
})
