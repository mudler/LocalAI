import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, previewFor, CAPABILITIES, ROLES_KIND } from './connector-fixtures.js'
import { ADMIN, MEMBER, sessionFor } from './addons-fixtures.js'

// The permissions of a binding on the shared connector page (drawn here by the
// Slack add-on, whose kind declares roles): the notice of a binding that has no
// permission on, the one-click preset, the switches, the role matrix, the
// approved channels, pairing and the read-only access view.

const PAGE = '/app/addons/slack/apps'
const SHOTS = process.env.UI_SHOTS_DIR
const LEGACY = { id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001'], has_token: true, caps: [], grants: [], channels: [], paired: [] }
const SET = { ...LEGACY, caps: ['memory.write', 'talkback.channels'], grants: [{ role: 'workspace_admin', caps: ['memory.write'] }], channels: [{ id: 'C0123', label: 'ops', approved: true }], paired: [{ sender_id: 'U001', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }] }
const ACCESS = {
  binding: 'b1',
  subjects: [
    { id: 'anyone', title: 'A sender on the allowlist', decisions: [{ capability: 'memory.write', allowed: true }, { capability: 'files.upload', allowed: false, reason: 'not enabled for this bot' }] },
    { id: 'paired', title: 'A paired sender', decisions: [{ capability: 'memory.write', allowed: true, via: 'paired' }, { capability: 'talkback.new_channel', allowed: false, reason: 'not enabled for this bot' }] },
    { id: 'role:workspace_admin', title: 'Workspace admin', decisions: [{ capability: 'memory.write', allowed: true, via: 'role:workspace_admin' }] },
  ],
  recent: [
    { at: '2026-11-03T09:30:00Z', senders: ['U001'], capability: 'memory.write', allowed: true, via: 'paired' },
    { at: '2026-11-03T09:10:00Z', senders: ['U002'], roles: ['member'], capability: 'files.upload', allowed: false, reason: 'not enabled for this bot' },
  ],
}
// The latest add-on tool calls of the binding, newest first: names and codes of
// the host, never what was asked or answered.
const TOOL_OUTCOMES = [
  { at: '2026-11-05T09:41:00Z', senders: ['U001'], addon: 'github', tool: 'create-issue', outcome: 'error', status: 403, code: 'upstream_403', message: 'The service refused the call (403): the token lacks a permission. Give the token the permission or use another connection.' },
  { at: '2026-11-05T09:40:00Z', senders: ['U001'], addon: 'github', tool: 'create-issue', outcome: 'held', code: 'write_mode_propose', message: 'The change is waiting for the owner: this connection only proposes changes.' },
  { at: '2026-11-05T09:39:00Z', senders: ['U002'], addon: 'github', tool: 'create-issue', outcome: 'denied', code: 'sender_needs_pairing', message: 'This sender is not paired with the bot, so it cannot change things in other services.' },
  { at: '2026-11-05T09:38:00Z', senders: ['U001'], addon: 'github', tool: 'list-issues', outcome: 'ok' },
]
const api = (page, initial, extra = {}) => connectorApi(page, initial, 'slack', [], undefined, { kindExtra: ROLES_KIND, access: ACCESS, ...extra })

// A screenshot of one element, which a full-page image of a long form cannot show.
async function shotOf(locator, name) {
  if (SHOTS) await locator.screenshot({ path: `${SHOTS}/${name}.png` })
}

async function shot(page, name) {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true })
}

