import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, previewFor, CAPABILITIES, ROLES_KIND, openAnswerMore, clippedOptions, openEdit, expandAll, rowMenu } from './connector-fixtures.js'
import { ADMIN, MEMBER, sessionFor } from './addons-fixtures.js'
import { freezeClock } from './clock-fixtures.js'

// The permissions of a binding on the shared connector page (drawn here by the
// Slack add-on, whose kind declares roles): the matrix of who can do what (the
// switches, the role boxes, the cells and their messages), the places the bot
// may post, pairing and the read-only access view.

const PAGE = '/app/addons/slack/apps'
const SHOTS = process.env.UI_SHOTS_DIR
const LEGACY = { id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001'], has_token: true, caps: [], grants: [], channels: [], paired: [] }
const SET = { ...LEGACY, caps: ['memory.write', 'talkback.channels'], grants: [{ role: 'workspace_admin', caps: ['memory.write'] }], channels: [{ id: 'C0123', label: 'ops', approved: true }], paired: [{ sender_id: 'U001', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }] }
const ACCESS = {
  binding: 'b1',
  subjects: [
    { id: 'anyone', title: 'A listed person', decisions: [{ capability: 'memory.write', allowed: true }, { capability: 'files.upload', allowed: false, reason: 'not enabled for this bot' }] },
    { id: 'paired', title: 'A verified person', decisions: [{ capability: 'memory.write', allowed: true, via: 'paired' }, { capability: 'talkback.new_channel', allowed: false, reason: 'not enabled for this bot' }] },
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
  { at: '2026-11-05T09:39:00Z', senders: ['U002'], addon: 'github', tool: 'create-issue', outcome: 'denied', code: 'sender_needs_pairing', message: 'This sender is not verified with the bot, so it cannot change things in other services.' },
  { at: '2026-11-05T09:38:00Z', senders: ['U001'], addon: 'github', tool: 'list-issues', outcome: 'ok' },
]
const api = (page, initial, extra = {}) => connectorApi(page, initial, 'slack', [], undefined, { kindExtra: ROLES_KIND, access: ACCESS, ...extra })

// A screenshot of one element, which a full-page image of a long form cannot show.
async function shotOf(locator, name) {
  if (SHOTS) await locator.screenshot({ path: `${SHOTS}/${name}.png` })
}

// The disclosure of a row of the matrix, opened.
async function disclosure(page, cap) {
  const more = page.getByTestId(`connector-matrix-more-${cap}`)
  if ((await more.getAttribute('aria-expanded')) !== 'true') await more.click()
  return page.getByTestId(`connector-matrix-disclosure-${cap}`)
}

async function shot(page, name) {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true })
}

