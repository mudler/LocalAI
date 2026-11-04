import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, CAPABILITIES, ROLES_KIND } from './connector-fixtures.js'

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
const api = (page, initial) => connectorApi(page, initial, 'slack', [], undefined, { kindExtra: ROLES_KIND, access: ACCESS })

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

  test('roles get only non-sensitive permissions that are switched on, and the request carries the grants', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, caps: ['memory.write', 'talkback.channels', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const roles = page.getByTestId('connector-roles')
    await expect(roles.getByRole('columnheader')).toHaveText(['Roles', 'Save to memory', 'Post to approved channels'])
    await expect(roles.getByRole('checkbox', { name: 'Workspace admin: Post to a new channel' })).toHaveCount(0)
    await roles.getByRole('checkbox', { name: 'Workspace admin: Save to memory' }).check()
    await roles.getByRole('checkbox', { name: 'Channel member: Post to approved channels' }).check()
    // Turning a permission off takes it from the roles too.
    await page.getByTestId('connector-cap-switch-talkback.channels').click()
    await expect(roles.getByRole('checkbox', { name: 'Channel member: Post to approved channels' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    const body = a.s.calls.find(c => c.method === 'PUT').body
    expect(body.caps.sort()).toEqual(['memory.write', 'talkback.new_channel'])
    expect(body.grants).toEqual([{ role: 'workspace_admin', caps: ['memory.write'] }])
  })

  test('edits the approved channels, at most ten, and refuses a bad id', async ({ page }) => {
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
    await expect(page.getByTestId('connector-channels')).toContainText('Switch on "Post to approved channels"')
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

  test('shows what each sender may do and the latest decisions, read only', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const box = page.getByTestId('connector-access')
    await expect(box.getByTestId('connector-access-anyone')).toContainText('May: Save to memory')
    await expect(box.getByTestId('connector-access-anyone')).toContainText('May not: Receive files')
    await expect(box.getByTestId('connector-access-role:workspace_admin')).toContainText('Workspace admin')
    await expect(box.getByTestId('connector-audit')).toContainText('allowed')
    await expect(box.getByTestId('connector-audit')).toContainText('denied')
    await expect(box.getByTestId('connector-audit')).toContainText('Receive files')
    await box.getByTestId('connector-access-anyone').getByText('May not:').click()
    await expect(box.getByTestId('connector-access-anyone')).toContainText('not enabled for this bot')
    await expect(box.getByRole('checkbox')).toHaveCount(0)
    await shot(page, 'perm-5-access')
  })

  test('trigger approval says it applies only while triggers are allowed', async ({ page }) => {
    await api(page, [SET]).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-approval-off')).toBeVisible()
    await page.getByTestId('connector-cap-switch-triggers.propose').click()
    await expect(page.getByTestId('connector-approval-off')).toHaveCount(0)
  })

  test('an older host that sends no capability list shows no permission section', async ({ page }) => {
    await connectorApi(page, [LEGACY], 'slack', [], undefined, { capabilities: null }).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('connector-row')).toContainText('Team app')
    await expect(page.getByTestId('connector-nocaps')).toHaveCount(0)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByTestId('connector-permissions')).toHaveCount(0)
  })
})