test.describe('Connector permissions', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['slack'] })
  })

  test('a binding with no permission says why features are off, and one click turns the usual set on', async ({ page }) => {
    const a = api(page, [LEGACY])
    await a.install()
    await page.goto(PAGE)
    const row = page.getByTestId('connector-row')
    await expect(row.getByTestId('connector-nocaps')).toContainText('these features are off')
    await expect(row.getByTestId('connector-nocaps')).toContainText('Receive files')
    await expect(row.getByTestId('connector-nocaps')).not.toContainText('Post to a new channel')
    await shot(page, 'perm-1-legacy-banner')
    await row.getByTestId('connector-usual').click()
    await expect(page.getByText('The usual permissions are saved for Team app.')).toBeVisible()
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.body.caps.sort()).toEqual(['files.upload', 'memory.write', 'proposals.confirm', 'talkback.channels', 'triggers.propose', 'triggers.run'])
    expect(put.body.caps).not.toContain('talkback.new_channel')
    expect(put.body.caps).not.toContain('tools.write')
    expect('token' in put.body).toBe(false)
    expect(put.body.allow).toEqual(['U001'])
  })

  test('the form lists every permission with a switch and a description, marks the sensitive one and previews the preset', async ({ page }) => {
    await api(page, [LEGACY]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-permissions')
    for (const c of CAPABILITIES) {
      await expect(box.getByTestId(`connector-cap-${c.name}`)).toContainText(c.title)
      await expect(box.getByTestId(`connector-cap-switch-${c.name}`)).toHaveAttribute('aria-checked', 'false')
    }
    await expect(box.getByTestId('connector-cap-sensitive-talkback.new_channel')).toHaveText('Paired senders only')
    await expect(box.getByTestId('connector-cap-sensitive-memory.write')).toHaveCount(0)
    await box.getByTestId('connector-usual').click()
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-talkback.new_channel')).toHaveAttribute('aria-checked', 'false')
    await expect(box.getByTestId('connector-nocaps')).toHaveCount(0)
    await shot(page, 'perm-2-form-switches')
  })

  test('"Allow the usual set" ticks the usual caps the agent has, disables the ones it lacks and says what it did', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3' }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-permissions')
    // The agent holds two of the permissions; the others are shown off and cannot be switched on.
    await expect(box.getByTestId('connector-cap-switch-triggers.propose')).toBeDisabled()
    await expect(box.getByTestId('connector-cap-switch-triggers.propose')).toHaveAccessibleDescription('The agent does not have this.')
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toBeEnabled()
    await box.getByRole('button', { name: 'Allow the usual set' }).click()
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-files.upload')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-proposals.confirm')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-triggers.propose')).toHaveAttribute('aria-checked', 'false')
    await expect(box.getByTestId('connector-cap-switch-talkback.new_channel')).toHaveAttribute('aria-checked', 'false')
    const done = box.getByTestId('connector-preset-done-usual')
    await expect(done).toContainText('Ticked 3')
    await expect(done).toContainText('Not ticked, because the agent does not have them')
    await expect(done).toContainText('Propose triggers')
    expect(a.s.calls.some(c => c.method === 'PUT')).toBe(false)
    await shot(page, 'perm-4-usual-set')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.caps.sort()).toEqual(['files.upload', 'memory.write', 'proposals.confirm'])
  })

  test('"Same as the agent" copies what the agent has, asks first when that includes a sensitive permission, and is not a live link', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, caps: ['tools.write'], agent_id: 'a1' }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-permissions')
    await expect(box).toContainText('does not follow later changes')
    await box.getByRole('button', { name: 'Same as the agent' }).click()
    const ask = box.getByTestId('connector-same-confirm')
    await expect(ask).toBeVisible()
    await expect(ask.getByRole('listitem')).toHaveCount(1)
    await expect(ask).toContainText('Post to a new channel')
    await expect(ask).not.toContainText('Save to memory')
    // Nothing changed until the owner confirms.
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'false')
    await shot(page, 'perm-5-same-confirm')
    await ask.getByRole('button', { name: 'Cancel' }).click()
    await expect(ask).toHaveCount(0)
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'false')
    await box.getByRole('button', { name: 'Same as the agent' }).click()
    await box.getByRole('button', { name: 'Copy them too' }).click()
    for (const n of ['memory.write', 'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel']) {
      await expect(box.getByTestId(`connector-cap-switch-${n}`)).toHaveAttribute('aria-checked', 'true')
    }
    // A cap that is not a grant of the agent stays as it was.
    await expect(box.getByTestId('connector-cap-switch-tools.write')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-preset-done-same')).toContainText('Copied 6 permissions of Support')
    await shot(page, 'perm-6-same-done')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.caps.length).toBe(7)
  })

  test('a copy without sensitive permissions needs no question, and both buttons say why they are off without an agent or without readable permissions', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3' }, { ...LEGACY, id: 'b2', name: 'Orphan', agent_id: 'gone' }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).first().click()
    const box = page.getByTestId('connector-permissions')
    await box.getByRole('button', { name: 'Same as the agent' }).click()
    await expect(box.getByTestId('connector-same-confirm')).toHaveCount(0)
    await expect(box.getByTestId('connector-preset-done-same')).toContainText('Copied 2 permissions of Careful')
    await page.getByRole('button', { name: 'Cancel' }).click()
    await page.getByRole('button', { name: 'Edit' }).nth(1).click()
    const orphan = page.getByTestId('connector-permissions')
    await expect(orphan.getByRole('button', { name: 'Same as the agent' })).toBeDisabled()
    await expect(orphan.getByRole('button', { name: 'Allow the usual set' })).toBeDisabled()
    await expect(orphan.getByRole('button', { name: 'Allow the usual set' })).toHaveAccessibleDescription('The permissions of this agent could not be read.')
    await page.getByLabel('Agent', { exact: true }).selectOption('')
    await expect(orphan.getByTestId('connector-preset-reason')).toContainText('Choose an agent first.')
  })

  test('a link with the id of a binding opens it at its permissions', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a3' }]).install()
    await page.goto(`${PAGE}?binding=b1`)
    await expect(page.getByTestId('connector-permissions')).toBeVisible()
    await expect(page.getByRole('form', { name: /Edit/ })).toBeVisible()
  })

  test('the page says the rule: the allowlist can use the ticked permissions, sensitive ones also need a pairing', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['triggers.propose', 'memory.write'] }]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-permissions')
    await expect(box.getByTestId('connector-rule')).toHaveText('People on the allowlist can use the ticked permissions. Sensitive ones also need the person to be paired.')
    // Nothing sensitive is ticked, so there is no pairing notice.
    await expect(box.getByTestId('connector-stuck')).toHaveCount(0)
    await expect(box.getByTestId('connector-channel-only')).toHaveCount(0)
  })

  test('a ticked sensitive permission with nobody paired is said on the page, with a way to pair, and goes away once someone is paired', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['talkback.new_channel', 'memory.write'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-permissions')
    const notice = box.getByTestId('connector-stuck')
    await expect(notice).toContainText('Sensitive permissions are ticked, but nobody is paired')
    await expect(notice).toContainText('Post to a new channel')
    await expect(notice).not.toContainText('Save to memory')
    await shot(page, 'perm-7-sensitive-nobody-paired')
    await notice.getByRole('button', { name: 'Pair your account' }).click()
    await expect(box.getByTestId('connector-pair-start')).toBeFocused()
  })

  test('the notice stays off with a live pairing and comes back when the pairing ended', async ({ page }) => {
    const sens = { ...SET, caps: ['talkback.new_channel'], grants: [] }
    await api(page, [sens]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-permissions').getByTestId('connector-stuck')).toHaveCount(0)
    await page.getByRole('button', { name: 'Cancel' }).click()
    await api(page, [{ ...sens, paired: [{ sender_id: 'U001', paired_at: '2026-01-01T10:00:00Z', expires_at: '2026-02-01T10:00:00Z' }] }]).install()
    await page.reload()
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-permissions').getByTestId('connector-stuck')).toContainText('Post to a new channel')
  })

  test('an allowlist with only channels says that members are not trusted one by one', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a1', allow: ['C0123'], caps: ['memory.write'] }]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-permissions')
    await expect(box.getByTestId('connector-channel-only')).toContainText('The allowlist has only channels')
    await expect(box.getByTestId('connector-channel-only')).toContainText('Add their user')
    await shot(page, 'perm-8-channel-only')
    await page.getByRole('textbox', { name: 'Allowed people +' }).fill('U001')
    await page.getByRole('textbox', { name: 'Allowed people +' }).press('Enter')
    await expect(box.getByTestId('connector-channel-only')).toHaveCount(0)
  })

  test('roles get only non-sensitive permissions that are switched on, and the request carries the grants', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, caps: ['memory.write', 'talkback.channels', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const roles = page.getByTestId('connector-roles')
    await expect(roles.getByRole('columnheader')).toHaveText(['Roles', 'Save to memory', 'Post to other channels'])
    await expect(roles.getByRole('checkbox', { name: 'Workspace admin: Post to a new channel' })).toHaveCount(0)
    await roles.getByRole('checkbox', { name: 'Workspace admin: Save to memory' }).check()
    await roles.getByRole('checkbox', { name: 'Channel member: Post to other channels' }).check()
    // Turning a permission off takes it from the roles too.
    await page.getByTestId('connector-cap-switch-talkback.channels').click()
    await expect(roles.getByRole('checkbox', { name: 'Channel member: Post to other channels' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    const body = a.s.calls.find(c => c.method === 'PUT').body
    expect(body.caps.sort()).toEqual(['memory.write', 'talkback.new_channel'])
    expect(body.grants).toEqual([{ role: 'workspace_admin', caps: ['memory.write'] }])
  })

  test('edits the channels it may post to, at most ten, and refuses a bad id', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-channels')
    await expect(box.getByTestId('connector-channel-id')).toHaveValue('C0123')
    await expect(box.getByTestId('connector-channel-label')).toHaveValue('ops')
    await expect(box.getByTestId('connector-channel-approved')).toHaveAttribute('aria-checked', 'true')
    await box.getByTestId('connector-channel-add').click()
    await expect(page.getByTestId('connector-channel-problem')).toContainText('needs an ID')
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await box.getByTestId('connector-channel-id').nth(1).fill('bad id')
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await box.getByTestId('connector-channel-id').nth(1).fill('C0999')
    await box.getByTestId('connector-channel-label').nth(1).fill('random')
    for (let i = 2; i < 10; i++) {
      await box.getByTestId('connector-channel-add').click()
      await box.getByTestId('connector-channel-id').nth(i).fill(`C${i}`)
    }
    await expect(box.getByTestId('connector-channel')).toHaveCount(10)
    await expect(box.getByTestId('connector-channel-add')).toBeDisabled()
    await shot(page, 'perm-3-channels')
    await box.getByRole('button', { name: 'Remove channel C9' }).click()
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    const sent = a.s.calls.find(c => c.method === 'PUT').body.channels
    expect(sent).toHaveLength(9)
    expect(sent[0]).toEqual({ id: 'C0123', label: 'ops', approved: true })
    expect(sent[1]).toEqual({ id: 'C0999', label: 'random', approved: false })
  })

  test('channels are off until talk-back is switched on', async ({ page }) => {
    await api(page, [LEGACY]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-channels')).toContainText('Switch on "Post to other channels"')
    await expect(page.getByTestId('connector-channel-add')).toHaveCount(0)
  })

  test('pairing shows the code once, lists paired senders with their expiry and removes one', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-pairing')
    await expect(box.getByTestId('connector-paired')).toContainText('U001')
    await expect(box.getByTestId('connector-paired')).toContainText('ends')
    await box.getByTestId('connector-pair-start').click()
    await expect(box.getByTestId('connector-pair-code')).toHaveText('LA-K7M2QX9P')
    await expect(box.getByTestId('connector-pair-code-box')).toContainText('shown only now')
    await shot(page, 'perm-4-pairing')
    await box.getByRole('button', { name: 'Hide the code' }).click()
    await expect(page.getByText('LA-K7M2QX9P')).toHaveCount(0)
    await box.getByRole('button', { name: 'Remove the pairing of U001' }).click()
    await expect(box).toContainText('Nobody is paired.')
    expect(a.s.calls.some(c => c.method === 'DELETE' && c.url === '/api/chats/connectors/b1/paired/U001')).toBe(true)
    // The page never puts the code in a request body of a save.
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    expect(JSON.stringify(a.s.calls.find(c => c.method === 'PUT').body)).not.toContain('LA-K7M2QX9P')
  })

  test('a new binding explains that pairing needs a saved one', async ({ page }) => {
    await api(page, []).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await expect(page.getByTestId('connector-pairing')).toContainText('Save this bot first')
    await expect(page.getByTestId('connector-access')).toContainText('Save this bot first')
    await expect(page.getByTestId('connector-nocaps')).toBeVisible()
  })

  test('the latest decisions are listed read only, newest first', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-access')
    await expect(box.getByTestId('connector-audit')).toContainText('allowed')
    await expect(box.getByTestId('connector-audit')).toContainText('denied')
    await expect(box.getByTestId('connector-audit')).toContainText('Receive files')
    await expect(box.getByRole('checkbox')).toHaveCount(0)
    await shot(page, 'perm-5-access')
  })

  test('the latest decisions also list the add-on tool calls, with the outcome, the reason and no content', async ({ page }) => {
    await api(page, [SET], { access: { ...ACCESS, tool_outcomes: TOOL_OUTCOMES } }).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const list = page.getByTestId('connector-access').getByTestId('connector-tool-outcomes')
    await expect(list.getByRole('listitem')).toHaveCount(4)
    const first = list.getByRole('listitem').nth(0)
    await expect(first).toContainText('github / create-issue')
    await expect(first).toContainText('failed')
    await expect(first).toContainText('the token lacks a permission')
    const held = list.getByRole('listitem').nth(1)
    await expect(held).toHaveAttribute('data-outcome', 'held')
    await expect(held).toContainText('held')
    await expect(held).toContainText('waiting for the owner')
    await expect(list.getByRole('listitem').nth(2)).toContainText('denied')
    await expect(list.getByRole('listitem').nth(3)).toContainText('ok')
    await expect(list.getByRole('listitem').nth(3)).toContainText('github / list-issues')
    await expect(page.getByTestId('connector-access')).toContainText('never what was asked or answered')
    await shot(page, 'perm-6-tool-outcomes')
  })

  test('the add-on tool calls say there are none yet', async ({ page }) => {
    await api(page, [SET]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-access')).toContainText('No tool call yet.')
  })

  test('What this bot can do shows each permission for the chosen sender, the host reason and one fix', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3', caps: ['memory.write', 'triggers.propose', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const can = page.getByTestId('connector-can')
    await expect(can.getByTestId('connector-can-memory.write')).toHaveAttribute('data-allowed', 'true')
    // The agent lacks the permission: the first blocker is the agent, and the fix opens the agent in a new tab.
    const trig = can.getByTestId('connector-can-triggers.propose')
    await expect(trig).toHaveAttribute('data-allowed', 'false')
    await expect(trig.getByTestId('connector-can-why')).toContainText('is not allowed to set reminders and scheduled tasks')
    await expect(trig.getByTestId('connector-fix')).toHaveAttribute('href', '/app/agents/a3/edit')
    await expect(trig.getByTestId('connector-fix')).toHaveAttribute('target', '_blank')
    await expect(trig.getByTestId('connector-fix')).toHaveCount(1)
    // Not ticked: the fix scrolls to the switch and focuses it.
    const files = can.getByTestId('connector-can-files.upload')
    await expect(files.getByTestId('connector-can-why')).toContainText('does not allow')
    // The sender is known (the selector), so only the other links are listed; none remain here.
    await expect(files).not.toContainText('Not checked yet')
    await expect(trig).toContainText('Not checked yet: the permissions of this connection.')
    await expect(trig).not.toContainText('who is sending')
    await files.getByRole('button', { name: 'Show the setting' }).click()
    await expect(page.getByTestId('connector-cap-switch-files.upload')).toBeFocused()
    await shot(page, 'can-1-listed')
    // A paired person: the same form, other trust.
    await can.getByTestId('connector-can-sender').selectOption('paired')
    await expect(can.getByTestId('connector-can-memory.write')).toContainText('because this person is paired')
    // A role of the kind.
    await can.getByTestId('connector-can-sender').selectOption('role:workspace_admin')
    await expect(can.getByTestId('connector-can-memory.write')).toHaveAttribute('data-allowed', 'false')
    await expect(can.getByTestId('connector-can-memory.write').getByRole('button', { name: 'Show the setting' })).toBeVisible()
    await expect(can.getByTestId('connector-can-sender').locator('option')).toHaveText(['A person on the allowed list', 'A member of an allowed channel', 'A person you paired', 'Role: Workspace admin', 'Role: Channel member'])
  })

  test('a fix that needs a pairing jumps to the pairing button, and a person to the allowed people', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', allow: ['C001'], caps: ['memory.write', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const can = page.getByTestId('connector-can')
    await expect(can.getByTestId('connector-can-talkback.new_channel').getByTestId('connector-can-why')).toContainText('paired')
    await can.getByTestId('connector-can-talkback.new_channel').getByRole('button', { name: 'Pair a sender' }).click()
    await expect(page.getByTestId('connector-pair-start')).toBeFocused()
    // A member of the listed channel is not trusted one by one: add the person.
    await can.getByTestId('connector-can-sender').selectOption('channel_member')
    await expect(can.getByTestId('connector-can-memory.write').getByTestId('connector-can-why')).toContainText('cannot save notes to memory for them')
    await can.getByTestId('connector-can-memory.write').getByRole('button', { name: 'Add a person' }).click()
    await expect(page.getByTestId('connector-allow').getByRole('textbox')).toBeFocused()
  })

  test('the preview is asked for the form as it is now, debounced, with no token, and says so', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', caps: [] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect.poll(() => a.s.previews.length).toBe(1)
    expect(a.s.previews[0].id).toBe('b1')
    expect(a.s.previews[0].body).toMatchObject({ agent_id: 'a1', accept_any: false, allow: ['U001'], caps: [], grants: [] })
    expect('kind' in a.s.previews[0].body).toBe(false)
    await expect(page.getByTestId('connector-can-dirty')).toHaveCount(0)
    await page.getByLabel('Bot token').fill('xoxb-secret')
    for (const n of ['memory.write', 'files.upload', 'triggers.propose']) await page.getByTestId(`connector-cap-switch-${n}`).click()
    await expect(page.getByTestId('connector-can-dirty')).toContainText('Computed from the form as it is now')
    await expect.poll(() => a.s.previews.at(-1).body.caps.sort()).toEqual(['files.upload', 'memory.write', 'triggers.propose'])
    expect(a.s.previews.length).toBeLessThanOrEqual(2)
    expect(JSON.stringify(a.s.previews)).not.toContain('xoxb-secret')
    await expect(page.getByTestId('connector-can-memory.write')).toHaveAttribute('data-allowed', 'true')
    // Nothing was saved by looking.
    expect(a.s.calls.some(c => c.method === 'PUT')).toBe(false)
  })

  test('a connection that is not saved yet is previewed under the id new with its kind', async ({ page }) => {
    const a = api(page, [])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await expect.poll(() => a.s.previews.at(-1)?.id).toBe('new')
    expect(a.s.previews.at(-1).body.kind).toBe('slack')
    await expect(page.getByTestId('connector-can-dirty')).toContainText('Not saved yet')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByTestId('connector-cap-switch-memory.write').click()
    await expect.poll(() => a.s.previews.at(-1).body).toMatchObject({ agent_id: 'a1', caps: ['memory.write'], kind: 'slack' })
    await expect(page.getByTestId('connector-can-memory.write')).toHaveAttribute('data-allowed', 'true')
    await shot(page, 'can-2-new')
  })

  test('the panel says it is checking, and shows the error of the host with a way to try again', async ({ page }) => {
    const slow = api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['memory.write'] }], { previewDelay: 1200 })
    await slow.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-can-status')).toHaveText('Checking what this bot can do')
    await expect(page.getByTestId('connector-can')).toHaveAttribute('aria-busy', 'true')
    await shot(page, 'can-3-loading')
    await expect(page.getByTestId('connector-can-memory.write')).toBeVisible()
    await expect(page.getByTestId('connector-can-status')).toHaveText('')
  })

  test('a failed preview shows a notice with the host reason, keeps the page usable and retries', async ({ page }) => {
    const a = connectorApi(page, [{ ...LEGACY, agent_id: 'a1' }], 'slack', [], undefined, { kindExtra: ROLES_KIND, access: ACCESS, previewStatus: 503 })
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const err = page.getByTestId('connector-can-error')
    await expect(err).toContainText('Could not check what this bot can do')
    await expect(err).toContainText('The check is not available right now.')
    await shot(page, 'can-4-error')
    const before = a.s.previews.length
    await page.getByTestId('connector-can-retry').click()
    await expect.poll(() => a.s.previews.length).toBeGreaterThan(before)
    await expect(page.getByRole('button', { name: 'Save' })).toBeEnabled()
  })

  test('in an Italian page the host is asked for Italian and its localized text is shown, with English as the fallback', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-language', 'it'))
    const capabilities = CAPABILITIES.map(c => (c.name === 'memory.write' ? { ...c, language: 'it', localized_title: 'Salva in memoria', localized_description: 'L\'agente può ricordare.' } : c))
    const preview = body => {
      const v = previewFor(body, [])
      for (const subject of v.subjects) {
        for (const d of subject.decisions) if (d.capability === 'memory.write') Object.assign(d, { allowed: false, code: 'connection_permission_off', blocked_by: 'connection', message: 'This connection does not allow the bot to save notes to memory.', localized_message: 'Questa connessione non permette al bot di salvare note in memoria.' })
      }
      return v
    }
    const a = connectorApi(page, [{ ...LEGACY, agent_id: 'a1', caps: ['memory.write'] }], 'slack', [], undefined, { kindExtra: ROLES_KIND, access: ACCESS, capabilities, preview })
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Modifica' }).click()
    const row = page.getByTestId('connector-can-memory.write')
    await expect(row).toContainText('Salva in memoria')
    await expect(row.getByTestId('connector-can-why')).toHaveText('Questa connessione non permette al bot di salvare note in memoria.')
    // A capability the host did not translate keeps its English title.
    await expect(page.getByTestId('connector-can-files.upload')).toContainText('Receive files')
    expect(a.s.urls.some(u => u.endsWith('/api/chats/connectors/kinds?lang=it'))).toBe(true)
    expect(a.s.urls.some(u => u.endsWith('/access/preview?lang=it'))).toBe(true)
    // A save tells the host the interface language, for the sentences the bot writes itself.
    await page.getByRole('button', { name: 'Salva' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.ui_language).toBe('it')
  })

  test('a blocker held by the server settings links to Settings for an administrator and says to ask one otherwise', async ({ page }) => {
    const blocked = body => {
      const v = previewFor(body, [])
      v.subjects = v.subjects.map(s => ({ ...s, decisions: s.decisions.map(d => d.capability === 'triggers.propose' ? { capability: d.capability, allowed: false, code: 'server_setting_off', blocked_by: 'server', message: 'An administrator turned reminders off for the whole server.', fix: { kind: 'open_server_setting', target: 'disable_triggers' } } : d) }))
      return v
    }
    for (const [user, admin] of [[ADMIN, true], [MEMBER, false]]) {
      const p = await page.context().newPage()
      await mockChat(p)
      await installStudioAddons(p, { ids: ['slack'] })
      await p.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(user, { addons: true }) }))
      await connectorApi(p, [{ ...LEGACY, agent_id: 'a1', caps: ['triggers.propose'] }], 'slack', [], undefined, { kindExtra: ROLES_KIND, preview: blocked }).install()
      await p.goto(PAGE)
      await p.getByRole('button', { name: 'Edit' }).click()
      const row = p.getByTestId('connector-can').getByTestId('connector-can-triggers.propose')
      await expect(row.getByTestId('connector-can-why')).toContainText('turned reminders off')
      if (admin) await expect(row.getByRole('link', { name: 'Open Settings' })).toHaveAttribute('href', '/app/settings')
      else {
        await expect(row.getByTestId('connector-fix-text')).toHaveText('Ask an administrator')
        await expect(row.getByRole('link')).toHaveCount(0)
      }
      await p.close()
    }
  })

  test('readiness lists only what needs attention and opens to every row; the channel-only trap is named', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', allow: ['C001'], caps: ['memory.write'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const ready = page.getByTestId('connector-readiness')
    const who = ready.getByTestId('connector-ready-who')
    await expect(who).toHaveAttribute('data-state', 'attention')
    await expect(who).toContainText('Only channels are on the list')
    await expect(ready.getByTestId('connector-ready-token')).toHaveCount(0)
    await expect(ready.getByTestId('connector-ready-toggle')).toHaveText(/Show all \d/)
    await shot(page, 'ready-1-trap')
    await who.getByRole('button', { name: 'Add a person' }).click()
    await expect(page.getByTestId('connector-allow').getByRole('textbox')).toBeFocused()
    await ready.getByTestId('connector-ready-toggle').click()
    await expect(ready.getByRole('listitem')).toHaveCount(8)
    await expect(ready.getByTestId('connector-ready-token')).toHaveAttribute('data-state', 'ok')
    await expect(ready.getByTestId('connector-ready-triggers')).toHaveAttribute('data-state', 'off')
    await expect(ready.getByTestId('connector-ready-notify')).toHaveAttribute('data-state', 'off')
    await expect(ready.getByTestId('connector-ready-toggle')).toHaveAttribute('aria-expanded', 'true')
    await shot(page, 'ready-2-all')
  })

  test('readiness follows the form: a ticked permission that nobody can use, then a fix, then a row that works after saving', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3', caps: ['memory.write', 'triggers.propose'], approval: { mode: 'ask' } }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const ready = page.getByTestId('connector-readiness')
    await ready.getByTestId('connector-ready-toggle').click()
    // The agent lacks Propose triggers: the row shows the host sentence and a link to the agent.
    const trig = ready.getByTestId('connector-ready-triggers')
    await expect(trig).toHaveAttribute('data-state', 'attention')
    await expect(trig).toContainText('is not allowed to set reminders and scheduled tasks')
    await expect(trig.getByTestId('connector-fix')).toHaveAttribute('href', '/app/agents/a3/edit')
    // Choosing an agent that has it changes the row without a save; the row then works after a save.
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await expect(trig).toHaveAttribute('data-state', 'after_save')
    await expect(trig).toContainText('You confirm each one')
    await trig.getByRole('button', { name: 'Keep these changes' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.agent_id).toBe('a1')
  })

  test('readiness says when the agent has no model, and when no permission is on', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a2', caps: [] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const ready = page.getByTestId('connector-readiness')
    await expect(ready.getByTestId('connector-ready-agent')).toHaveAttribute('data-state', 'broken')
    await expect(ready.getByTestId('connector-ready-agent')).toContainText('has no model')
    await expect(ready.getByTestId('connector-ready-permissions')).toHaveAttribute('data-state', 'attention')
    await ready.getByTestId('connector-ready-permissions').getByRole('button', { name: 'Choose permissions' }).click()
    await expect(page.getByTestId('connector-presets').getByTestId('connector-usual')).toBeFocused()
  })

  test('the approval of proposed triggers sits under Propose triggers, shows its choice in the row and is greyed while the permission is off', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, caps: ['memory.write'], approval: { mode: 'auto_readonly' } }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const row = page.getByTestId('connector-cap-triggers.propose')
    // One place only: nothing at the bottom of the page.
    await expect(page.getByTestId('connector-approval')).toHaveCount(1)
    await expect(row.getByTestId('connector-approval')).toBeVisible()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: at once if read-only')
    // Permission off: greyed, with the reason, and the radios cannot be used.
    await expect(row.getByTestId('connector-approval-off')).toHaveText('Turn on Propose triggers to use this.')
    await expect(row.getByRole('radio').first()).toBeDisabled()
    await expect(row.getByTestId('connector-approval')).toHaveAccessibleDescription('Turn on Propose triggers to use this.')
    await shot(page, 'perm-9-approval-greyed')
    await row.getByTestId('connector-cap-switch-triggers.propose').click()
    await expect(row.getByTestId('connector-approval-off')).toHaveCount(0)
    await expect(row.getByRole('radio', { name: /Ask me/ })).toBeEnabled()
    // The three modes change the line of the row.
    await row.getByRole('radio', { name: /Ask me/ }).check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: asks first')
    await shot(page, 'perm-10-approval-ask')
    await row.getByRole('radio', { name: /Set it up at once/ }).check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: set up at once')
    await shot(page, 'perm-11-approval-auto')
    await row.getByRole('radio', { name: /At once when the runs only read/ }).check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: at once if read-only')
    await shot(page, 'perm-12-approval-readonly')
    // The limits are in a closed disclosure and are sent as before.
    const limits = row.getByTestId('connector-approval-limits')
    await expect(limits).not.toHaveAttribute('open', '')
    await limits.getByText('Limits', { exact: true }).click()
    await limits.getByTestId('connector-approval-max_pending').fill('3')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.approval).toEqual({ mode: 'auto_readonly', acts: [], max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('Runs may act sits under the approval mode, is off by default, lists the four acts, disables what the agent lacks and is sent with the whole approval', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a4', caps: ['triggers.propose'], approval: { mode: 'auto', acts: [], max_pending: 3 } }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const row = page.getByTestId('connector-cap-triggers.propose')
    const acts = row.getByTestId('connector-acts')
    await expect(acts.getByTestId('connector-acts-switch')).toHaveAttribute('aria-checked', 'false')
    await expect(acts.getByRole('checkbox')).toHaveCount(0)
    await expect(acts.getByTestId('connector-acts-modes')).toHaveText('Ask me: you always decide. At once: only when you are paired as sender and every act is ticked here.')
    await shot(page, 'acts-1-off')
    await acts.getByTestId('connector-acts-switch').click()
    await expect(acts.getByRole('checkbox')).toHaveCount(4)
    await expect(acts.getByTestId('connector-act-act.files')).toContainText("Write files in the agent's folders")
    await expect(acts.getByTestId('connector-act-act.addons')).toContainText("Use the actions of the agent's add-ons")
    await expect(acts.getByTestId('connector-act-act.send')).toContainText('Send messages beyond where it reports')
    await expect(acts.getByTestId('connector-act-act.spend')).toContainText('Use paid or external tools and the network')
    await expect(acts.getByText('Sensitive')).toHaveCount(4)
    // The agent holds files and add-ons only.
    await expect(acts.getByTestId('connector-act-check-act.files')).toBeEnabled()
    await expect(acts.getByTestId('connector-act-check-act.send')).toBeDisabled()
    await expect(acts.getByTestId('connector-act-lacks-act.send')).toHaveText('The agent does not have this.')
    await expect(acts.getByTestId('connector-act-check-act.send')).toHaveAccessibleDescription('The agent does not have this.')
    await expect(acts.getByTestId('connector-act-lacks-act.files')).toHaveCount(0)
    await shot(page, 'acts-3-lacks')
    await acts.getByTestId('connector-act-check-act.addons').check()
    await acts.getByTestId('connector-act-check-act.files').check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: set up at once, runs may: write files, use add-ons')
    await shot(page, 'acts-2-on')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.approval).toEqual({ mode: 'auto', acts: ['act.files', 'act.addons'], max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('a ticked act the agent no longer holds stays ticked and can be unticked; turning Runs may act off sends an empty list', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['triggers.propose'], approval: { mode: 'ask', acts: ['act.send', 'act.spend'] } }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const acts = page.getByTestId('connector-acts')
    await expect(acts.getByTestId('connector-acts-switch')).toHaveAttribute('aria-checked', 'true')
    await expect(acts.getByTestId('connector-act-check-act.send')).toBeChecked()
    await expect(acts.getByTestId('connector-act-check-act.send')).toBeEnabled()
    await expect(acts.getByTestId('connector-act-lacks-act.send')).toBeVisible()
    await expect(acts.getByTestId('connector-act-check-act.files')).toBeDisabled()
    await acts.getByTestId('connector-act-check-act.send').uncheck()
    await expect(acts.getByTestId('connector-act-check-act.send')).toBeDisabled()
    await acts.getByTestId('connector-acts-switch').click()
    await expect(acts.getByRole('checkbox')).toHaveCount(0)
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.approval).toEqual({ mode: 'ask', acts: [], max_pending: 0, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('Runs may act is greyed with the reason while Propose triggers is off', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a4', caps: ['memory.write'], approval: { mode: 'ask', acts: ['act.files'] } }]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const row = page.getByTestId('connector-cap-triggers.propose')
    await expect(row.getByTestId('connector-approval-off')).toHaveText('Turn on Propose triggers to use this.')
    await expect(row.getByTestId('connector-acts-switch')).toBeDisabled()
    await expect(row.getByTestId('connector-act-check-act.files')).toBeDisabled()
    await shot(page, 'acts-4-greyed')
    await row.getByTestId('connector-cap-switch-triggers.propose').click()
    await expect(row.getByTestId('connector-act-check-act.files')).toBeEnabled()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: asks first, runs may: write files')
    await shot(page, 'acts-5-summary')
  })

  test('write tools of an add-on the agent is connected to: paired people have them without the tick, the row and the access view say why', async ({ page }) => {
    const hold = { addon: 'tracker', capability: 'tools.write', tools: ['create-issue', 'comment-issue'], connections: [{ name: 'Work tracker', write_mode: 'propose' }], without_tick: ['paired'] }
    const cover = (view) => ({
      ...view,
      addon_tools: [hold],
      subjects: view.subjects.map((x) => ({ ...x, decisions: x.decisions.map((d) => (x.id === 'paired' && d.capability === 'tools.write' ? { capability: 'tools.write', allowed: true, via: 'agent-connection' } : d)) })),
    })
    const a = api(page, [SET], { access: cover(ACCESS), preview: (body) => (body.caps?.includes('tools.write') ? { ...previewFor(body, ROLES_KIND.roles), addon_tools: [{ ...hold, without_tick: [] }] } : cover(previewFor(body, ROLES_KIND.roles))) })
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const ready = page.getByTestId('connector-readiness')
    await ready.getByTestId('connector-ready-toggle').click()
    // One info row, once: who has the tools, through which connection and mode, and who needs the tick.
    const row = ready.getByTestId('connector-ready-addons:tracker')
    await expect(row).toHaveAttribute('data-state', 'ok')
    await expect(row).toContainText('Paired people can use the tracker write tools create-issue, comment-issue in this chat')
    await expect(row).toContainText('connected to Work tracker (write mode: ask first)')
    await expect(row).toContainText('People who are not paired need "Let people who are not paired change things with add-on tools"')
    await expect(ready.locator('[data-testid^="connector-ready-addons:"]')).toHaveCount(1)
    // The preview of the paired sender says the tools are allowed because of the connection; the listed sender needs the tick.
    const can = page.getByTestId('connector-can')
    await can.getByTestId('connector-can-sender').selectOption('paired')
    await expect(can).toContainText('because this person is paired and the agent is connected to the add-on')
    await shot(page, 'paired-write-1-paired-sender')
    await can.getByTestId('connector-can-sender').selectOption('listed')
    await expect(can).toContainText('Tick the matching permission on the connection')
    await shot(page, 'paired-write-2-listed-sender')
    await shot(page, 'paired-write-3-readiness')
    // Ticking the permission turns the row into the usual one.
    await page.getByTestId('connector-cap-switch-tools.write').click()
    await expect(ready.getByTestId('connector-ready-addons:tracker')).toContainText('The agent can use the tracker tools create-issue, comment-issue in this chat')
  })

  test('write tools: the owner took the tick away, or the connection is in write mode off: the row says which', async ({ page }) => {
    const holdOf = (extra) => ({ addon: 'tracker', capability: 'tools.write', tools: ['create-issue'], connections: [{ name: 'Work tracker', write_mode: 'off' }], ...extra })
    for (const [extra, text] of [[{ tick_removed: true, connections: [{ name: 'Work tracker', write_mode: 'auto' }] }, 'You turned off'], [{}, 'the write mode of Work tracker is off']]) {
      const a = api(page, [SET], { access: { ...ACCESS, addon_tools: [holdOf(extra)] }, preview: (body) => ({ ...previewFor(body, ROLES_KIND.roles), addon_tools: [holdOf(extra)] }) })
      await a.install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      await expect(page.getByTestId('connector-readiness').getByTestId('connector-ready-addons:tracker')).toContainText(text)
      await page.unrouteAll({ behavior: 'ignoreErrors' })
      await mockChat(page)
      await installStudioAddons(page, { ids: ['slack'] })
    }
  })

  test('an older host that sends no capability list shows no permission section', async ({ page }) => {
    await connectorApi(page, [LEGACY], 'slack', [], undefined, { capabilities: null }).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('connector-row')).toContainText('Team app')
    await expect(page.getByTestId('connector-nocaps')).toHaveCount(0)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-permissions')).toHaveCount(0)
  })
  test('a tool the owner relaxed is listed as running without asking, for the owner and paired senders only', async ({ page }) => {
    const approvals = [
      { tool: 'power-action', skip: true, runs_without_asking: ['owner', 'paired'] },
      { tool: 'send-key', skip: false },
    ]
    const a = api(page, [SET], {
      access: { ...ACCESS, approvals },
      preview: (body) => ({ ...previewFor(body, ROLES_KIND.roles), approvals }),
    })
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    // The saved view is the owner's own: the relaxed tool runs, the other asks.
    const saved = page.getByTestId('connector-access-approvals')
    await expect(saved.getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'runs')
    await expect(saved.getByTestId('connector-approval-power-action')).toContainText('Runs without asking')
    await expect(saved.getByTestId('connector-approval-send-key')).toHaveAttribute('data-state', 'asks')
    // The preview follows the kind of sender chosen: only a paired one skips the card.
    const can = page.getByTestId('connector-can')
    const listed = can.getByTestId('connector-can-approvals')
    await expect(listed.getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'other')
    await expect(listed.getByTestId('connector-approval-power-action')).toContainText('Still asks this sender')
    await can.getByTestId('connector-can-sender').selectOption('paired')
    await expect(can.getByTestId('connector-can-approvals').getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'runs')
    await can.getByTestId('connector-can-sender').selectOption('channel_member')
    await expect(can.getByTestId('connector-can-approvals').getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'other')
    await shot(page, 'perm-6-approvals')
  })

  test('an agent that saves notes at once says so in the access view and the preview, for the paired sender', async ({ page }) => {
    const mark = (view) => ({ ...view, subjects: view.subjects.map((x) => ({ ...x, note_save: x.id === 'paired' ? 'now' : 'ask' })) })
    const a = api(page, [SET], {
      access: mark(ACCESS),
      preview: (body) => mark(previewFor(body, ROLES_KIND.roles)),
    })
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const saved = page.getByTestId('connector-access-notesave')
    await expect(saved.getByTestId('connector-access-notesave-paired')).toContainText('Saves notes at once and tells you, with an Undo')
    await expect(saved.getByTestId('connector-access-notesave-anyone')).toContainText('wait for the owner')
    const can = page.getByTestId('connector-can')
    await expect(can.getByTestId('connector-can-notesave')).toContainText('wait for the owner')
    await can.getByTestId('connector-can-sender').selectOption('paired')
    await expect(can.getByTestId('connector-can-notesave')).toContainText('Saves notes at once and tells you, with an Undo')
    await shot(page, 'perm-7-notesave')
  })

  test('an agent without the rule shows no note line', async ({ page }) => {
    await api(page, [SET]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-access')).toBeVisible()
    await expect(page.getByTestId('connector-access-notesave')).toHaveCount(0)
    await expect(page.getByTestId('connector-can-notesave')).toHaveCount(0)
  })

  test('a run without asking appears in the latest decisions in plain words, with no typed text', async ({ page }) => {
    const recent = [{ at: '2026-11-03T09:30:00Z', senders: ['U001'], capability: 'tools.write', allowed: true, via: 'no-confirmation', reason: 'power-action: applied' }]
    const a = api(page, [SET], { access: { ...ACCESS, recent } })
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const audit = page.getByTestId('connector-audit')
    await expect(audit).toContainText('ran without asking')
    await expect(audit).toContainText('power-action: applied')
    await expect(audit).not.toContainText('via no-confirmation')
  })

  test.describe('channels where the bot listens', () => {
    const STORED = { ...SET, allow: ['U001', 'C0123'], caps: ['files.upload', 'memory.write', 'talkback.channels'] }

    test('a channel that is added starts as Let everyone here ask for actions, and the choice goes back to Answer only', async ({ page }) => {
      const a = api(page, [{ ...SET, allow: ['U001'], caps: ['files.upload', 'memory.write'] }])
      await a.install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      await page.getByRole('textbox', { name: 'Allowed channels +' }).fill('C0555')
      await page.getByRole('textbox', { name: 'Allowed channels +' }).press('Enter')
      const row = page.getByTestId('connector-trust-row')
      await expect(row).toHaveCount(1)
      await expect(row).toHaveAttribute('data-level', 'act')
      await expect(row.getByRole('radio', { name: 'Let everyone here ask for actions' })).toBeChecked()
      await expect(page.getByTestId('connector-trust-effect')).toContainText('Anyone who can post here can make the bot read their files')
      await expect(page.getByTestId('connector-trust-effect')).toContainText('Sensitive things still need pairing')
      await expect(page.getByTestId('connector-ready-channelTrust')).toContainText('People in C0555 can ask for everyday actions')
      await shot(page, 'trust-1-new-channel-act')
      await shotOf(page.getByTestId('connector-who'), 'trust-1b-who-section')
      await shotOf(page.getByTestId('connector-trust'), 'trust-1c-channels-section')
      await shotOf(page.getByTestId('connector-readiness'), 'trust-1d-readiness')
      await row.getByRole('radio', { name: 'Answer only' }).check()
      await expect(row).toHaveAttribute('data-level', 'answer')
      await expect(page.getByTestId('connector-trust-effect')).toContainText('only people on the allowed list can ask for actions')
      await expect(page.getByTestId('connector-ready-channelTrust')).toContainText('People in C0555 cannot use actions: set it to Let everyone here ask for actions')
      await shot(page, 'trust-2-answer-only')
      await shotOf(page.getByTestId('connector-trust'), 'trust-2b-channels-answer-only')
      await shotOf(page.getByTestId('connector-readiness'), 'trust-2c-readiness')
      await page.getByRole('button', { name: 'Save' }).click()
      await expect.poll(() => a.s.calls.some((c) => c.method === 'PUT')).toBe(true)
      expect(a.s.calls.find((c) => c.method === 'PUT').body.allow_trust).toEqual({ C0555: 'answer' })
    })

    test('a channel stored before the setting reads as Answer only and is not widened by a save', async ({ page }) => {
      const a = api(page, [STORED])
      await a.install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      const row = page.getByTestId('connector-trust-row')
      await expect(row).toHaveAttribute('data-level', 'answer')
      await expect(page.getByTestId('connector-ready-channelTrust')).toContainText('People in C0123 cannot use actions')
      await page.getByTestId('connector-ready-toggle').click()
      await expect(page.getByTestId('connector-ready-postTo')).toContainText('may post to 1 other channel when')
      await shot(page, 'trust-3-stored-channel')
      await shotOf(page.getByTestId('connector-readiness'), 'trust-3b-readiness-expanded')
      await shotOf(page.getByTestId('connector-channels'), 'trust-3c-where-it-may-post')
      await page.getByRole('button', { name: 'Save' }).click()
      await expect.poll(() => a.s.calls.some((c) => c.method === 'PUT')).toBe(true)
      expect(a.s.calls.find((c) => c.method === 'PUT').body.allow_trust).toEqual({})
    })

    test('the talk-back list is named Where it may post and says it trusts nobody', async ({ page }) => {
      await api(page, [STORED]).install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      const box = page.getByTestId('connector-channels')
      await expect(box).toContainText('Where it may post')
      await expect(box).toContainText('This does not decide who the bot listens to')
      await expect(box).not.toContainText('Approved')
    })

    test('with no permission ticked the page says files are still refused', async ({ page }) => {
      await api(page, [{ ...STORED, caps: [], allow_trust: { C0123: 'act' } }]).install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      await expect(page.getByTestId('connector-trust-nocaps')).toContainText('No permission is ticked on this connection')
      await expect(page.getByTestId('connector-ready-channelTrust')).toContainText('Nothing is ticked under permissions')
    })

    test('the channel rows do not overflow a 320 px screen', async ({ page }) => {
      await page.setViewportSize({ width: 320, height: 800 })
      await api(page, [{ ...STORED, allow: ['U001', 'C0123', 'C0456'], allow_trust: { C0456: 'act' } }]).install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      await expect(page.getByTestId('connector-trust-row')).toHaveCount(2)
      // Only the section of this change is measured: the role matrix has its own scroll box.
      const over = await page.evaluate(() => [...document.querySelectorAll('[data-testid="connector-trust"], [data-testid="connector-trust"] *')]
        .filter((e) => e.getBoundingClientRect().right > document.documentElement.clientWidth + 1).map((e) => e.tagName + '.' + String(e.className)))
      expect(over, 'elements of the channel section wider than the screen').toEqual([])
      await shot(page, 'trust-4-320px')
      await shotOf(page.getByTestId('connector-trust'), 'trust-4b-320px-channels')
    })

    test('a refusal of a sender who came in through a channel offers Allow this person', async ({ page }) => {
      const refused = { at: '2026-11-03T09:10:00Z', senders: ['U777'], capability: 'files.upload', allowed: false, code: 'channel_not_trusted', reason: 'files.upload needs a trusted sender: sender U777 was let in only through a channel set to Answer only; add U777 to the allowed people, or set the channel to Let everyone here ask for actions' }
      const a = api(page, [STORED], { access: { ...ACCESS, recent: [refused] } })
      await a.install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      await shot(page, 'trust-5-refusal')
      await shotOf(page.getByTestId('connector-access'), 'trust-5b-refusal-allow-person')
      await page.getByTestId('connector-audit-allow').click()
      await expect.poll(() => a.s.calls.some((c) => c.method === 'POST' && c.url.endsWith('/allow'))).toBe(true)
    })
  })
})