test.describe('Connector permissions', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['slack'] })
  })

  test('a saved binding with no permission shows every switch off, and Reset to the usual set turns the usual ones on without saving', async ({ page }) => {
    const a = api(page, [LEGACY])
    await a.install()
    await page.goto(PAGE)
    await expect(page.getByTestId('connector-row').getByTestId('connector-nocaps')).toHaveCount(0)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    await expect(mx.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'false')
    await mx.getByTestId('connector-usual').click()
    await expect(mx.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
    await expect(mx.getByTestId('connector-cap-switch-talkback.new_channel')).toHaveAttribute('aria-checked', 'false')
    await expect(mx.getByTestId('connector-cap-switch-tools.write')).toHaveAttribute('aria-checked', 'false')
    expect(a.s.calls.some(c => c.method === 'PUT')).toBe(false)
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.caps.sort()).toEqual(['files.upload', 'memory.write', 'proposals.confirm', 'talkback.channels', 'triggers.propose', 'triggers.run'])
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect('token' in put.body).toBe(false)
    expect(put.body.allow).toEqual(['U001'])
  })

  test('the matrix lists every permission with a switch, marks the sensitive one and has no list of switches besides it', async ({ page }) => {
    await api(page, [LEGACY]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-matrix')
    for (const c of CAPABILITIES) {
      if (c.name !== 'tools.write') await expect(box.getByTestId(`connector-matrix-row-${c.name}`)).toContainText(c.title.charAt(0).toUpperCase() + c.title.slice(1))
      await expect(box.getByTestId(`connector-cap-switch-${c.name}`)).toHaveAttribute('aria-checked', 'false')
    }
    await expect(page.locator('[data-testid^="connector-cap-switch-"]')).toHaveCount(CAPABILITIES.length)
    await expect(box.getByTestId('connector-cap-sensitive-talkback.new_channel')).toHaveText('Verified people only')
    await expect(box.getByTestId('connector-cap-sensitive-memory.write')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Same as the agent' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Allow the usual set' })).toHaveCount(0)
    await shot(page, 'perm-2-form-switches')
  })

  test('Reset to the usual set ticks the usual caps the agent has and leaves the ones it lacks off', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3' }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-matrix')
    // The agent holds two of the permissions; the others are shown off and cannot be switched on.
    await expect(box.getByTestId('connector-cap-switch-triggers.propose')).toBeDisabled()
    await expect(box.getByTestId('connector-cap-switch-triggers.propose')).toHaveAccessibleDescription('The agent does not have this.')
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toBeEnabled()
    await box.getByTestId('connector-usual').click()
    await expect(box.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-files.upload')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-proposals.confirm')).toHaveAttribute('aria-checked', 'true')
    await expect(box.getByTestId('connector-cap-switch-triggers.propose')).toHaveAttribute('aria-checked', 'false')
    await expect(box.getByTestId('connector-cap-switch-talkback.new_channel')).toHaveAttribute('aria-checked', 'false')
    expect(a.s.calls.some(c => c.method === 'PUT')).toBe(false)
    await shot(page, 'perm-4-usual-set')
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.caps.sort()).toEqual(['files.upload', 'memory.write', 'proposals.confirm'])
  })

  test('a link with the id of a binding opens it at its permissions', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a3' }]).install()
    await page.goto(`${PAGE}?binding=b1`)
    await expect(page.getByTestId('connector-matrix')).toBeVisible()
    await expect(page.getByRole('form', { name: /Edit/ })).toBeVisible()
  })

  test('the page says the rule: the allowlist can use the ticked permissions, sensitive ones also need a pairing', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['triggers.propose', 'memory.write'] }]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-matrix').getByTestId('connector-rule')).toHaveText('Listed people can use the ticked permissions. Sensitive ones also need a verified person.')
  })

  test('the Verified person column says when nobody is verified, and stops saying it once a pairing is live', async ({ page }) => {
    const sens = { ...SET, caps: ['talkback.new_channel'], grants: [] }
    await api(page, [{ ...sens, paired: [] }]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    await expect(mx.getByTestId('connector-matrix-nobody-verified')).toHaveText('Nobody is verified yet.')
    await expect(page.getByTestId('connector-stuck')).toHaveCount(0)
    await shot(page, 'perm-7-sensitive-nobody-paired')
    await mx.getByTestId('connector-matrix-cell-talkback.new_channel-listed').click()
    await mx.getByTestId('connector-matrix-detail').getByRole('button', { name: 'Verify a person' }).click()
    await expect(page.getByTestId('connector-pair-start')).toBeFocused()
  })

  test('the note about nobody verified stays off with a live pairing and comes back when the pairing ended', async ({ page }) => {
    const sens = { ...SET, caps: ['talkback.new_channel'], grants: [] }
    await api(page, [sens]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-matrix')).toBeVisible()
    await expect(page.getByTestId('connector-matrix-nobody-verified')).toHaveCount(0)
    await page.getByRole('button', { name: 'Cancel' }).click()
    await api(page, [{ ...sens, paired: [{ sender_id: 'U001', paired_at: '2026-01-01T10:00:00Z', expires_at: '2026-02-01T10:00:00Z' }] }]).install()
    await page.reload()
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-matrix-nobody-verified')).toBeVisible()
  })

  test('an allowlist with only channels is named in the status line, not in a notice of its own', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a1', allow: ['C0123'], caps: ['memory.write'] }]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-channel-only')).toHaveCount(0)
    await expect(page.getByTestId('connector-ready-who')).toContainText('Only channels are on the list')
    await shot(page, 'perm-8-channel-only')
    await page.getByRole('textbox', { name: 'Add a person or channel' }).fill('U001')
    await page.getByRole('textbox', { name: 'Add a person or channel' }).press('Enter')
    await expect(page.getByTestId('connector-ready-who')).toHaveAttribute('data-state', 'after_save')
  })

  test('role columns hold checkboxes for non-sensitive permissions that are switched on, and the request carries the grants', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, caps: ['memory.write', 'talkback.channels', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    await expect(mx.locator('th[data-col^="role:"]')).toHaveText(['Role: Workspace admin', 'Role: Channel member'])
    // A sensitive permission has no box; a permission that is off has a box that cannot be used.
    await expect(mx.getByRole('checkbox', { name: 'Workspace admin: Post to a new channel' })).toHaveCount(0)
    await expect(mx.getByRole('checkbox', { name: 'Workspace admin: Receive files' })).toBeDisabled()
    await mx.getByRole('checkbox', { name: 'Workspace admin: Save to memory' }).check()
    await mx.getByRole('checkbox', { name: 'Channel member: Post to other channels' }).check()
    await expect(mx.getByTestId('connector-matrix-cell-memory.write-role:workspace_admin')).toHaveAttribute('data-state', 'yes')
    // Turning a permission off takes it from the roles too.
    await page.getByTestId('connector-cap-switch-talkback.channels').click()
    await expect(mx.getByRole('checkbox', { name: 'Channel member: Post to other channels' })).toBeDisabled()
    await expect(mx.getByRole('checkbox', { name: 'Channel member: Post to other channels' })).not.toBeChecked()
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    const body = a.s.calls.find(c => c.method === 'PUT').body
    expect(body.caps.sort()).toEqual(['memory.write', 'talkback.new_channel'])
    expect(body.grants).toEqual([{ role: 'workspace_admin', caps: ['memory.write'] }])
  })

  for (const width of [1280, 375]) {
    test(`the "who may ask" select of a place shows its whole option at ${width} wide`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await api(page, [SET]).install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      const box = page.getByTestId('connector-channels')
      await box.getByTestId('connector-channel-add').click()
      const select = box.getByTestId('connector-channel-approved').nth(1)
      await expect(select).toHaveValue('verified')
      const m = await clippedOptions(select)
      expect(m.scrollWidth).toBeLessThanOrEqual(m.clientWidth)
      expect(m.out, `an option is cut when selected`).toEqual([])
      if (SHOTS) await box.screenshot({ path: `${SHOTS}/places-${width}.png` })
    })
  }

  test('edits the channels it may post to, at most ten, and refuses a bad id', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-channels')
    await expect(box.getByTestId('connector-channel-id')).toHaveValue('C0123')
    await expect(box.getByTestId('connector-channel-label')).toHaveValue('ops')
    await expect(box.getByTestId('connector-channel-approved')).toHaveValue('any')
    await box.getByTestId('connector-channel-add').click()
    await expect(box.getByTestId('connector-channel-approved').nth(1)).toHaveValue('verified')
    await expect(page.getByTestId('connector-channel-problem')).toContainText('needs an ID')
    await expect(page.getByTestId('connector-save')).toBeDisabled()
    await box.getByTestId('connector-channel-id').nth(1).fill('bad id')
    await expect(page.getByTestId('connector-save')).toBeDisabled()
    await box.getByTestId('connector-channel-id').nth(1).fill('C0999')
    await box.getByTestId('connector-channel-label').nth(1).fill('random')
    for (let i = 2; i < 10; i++) {
      await box.getByTestId('connector-channel-add').click()
      await box.getByTestId('connector-channel-id').nth(i).fill(`C${i}`)
    }
    await expect(box.getByTestId('connector-channel')).toHaveCount(10)
    await expect(box.getByTestId('connector-channel-add')).toBeDisabled()
    await shot(page, 'perm-3-channels')
    await box.getByRole('button', { name: 'Remove place C9' }).click()
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    const sent = a.s.calls.find(c => c.method === 'PUT').body.channels
    expect(sent).toHaveLength(9)
    expect(sent[0]).toEqual({ id: 'C0123', label: 'ops', approved: true })
    expect(sent[1]).toEqual({ id: 'C0999', label: 'random', approved: false })
  })

  test('channels are off until talk-back is switched on', async ({ page }) => {
    await api(page, [LEGACY]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-channels')).toContainText('Switch on "Post to other channels"')
    await expect(page.getByTestId('connector-channel-add')).toHaveCount(0)
    await page.getByTestId('connector-channels-switch').click()
    await expect(page.getByTestId('connector-cap-switch-talkback.channels')).toBeFocused()
  })

  test('each place has one choice: any allowed person, or only a verified person', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-channels')
    const choice = box.getByTestId('connector-channel-approved')
    await expect(choice.locator('option')).toHaveText(['When any allowed person asks', 'Only when a verified person asks'])
    await choice.selectOption('verified')
    await expect(box.getByTestId('connector-channel')).toHaveAttribute('data-approved', 'false')
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.channels).toEqual([{ id: 'C0123', label: 'ops', approved: false }])
  })

  // Listening and posting are two settings; the page puts them side by side.
  // C0789 is heard and is no place, C0123 is a place for a verified person
  // only, C0456 is a place for any allowed person, and C0999 is a place the bot
  // does not listen in.
  const PAIRED = {
    ...SET,
    allow: ['U001', 'C0123', 'C0456', 'C0789'],
    channels: [
      { id: 'C0123', label: 'ops', approved: false },
      { id: 'C0456', label: 'alerts', approved: true },
      { id: 'C0999', label: 'reports', approved: true },
    ],
  }

  test('the listening list says whether the bot may post in each channel, and a button makes a channel a place the safe way', async ({ page }) => {
    const a = api(page, [PAIRED])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const list = page.getByTestId('connector-who-channels')
    const status = (id) => list.locator(`[data-testid="connector-trust-row"][data-channel="${id}"]`).getByTestId('connector-post-status')
    await expect(status('C0123')).toHaveAttribute('data-post', 'verified')
    await expect(status('C0123')).toContainText('only when a verified person asks')
    await expect(status('C0456')).toHaveAttribute('data-post', 'any')
    await expect(status('C0789')).toHaveAttribute('data-post', 'none')
    await expect(status('C0789')).toContainText('cannot post here')
    await status('C0789').getByTestId('connector-post-add').click()
    await expect(status('C0789')).toHaveAttribute('data-post', 'verified')
    const row = page.getByTestId('connector-channels').locator('[data-testid="connector-channel"][data-id="C0789"]')
    await expect(row.getByTestId('connector-channel-approved')).toHaveValue('verified')
    await status('C0123').getByTestId('connector-post-show').click()
    await expect(page.getByTestId('connector-channels').locator('[data-id="C0123"]').getByTestId('connector-channel-approved')).toBeFocused()
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.channels.map(c => [c.id, c.approved])).toEqual(
      [['C0123', false], ['C0456', true], ['C0999', true], ['C0789', false]])
  })

  test('the picker offers the listening channels that are no place yet, and Other types an ID', async ({ page }) => {
    await api(page, [PAIRED]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-channels')
    const pick = box.getByTestId('connector-channel-pick')
    await expect(pick.locator('option')).toHaveText(['Add a place', 'C0789', 'Other: type an ID'])
    await pick.selectOption('C0789')
    await expect(box.getByTestId('connector-channel')).toHaveCount(4)
    await expect(box.locator('[data-id="C0789"]').getByTestId('connector-channel-approved')).toHaveValue('verified')
    await expect(pick).toHaveCount(0)
    await expect(box.getByTestId('connector-channel-add')).toBeVisible()
    await box.getByTestId('connector-channel-add').click()
    await expect(box.getByTestId('connector-channel-id').nth(4)).toHaveValue('')
  })

  test('each place says whether scheduled runs can post there, and a place the bot does not hear says so', async ({ page }) => {
    await api(page, [PAIRED]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const hint = (id) => page.getByTestId('connector-channels').locator(`[data-id="${id}"]`).getByTestId('connector-channel-hint')
    await expect(hint('C0123')).toContainText('Scheduled runs cannot post here. Choose "When any allowed person asks" to let them.')
    await expect(hint('C0456')).toHaveText('Scheduled runs can post here.')
    const row = (id) => page.getByTestId('connector-channels').locator(`[data-id="${id}"]`)
    await expect(row('C0999').getByTestId('connector-channel-nolisten')).toContainText('does not listen in this channel')
    await expect(row('C0456').getByTestId('connector-channel-nolisten')).toHaveCount(0)
    await row('C0123').getByTestId('connector-channel-approved').selectOption('any')
    await expect(hint('C0123')).toHaveText('Scheduled runs can post here.')
  })

  for (const width of [1280, 390]) {
    for (const theme of ['light', 'dark']) {
      test(`shots: listening and places side by side at ${width} wide, ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width, height: 1000 })
        await api(page, [PAIRED]).install()
        await page.goto(PAGE)
        await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
        await openEdit(page)
        await expandAll(page)
        await expect(page.getByTestId('connector-channels')).toBeVisible()
        await shotOf(page.getByTestId('connector-who-channels'), `listening-${width}-${theme}`)
        await shotOf(page.getByTestId('connector-channels'), `places-${width}-${theme}`)
      })
    }
  }

  test('reminders go to a person or channel the page knows, or to a saved ID it does not', async ({ page }) => {
    const a = connectorApi(page, [{ ...SET, allow: ['U001', 'C0123'], notify_chat: 'U9999' }], 'slack', [{ id: 'U001', name: 'Ada Example', count: 2, allowed: true }], undefined, { kindExtra: ROLES_KIND, access: ACCESS })
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const pick = page.getByTestId('connector-notify')
    await expect(pick).toHaveValue('U9999')
    await expect(pick.locator('option')).toContainText(['Nowhere', 'Ada Example · U001', 'C0123', 'U9999 (saved ID)', 'Another ID...'])
    await pick.selectOption('U001')
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.notify_chat).toBe('U001')
    // An ID that the page does not know can still be typed.
    await openEdit(page)
    await expandAll(page)
    await page.getByTestId('connector-notify').selectOption({ label: 'Another ID...' })
    await page.getByTestId('connector-notify-other').fill('U4242')
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.filter(c => c.method === 'PUT').at(-1)?.body.notify_chat).toBe('U4242')
  })

  test('the file sizes and the reply language sit in one group that opens when one is set', async ({ page }) => {
    await api(page, [SET]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const more = page.getByTestId('connector-answer-more')
    await expect(more).not.toHaveAttribute('open', '')
    await expect(page.getByTestId('connector-reply-language')).toBeHidden()
    await more.locator('summary').click()
    await expect(page.getByTestId('connector-reply-language')).toBeVisible()
    await expect(page.getByTestId('connector-filelimits')).toBeVisible()
    await expect(page.getByTestId('connector-reply')).toBeVisible()
    await expect(page.getByTestId('connector-conversation')).toBeVisible()
  })

  test('Verify shows the code once, marks a verified person with a badge and removes one', async ({ page }) => {
    const a = api(page, [{ ...SET, allow: ['U001', 'U002'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-pairing')
    const u1 = box.locator('[data-id="U001"]')
    await expect(u1.getByTestId('connector-person-badge')).toHaveText('Verified')
    await expect(u1).toContainText('ends')
    // Only a listed person who is not verified has a Verify button; it starts the pairing, like the one under the table.
    await expect(u1.getByTestId('connector-verify')).toHaveCount(0)
    await box.locator('[data-id="U002"]').getByTestId('connector-verify').click()
    await expect(box.getByTestId('connector-pair-code')).toHaveText('LA-K7M2QX9P')
    await expect(box.getByTestId('connector-pair-code-box')).toContainText('shown only now')
    expect(a.s.calls.filter(c => c.url.endsWith('/pairing')).length).toBe(1)
    await shot(page, 'perm-4-pairing')
    await box.getByRole('button', { name: 'Hide the code' }).click()
    await expect(page.getByText('LA-K7M2QX9P')).toHaveCount(0)
    await box.getByTestId('connector-pair-start').click()
    await expect(box.getByTestId('connector-pair-code')).toHaveText('LA-K7M2QX9P')
    // Remove takes the person off the list and ends the verification.
    await u1.getByRole('button', { name: 'Remove U001' }).click()
    await expect(u1).toHaveCount(0)
    expect(a.s.calls.some(c => c.method === 'DELETE' && c.url === '/api/chats/connectors/b1/paired/U001')).toBe(true)
    // The page never puts the code in a request body of a save.
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'PUT')).toBe(true)
    const put = a.s.calls.find(c => c.method === 'PUT').body
    expect(put.allow).toEqual(['U002'])
    expect(JSON.stringify(put)).not.toContain('LA-K7M2QX9P')
  })

  test('a new binding explains that pairing needs a saved one', async ({ page }) => {
    await api(page, []).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await expandAll(page)
    await expect(page.getByTestId('connector-pairing')).toContainText('Save this bot first')
    await expect(page.getByTestId('connector-access')).toContainText('Save this bot first')
    // The usual set is ticked from the start; a sensitive row is not.
    await expect(page.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('connector-cap-switch-talkback.new_channel')).toHaveAttribute('aria-checked', 'false')
    // Clearing every row leaves every switch off.
    const on = page.locator('[data-testid^="connector-cap-switch-"][aria-checked="true"]')
    while (await on.count()) await on.first().click()
    await expect(on).toHaveCount(0)
  })

  test('recent refusals list only what was refused, newest first, read only, and say the log resets on restart', async ({ page }) => {
    const a = api(page, [SET])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const box = page.getByTestId('connector-access')
    const audit = box.getByTestId('connector-audit')
    await expect(audit.getByRole('listitem')).toHaveCount(1)
    await expect(audit).toContainText('refused')
    await expect(audit.getByTestId('connector-audit-who')).toHaveText('U002')
    await expect(audit.getByTestId('connector-audit-action')).toHaveText('asked for: Receive files')
    await expect(audit.getByTestId('connector-audit-reason')).toHaveText('not enabled for this bot')
    await expect(audit).not.toContainText('allowed')
    await expect(box.getByTestId('connector-refusals-note')).toContainText('kept in memory only: it is empty after the server restarts')
    await expect(box.getByRole('checkbox')).toHaveCount(0)
    await shot(page, 'perm-5-access')
  })

  test('the latest decisions also list the add-on tool calls, with the outcome, the reason and no content', async ({ page }) => {
    await api(page, [SET], { access: { ...ACCESS, tool_outcomes: TOOL_OUTCOMES } }).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
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

  test('a write that waits in the chat shows as waiting, then confirmed, cancelled or expired, with who asked and who answered', async ({ page }) => {
    const rows = [
      { at: '2026-11-05T09:45:00Z', senders: [], addon: 'github', tool: 'Open an issue (acme/widgets)', outcome: 'expired', code: 'hold_expired', message: 'Nobody confirmed the change in 24 hours, so it was dropped.', asker: 'U001' },
      { at: '2026-11-05T09:44:00Z', senders: ['P001'], addon: 'github', tool: 'Comment on an issue (acme/widgets)', outcome: 'cancelled', code: 'chat_cancelled', message: 'A person cancelled the change in the chat.', asker: 'U001' },
      { at: '2026-11-05T09:43:00Z', senders: ['P001'], addon: 'github', tool: 'Open an issue (acme/widgets)', outcome: 'confirmed', code: 'chat_confirmed', message: 'A verified person confirmed the change in the chat and it was made.', asker: 'U001' },
      { at: '2026-11-05T09:42:00Z', senders: ['U001'], addon: 'github', tool: 'create-issue', outcome: 'held', code: 'waiting_in_chat', message: 'The change is waiting for a verified person to confirm it in the chat.' },
    ]
    await api(page, [SET], { access: { ...ACCESS, tool_outcomes: rows } }).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const list = page.getByTestId('connector-access').getByTestId('connector-tool-outcomes')
    await expect(list.getByRole('listitem')).toHaveCount(4)
    await expect(list.getByRole('listitem').nth(0)).toHaveAttribute('data-outcome', 'expired')
    await expect(list.getByRole('listitem').nth(0)).toContainText('expired')
    await expect(list.getByRole('listitem').nth(1)).toContainText('cancelled')
    const done = list.getByRole('listitem').nth(2)
    await expect(done).toHaveAttribute('data-outcome', 'confirmed')
    await expect(done).toContainText('confirmed')
    await expect(done).toContainText('sender P001')
    await expect(done).toContainText('asked by U001')
    await expect(list.getByRole('listitem').nth(3)).toContainText('waiting for a verified person')
    await shot(page, 'hold-1-decisions')
  })

  test('the tools of an add-on are sub-rows of the matrix, and a connection that asks first makes the verified cell wait for a person', async ({ page }) => {
    const hold = { addon: 'tracker', capability: 'tools.write', tools: ['create-issue'], connections: [{ name: 'Work tracker', write_mode: 'propose' }], without_tick: ['paired'] }
    const withHold = (view) => ({ ...view, addon_tools: [hold] })
    const a = api(page, [{ ...SET, caps: [...(SET.caps || []), 'proposals.confirm', 'tools.write'] }], { access: withHold(ACCESS), preview: (body) => withHold(previewFor(body, ROLES_KIND.roles)) })
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    await expect(mx.getByTestId('connector-matrix-row-tools.write:tracker:create-issue')).toContainText('tracker: create-issue')
    await expect(mx.getByTestId('connector-matrix-cell-tools.write:tracker:create-issue-paired')).toHaveAttribute('data-state', 'waiting')
    await shot(page, 'hold-2-readiness')
  })

  test('the add-on tool calls say there are none yet', async ({ page }) => {
    await api(page, [SET]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-access')).toContainText('No tool call yet.')
  })

  test('a cell of the matrix opens the host reason and one fix, and the fix of a switch scrolls to it', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3', caps: ['memory.write', 'triggers.propose', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    const detail = mx.getByTestId('connector-matrix-detail')
    await mx.getByTestId('connector-matrix-cell-memory.write-listed').click()
    await expect(detail.getByTestId('connector-can-memory.write')).toHaveAttribute('data-allowed', 'true')
    // The agent lacks the permission: the first blocker is the agent, and the fix opens the agent in a new tab.
    await mx.getByTestId('connector-matrix-cell-triggers.propose-listed').click()
    const trig = detail.getByTestId('connector-can-triggers.propose')
    await expect(trig).toHaveAttribute('data-allowed', 'false')
    await expect(trig.getByTestId('connector-matrix-message')).toContainText('is not allowed to set reminders and scheduled tasks')
    await expect(trig.getByTestId('connector-fix')).toHaveAttribute('href', '/app/agents/a3/edit')
    await expect(trig.getByTestId('connector-fix')).toHaveAttribute('target', '_blank')
    await expect(trig.getByTestId('connector-fix')).toHaveCount(1)
    await expect(trig).toContainText('Not checked yet: the permissions of this connection.')
    await expect(trig).not.toContainText('who is sending')
    // Not ticked: the fix scrolls to the switch and focuses it.
    await mx.getByTestId('connector-matrix-cell-files.upload-listed').click()
    const files = detail.getByTestId('connector-can-files.upload')
    await expect(files.getByTestId('connector-matrix-message')).toContainText('does not allow')
    await expect(files).not.toContainText('Not checked yet')
    await files.getByRole('button', { name: 'Show the setting' }).click()
    await expect(page.getByTestId('connector-cap-switch-files.upload')).toBeFocused()
    await shot(page, 'can-1-listed')
    // A verified person: the same form, other trust.
    await mx.getByTestId('connector-matrix-cell-memory.write-paired').click()
    await expect(detail.getByTestId('connector-can-memory.write')).toContainText('Because this person is verified')
    // A role of the kind.
    await mx.getByTestId('connector-matrix-cell-memory.write-role:workspace_admin').click()
    await expect(detail.getByTestId('connector-can-memory.write')).toHaveAttribute('data-allowed', 'false')
    await expect(detail.getByTestId('connector-can-memory.write').getByRole('button', { name: 'Show the setting' })).toBeVisible()
  })

  test('a fix that needs a pairing jumps to the pairing button, and a person to the allowed people', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', allow: ['C001'], caps: ['memory.write', 'talkback.new_channel'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    const detail = mx.getByTestId('connector-matrix-detail')
    await mx.getByTestId('connector-matrix-cell-talkback.new_channel-listed').click()
    await expect(detail.getByTestId('connector-matrix-message')).toContainText('verified')
    await detail.getByRole('button', { name: 'Verify a person' }).click()
    await expect(page.getByTestId('connector-pair-start')).toBeFocused()
    // A member of the listed channel is not trusted one by one: add the person.
    await mx.getByTestId('connector-matrix-cell-memory.write-channel_member').click()
    await expect(detail.getByTestId('connector-matrix-message')).toContainText('cannot save notes to memory for them')
    await detail.getByRole('button', { name: 'Add a person' }).click()
    await expect(page.getByTestId('connector-allow').getByRole('textbox')).toBeFocused()
  })

  test('the preview is asked for the form as it is now, debounced, with no token, and says so', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', caps: [] }])
    await a.install()
    // The form waits 400 ms after its last change before it asks. A slow machine
    // can take longer than that between two clicks, so the page clock is frozen
    // once the first answer is in: the debounce ends when fastForward says so.
    await page.clock.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect.poll(() => a.s.previews.length).toBe(1)
    await freezeClock(page)
    expect(a.s.previews[0].id).toBe('b1')
    expect(a.s.previews[0].body).toMatchObject({ agent_id: 'a1', accept_any: false, allow: ['U001'], caps: [], grants: [] })
    expect('kind' in a.s.previews[0].body).toBe(false)
    await expect(page.getByTestId('connector-can-dirty')).toHaveCount(0)
    await page.getByLabel('Bot token').fill('xoxb-secret')
    for (const n of ['memory.write', 'files.upload', 'triggers.propose']) await page.getByTestId(`connector-cap-switch-${n}`).click()
    await expect(page.getByTestId('connector-can-dirty')).toContainText('Computed from the form as it is now')
    // Four changes, one question: nothing was sent while the clock stood still.
    expect(a.s.previews.length).toBe(1)
    await page.clock.fastForward(500)
    await expect.poll(() => a.s.previews.at(-1).body.caps.sort()).toEqual(['files.upload', 'memory.write', 'triggers.propose'])
    expect(a.s.previews.length).toBe(2)
    expect(JSON.stringify(a.s.previews)).not.toContain('xoxb-secret')
    await expect(page.getByTestId('connector-matrix-cell-memory.write-listed')).toHaveAttribute('data-state', 'yes')
    // Nothing was saved by looking.
    expect(a.s.calls.some(c => c.method === 'PUT')).toBe(false)
  })

  test('a connection that is not saved yet is previewed under the id new with its kind', async ({ page }) => {
    const a = api(page, [])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await expandAll(page)
    await expect.poll(() => a.s.previews.at(-1)?.id).toBe('new')
    expect(a.s.previews.at(-1).body.kind).toBe('slack')
    await expect(page.getByTestId('connector-can-dirty')).toContainText('Not saved yet')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    // The usual set the agent holds is on from the start; no sensitive row is.
    await expect.poll(() => a.s.previews.at(-1).body).toMatchObject({ agent_id: 'a1', kind: 'slack' })
    const caps = a.s.previews.at(-1).body.caps
    expect(caps).toContain('memory.write')
    expect(caps).not.toContain('talkback.new_channel')
    await page.getByTestId('connector-cap-switch-memory.write').click()
    await expect.poll(() => a.s.previews.at(-1).body.caps).not.toContain('memory.write')
    await page.getByTestId('connector-cap-switch-memory.write').click()
    await expect(page.getByTestId('connector-matrix-cell-memory.write-listed')).toHaveAttribute('data-state', 'yes')
    await shot(page, 'can-2-new')
  })

  test('the panel says it is checking, and shows the error of the host with a way to try again', async ({ page }) => {
    const slow = api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['memory.write'] }], { previewDelay: 1200 })
    await slow.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-can-status')).toHaveText('Checking what this bot can do')
    await expect(page.getByTestId('connector-matrix')).toHaveAttribute('aria-busy', 'true')
    await shot(page, 'can-3-loading')
    await expect(page.getByTestId('connector-matrix-cell-memory.write-listed')).toBeVisible()
    await expect(page.getByTestId('connector-can-status')).toHaveText('')
  })

  test('a failed preview shows a notice with the host reason, keeps the page usable and retries', async ({ page }) => {
    const a = connectorApi(page, [{ ...LEGACY, agent_id: 'a1' }], 'slack', [], undefined, { kindExtra: ROLES_KIND, access: ACCESS, previewStatus: 503 })
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const err = page.getByTestId('connector-can-error')
    await expect(err).toContainText('Could not check what this bot can do')
    await expect(err).toContainText('The check is not available right now.')
    await shot(page, 'can-4-error')
    const before = a.s.previews.length
    await page.getByTestId('connector-can-retry').click()
    await expect.poll(() => a.s.previews.length).toBeGreaterThan(before)
    await expect(page.getByTestId('connector-save')).toBeEnabled()
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
    await rowMenu(page, 'Modifica')
    await expandAll(page)
    await expect(page.getByTestId('connector-matrix-row-memory.write')).toContainText('Salva in memoria')
    await page.getByTestId('connector-matrix-cell-memory.write-listed').click()
    await expect(page.getByTestId('connector-can-memory.write').getByTestId('connector-matrix-message')).toHaveText('Questa connessione non permette al bot di salvare note in memoria.')
    // A capability the host did not translate keeps its English title.
    await expect(page.getByTestId('connector-matrix-row-files.upload')).toContainText('Receive files')
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
      await openEdit(p)
      await expandAll(p)
      await p.getByTestId('connector-matrix-cell-triggers.propose-listed').click()
      const row = p.getByTestId('connector-matrix-detail')
      await expect(row.getByTestId('connector-matrix-message')).toContainText('turned reminders off')
      if (admin) await expect(row.getByRole('link', { name: 'Open Settings' })).toHaveAttribute('href', '/app/settings')
      else {
        await expect(row.getByTestId('connector-fix-text')).toHaveText('Ask an administrator')
        await expect(row.getByRole('link')).toHaveCount(0)
      }
      await p.close()
    }
  })

  test('the status line lists only what needs attention and opens to the token, the agent and who is let in; the channel-only trap is named', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', allow: ['C001'], caps: ['memory.write'] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
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
    await expect(ready.getByRole('listitem')).toHaveCount(3)
    await expect(ready.getByTestId('connector-ready-token')).toHaveAttribute('data-state', 'ok')
    await expect(ready.getByTestId('connector-ready-triggers')).toHaveCount(0)
    await expect(ready.getByTestId('connector-ready-toggle')).toHaveAttribute('aria-expanded', 'true')
    await shot(page, 'ready-2-all')
  })

  test('the matrix follows the form: a cell that is off because of the agent changes when another agent is chosen, with no save', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a3', caps: ['memory.write', 'triggers.propose'], approval: { mode: 'ask' } }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    // The agent lacks Propose triggers: the cell reads Off and its message links to the agent.
    const cell = mx.getByTestId('connector-matrix-cell-triggers.propose-listed')
    await expect(cell).toHaveAttribute('data-state', 'off')
    await cell.click()
    await expect(mx.getByTestId('connector-matrix-message')).toContainText('is not allowed to set reminders and scheduled tasks')
    await expect(mx.getByTestId('connector-matrix-detail').getByTestId('connector-fix')).toHaveAttribute('href', '/app/agents/a3/edit')
    // Choosing an agent that has it changes the cell without a save.
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await expect(cell).toHaveAttribute('data-state', 'asks')
    expect(a.s.calls.some(c => c.method === 'PUT')).toBe(false)
  })

  test('the status line says when the agent has no model', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a2', caps: [] }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const ready = page.getByTestId('connector-readiness')
    await expect(ready.getByTestId('connector-ready-agent')).toHaveAttribute('data-state', 'broken')
    await expect(ready.getByTestId('connector-ready-agent')).toContainText('has no model')
    await expect(ready.getByTestId('connector-ready-permissions')).toHaveCount(0)
  })

  test('the approval of proposed triggers sits under Propose triggers, shows its choice in the row and is greyed while the permission is off', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, caps: ['memory.write'], approval: { mode: 'auto_readonly' } }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const row = await disclosure(page, 'triggers.propose')
    // One place only: nothing at the bottom of the page.
    await expect(page.getByTestId('connector-approval')).toHaveCount(1)
    await expect(row.getByTestId('connector-approval')).toBeVisible()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: runs at once if read-only')
    // Permission off: greyed, with the reason, and the radios cannot be used.
    await expect(row.getByTestId('connector-approval-off')).toHaveText('Turn on Propose triggers to use this.')
    await expect(row.getByRole('radio').first()).toBeDisabled()
    await expect(row.getByTestId('connector-approval')).toHaveAccessibleDescription('Turn on Propose triggers to use this.')
    await shot(page, 'perm-9-approval-greyed')
    await page.getByTestId('connector-cap-switch-triggers.propose').click()
    await expect(row.getByTestId('connector-approval-off')).toHaveCount(0)
    await expect(row.getByRole('radio', { name: /Asks first/ })).toBeEnabled()
    // The three modes change the line of the row.
    await row.getByRole('radio', { name: /Asks first/ }).check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: asks first')
    await shot(page, 'perm-10-approval-ask')
    await row.getByRole('radio', { name: /^Runs at once: it is set up/ }).check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: runs at once')
    await shot(page, 'perm-11-approval-auto')
    await row.getByRole('radio', { name: /^Runs at once when the runs only read/ }).check()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: runs at once if read-only')
    await shot(page, 'perm-12-approval-readonly')
    // The limits are in a closed disclosure and are sent as before.
    const limits = row.getByTestId('connector-approval-limits')
    await expect(limits).not.toHaveAttribute('open', '')
    await limits.getByText('Limits', { exact: true }).click()
    await limits.getByTestId('connector-approval-max_pending').fill('3')
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.approval).toEqual({ mode: 'auto_readonly', acts: [], max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('Runs may act sits under the approval mode, is off by default, lists the four acts, disables what the agent lacks and is sent with the whole approval', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a4', caps: ['triggers.propose'], approval: { mode: 'auto', acts: [], max_pending: 3 } }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const row = await disclosure(page, 'triggers.propose')
    const acts = row.getByTestId('connector-acts')
    await expect(acts.getByTestId('connector-acts-switch')).toHaveAttribute('aria-checked', 'false')
    await expect(acts.getByRole('checkbox')).toHaveCount(0)
    await expect(acts.getByTestId('connector-acts-modes')).toHaveText('Asks first: you always decide. Runs at once: only when you are a verified person and every act is ticked here.')
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
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: runs at once, runs may: write files, use add-ons')
    await shot(page, 'acts-2-on')
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.approval).toEqual({ mode: 'auto', acts: ['act.files', 'act.addons'], max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('a ticked act the agent no longer holds stays ticked and can be unticked; turning Runs may act off sends an empty list', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a1', caps: ['triggers.propose'], approval: { mode: 'ask', acts: ['act.send', 'act.spend'] } }])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await disclosure(page, 'triggers.propose')
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
    await page.getByTestId('connector-save').click()
    await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.approval).toEqual({ mode: 'ask', acts: [], max_pending: 0, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  })

  test('a saved connection links back to its agent, and a connection with no agent shows no link', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a1' }]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const link = page.getByTestId('connector-agent-link')
    await expect(link).toHaveText('Open the agent Support')
    await expect(link).toHaveAttribute('href', '/app/agents/a1')
    await shot(page, 'agent-link')
    await page.getByRole('button', { name: 'Cancel' }).click()
    await api(page, [{ ...LEGACY, agent_id: '' }]).install()
    await page.reload()
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-form')).toBeVisible()
    await expect(page.getByTestId('connector-agent-link')).toHaveCount(0)
  })

  test('a save says what it changed, and names the triggers that lose an act the connection no longer allows', async ({ page }) => {
    const a = api(page, [{ ...LEGACY, agent_id: 'a4', caps: ['triggers.propose'], approval: { mode: 'auto', acts: ['act.files', 'act.addons'], max_pending: 3 } }])
    await a.install()
    await page.route('**/api/chats/triggers', r => r.fulfill({ json: { triggers: [
      { id: 't1', name: 'Morning digest', binding: 'b1', acts: ['act.addons', 'act.files'] },
      { id: 't2', name: 'Weekly sweep', binding: 'b1', acts: ['act.files'] },
      { id: 't3', name: 'Other connection', binding: 'b2', acts: ['act.addons'] },
      { id: 't4', name: 'Made by the owner', acts: ['act.addons'] },
    ] } }))
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const row = await disclosure(page, 'triggers.propose')
    await row.getByTestId('connector-act-check-act.addons').uncheck()
    await page.getByTestId('connector-save').click()
    await expect(page.getByTestId('connector-saved')).toHaveText('Saved. Changed: trigger approval and acts.')
    const dropped = page.getByTestId('connector-acts-dropped-item')
    await expect(dropped).toHaveCount(1)
    await expect(dropped).toHaveText('Morning digest: use add-ons')
    await shot(page, 'save-notice-dropped')
  })

  test('a save that removes no act leaves the dropped notice out', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a4', caps: ['triggers.propose'], approval: { mode: 'ask', acts: [] } }]).install()
    await page.route('**/api/chats/triggers', r => r.fulfill({ json: { triggers: [{ id: 't1', name: 'Morning digest', binding: 'b1', acts: ['act.addons'] }] } }))
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await page.getByTestId('connector-save').click()
    await expect(page.getByTestId('connector-saved')).toHaveText('Saved. Nothing changed.')
    await expect(page.getByTestId('connector-acts-dropped')).toHaveCount(0)
  })

  test('Runs may act is greyed with the reason while Propose triggers is off', async ({ page }) => {
    await api(page, [{ ...LEGACY, agent_id: 'a4', caps: ['memory.write'], approval: { mode: 'ask', acts: ['act.files'] } }]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const row = await disclosure(page, 'triggers.propose')
    await expect(row.getByTestId('connector-approval-off')).toHaveText('Turn on Propose triggers to use this.')
    await expect(row.getByTestId('connector-acts-switch')).toBeDisabled()
    await expect(row.getByTestId('connector-act-check-act.files')).toBeDisabled()
    await shot(page, 'acts-4-greyed')
    await page.getByTestId('connector-cap-switch-triggers.propose').click()
    await expect(row.getByTestId('connector-act-check-act.files')).toBeEnabled()
    await expect(row.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: asks first, runs may: write files')
    await shot(page, 'acts-5-summary')
  })

  test('write tools of an add-on the agent is connected to: verified people have them without the tick, and the cells say why', async ({ page }) => {
    const hold = { addon: 'tracker', capability: 'tools.write', tools: ['create-issue', 'comment-issue'], connections: [{ name: 'Work tracker', write_mode: 'propose' }], without_tick: ['paired'] }
    const cover = (view) => ({
      ...view,
      addon_tools: [hold],
      subjects: view.subjects.map((x) => ({ ...x, decisions: x.decisions.map((d) => (x.id === 'paired' && d.capability === 'tools.write' ? { capability: 'tools.write', allowed: true, via: 'agent-connection' } : d)) })),
    })
    const a = api(page, [SET], { access: cover(ACCESS), preview: (body) => (body.caps?.includes('tools.write') ? { ...previewFor(body, ROLES_KIND.roles), addon_tools: [{ ...hold, without_tick: [] }] } : cover(previewFor(body, ROLES_KIND.roles))) })
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    const mx = page.getByTestId('connector-matrix')
    const detail = mx.getByTestId('connector-matrix-detail')
    await expect(mx.getByTestId('connector-matrix-row-tools.write:tracker:create-issue')).toBeVisible()
    await expect(mx.getByTestId('connector-matrix-row-tools.write:tracker:comment-issue')).toBeVisible()
    // The verified person has the tools through the connection; the listed person needs the tick.
    await mx.getByTestId('connector-matrix-cell-tools.write-paired').click()
    await expect(detail).toContainText('Because this person is verified and the agent is connected to the add-on')
    await shot(page, 'paired-write-1-paired-sender')
    await mx.getByTestId('connector-matrix-cell-tools.write-listed').click()
    await expect(detail).toContainText('Tick the matching permission on the connection')
    await shot(page, 'paired-write-2-listed-sender')
  })

  test('an older host that sends no capability list shows no permission section', async ({ page }) => {
    await connectorApi(page, [LEGACY], 'slack', [], undefined, { capabilities: null }).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('connector-row')).toContainText('Team app')
    await expect(page.getByTestId('connector-nocaps')).toHaveCount(0)
    await openEdit(page)
    await expandAll(page)
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
    await openEdit(page)
    await expandAll(page)
    // The saved view is the owner's own: the relaxed tool runs, the other asks.
    const saved = page.getByTestId('connector-access-approvals')
    await expect(saved.getByTestId('connector-approval-power-action')).toHaveAttribute('data-state', 'runs')
    await expect(saved.getByTestId('connector-approval-power-action')).toContainText('Runs at once')
    await expect(saved.getByTestId('connector-approval-send-key')).toHaveAttribute('data-state', 'asks')
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
    await openEdit(page)
    await expandAll(page)
    const saved = page.getByTestId('connector-access-notesave')
    await expect(saved.getByTestId('connector-access-notesave-paired')).toContainText('Saves notes at once and tells you, with an Undo')
    await expect(saved.getByTestId('connector-access-notesave-anyone')).toContainText('wait for the owner')
    // The matrix says it per kind of sender: a verified person saves at once, a listed one asks first.
    await expect(page.getByTestId('connector-matrix-cell-memory.write-paired')).toHaveAttribute('data-state', 'yes')
    await expect(page.getByTestId('connector-matrix-cell-memory.write-listed')).toHaveAttribute('data-state', 'asks')
    await shot(page, 'perm-7-notesave')
  })

  test('an agent without the rule shows no note line', async ({ page }) => {
    await api(page, [SET]).install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-access')).toBeVisible()
    await expect(page.getByTestId('connector-access-notesave')).toHaveCount(0)
    await expect(page.getByTestId('connector-can-notesave')).toHaveCount(0)
  })

  test('a run that was allowed is not a refusal, and the add-on tool calls still list it', async ({ page }) => {
    const recent = [{ at: '2026-11-03T09:30:00Z', senders: ['U001'], capability: 'tools.write', allowed: true, via: 'no-confirmation', reason: 'power-action: applied' }]
    const a = api(page, [SET], { access: { ...ACCESS, recent } })
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-refusals-none')).toHaveText('Nothing refused since the server started.')
    await expect(page.getByTestId('connector-audit')).toHaveCount(0)
  })

  test.describe('channels where the bot listens', () => {
    const STORED = { ...SET, allow: ['U001', 'C0123'], caps: ['files.upload', 'memory.write', 'talkback.channels'] }

    test('a channel that is added starts as Let everyone here ask for actions, and the level goes back to Answer only', async ({ page }) => {
      const a = api(page, [{ ...SET, allow: ['U001'], caps: ['files.upload', 'memory.write'] }])
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      await page.getByRole('textbox', { name: 'Add a person or channel' }).fill('C0555')
      await page.getByRole('textbox', { name: 'Add a person or channel' }).press('Enter')
      const row = page.getByTestId('connector-trust-row')
      await expect(row).toHaveCount(1)
      await expect(row).toHaveAttribute('data-level', 'act')
      const level = row.getByTestId('connector-trust-level')
      await expect(level).toHaveValue('act')
      await expect(level.locator('option')).toHaveText(['Answer only', 'Let everyone here ask for actions'])
      await expect(page.getByTestId('connector-who-channels-note')).toHaveText('People in a channel can act only inside that channel, never in a direct message.')
      await shot(page, 'trust-1-new-channel-act')
      await shotOf(page.getByTestId('connector-who'), 'trust-1b-who-section')
      await level.selectOption('answer')
      await expect(row).toHaveAttribute('data-level', 'answer')
      await shot(page, 'trust-2-answer-only')
      await page.getByTestId('connector-save').click()
      await expect.poll(() => a.s.calls.some((c) => c.method === 'PUT')).toBe(true)
      expect(a.s.calls.find((c) => c.method === 'PUT').body.allow_trust).toEqual({ C0555: 'answer' })
    })

    test('a channel stored before the setting reads as Answer only and is not widened by a save', async ({ page }) => {
      const a = api(page, [STORED])
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      await expect(page.getByTestId('connector-trust-level')).toHaveValue('answer')
      await shot(page, 'trust-3-stored-channel')
      await shotOf(page.getByTestId('connector-channels'), 'trust-3c-where-it-may-post')
      await page.getByTestId('connector-save').click()
      await expect.poll(() => a.s.calls.some((c) => c.method === 'PUT')).toBe(true)
      expect(a.s.calls.find((c) => c.method === 'PUT').body.allow_trust).toEqual({})
    })

    test('the talk-back list is named Place the bot may post and says it trusts nobody', async ({ page }) => {
      await api(page, [STORED]).install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      const box = page.getByTestId('connector-channels')
      await expect(box).toContainText('Place the bot may post')
      await expect(box).toContainText('This does not decide who the bot listens to')
      await expect(box).not.toContainText('Approved')
    })

    test('the people and channel rows do not overflow a 320 px screen', async ({ page }) => {
      await page.setViewportSize({ width: 320, height: 800 })
      await api(page, [{ ...STORED, allow: ['U001', 'C0123', 'C0456'], allow_trust: { C0456: 'act' } }]).install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      await expect(page.getByTestId('connector-trust-row')).toHaveCount(2)
      // Only the section of this change is measured: the matrix has its own scroll box.
      const over = await page.evaluate(() => [...document.querySelectorAll('[data-testid="connector-who"], [data-testid="connector-who"] *')]
        .filter((e) => e.getBoundingClientRect().right > document.documentElement.clientWidth + 1).map((e) => e.tagName + '.' + String(e.className)))
      expect(over, 'elements of the who section wider than the screen').toEqual([])
      await shot(page, 'trust-4-320px')
      await shotOf(page.getByTestId('connector-who'), 'trust-4b-320px-who')
    })

    test('a refusal of a sender who came in through a channel offers Allow <name>', async ({ page }) => {
      const refused = { at: '2026-11-03T09:10:00Z', senders: ['U777'], sender_name: 'Stranger Example', capability: 'files.upload', allowed: false, code: 'channel_not_trusted', reason: 'files.upload needs a trusted sender: sender U777 was let in only through a channel set to Answer only; add U777 to the allowed people, or set the channel to Let everyone here ask for actions' }
      const a = api(page, [STORED], { access: { ...ACCESS, recent: [refused] } })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      await shot(page, 'trust-5-refusal')
      await shotOf(page.getByTestId('connector-access'), 'trust-5b-refusal-allow-person')
      await page.getByRole('button', { name: 'Allow Stranger Example' }).click()
      await expect.poll(() => a.s.calls.some((c) => c.method === 'POST' && c.url.endsWith('/allow'))).toBe(true)
      // The person is on the list of the form now, and the button is gone.
      await expect(page.getByTestId('connector-person-row').and(page.locator('[data-id="U777"]'))).toHaveCount(1)
      await expect(page.getByTestId('connector-audit-allow')).toHaveCount(0)
    })

    test('a refusal in a channel set to answer only offers Let <channel> members act, which changes the level of the form', async ({ page }) => {
      const refused = { at: '2026-11-03T09:10:00Z', senders: ['U001'], chat: 'C0123', chat_name: 'ops', capability: 'files.upload', allowed: false, code: 'channel_not_trusted', reason: 'files.upload needs a trusted sender' }
      const a = api(page, [{ ...STORED, allow_trust: {} }], { access: { ...ACCESS, recent: [refused] } })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      await expect(page.getByTestId('connector-audit-allow')).toHaveCount(0)
      await expect(page.getByTestId('connector-audit-chat')).toHaveText('in ops (C0123)')
      await page.getByRole('button', { name: 'Let ops members act' }).click()
      await expect(page.getByTestId('connector-trust-row')).toHaveAttribute('data-level', 'act')
      await expect(page.getByTestId('connector-audit-act')).toHaveCount(0)
      expect(a.s.calls.some((c) => c.method === 'PUT')).toBe(false)
      await page.getByTestId('connector-save').click()
      await expect.poll(() => a.s.calls.find((c) => c.method === 'PUT')?.body.allow_trust).toEqual({ C0123: 'act' })
    })
  })
})
