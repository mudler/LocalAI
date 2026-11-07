import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, ROLES_KIND, openEdit, expandAll, openPane } from './connector-fixtures.js'

// "Who can talk to the bot" on the shared connection page: a two-way choice, a
// table of people, a table of channels with a level, one box that adds either,
// and the recent people who wrote to the bot. The saved fields do not change
// (accept_any, allow, allow_trust, paired). IDs and names are invented.
//
// The last block is names beside IDs: the table of people, the refusals and the
// sender log show the display name and the chat next to the ID, and fall back
// to the ID alone when the host sent none.

const SHOTS = process.env.UI_SHOTS_DIR
const SLACK = '/app/addons/slack/apps'
const TELEGRAM = '/app/addons/telegram/bots'
const DISCORD = '/app/addons/discord/bots'
const USER = '11111111111111111'
const CHANNEL = '22222222222222222'
const base = (kind, over = {}) => ({ id: 'b1', name: 'Team', kind, agent_id: 'a1', enabled: true, accept_any: false, allow: [], has_token: true, caps: ['memory.write'], grants: [], channels: [], paired: [], ...over })
const SENDERS = [
  { id: 'U001', name: 'Ada Example', count: 3, allowed: true, first: '2026-11-04T08:00:00Z', last: '2026-11-04T08:30:00Z', chat: 'C0123', chat_name: 'ops' },
  { id: 'U003', name: 'Cleo Demo', count: 1, allowed: false, first: '2026-11-04T09:00:00Z', last: '2026-11-04T09:05:00Z' },
  { id: 'U004', name: 'Dan Sample', count: 2, allowed: false, first: '2026-11-04T07:00:00Z', last: '2026-11-04T07:05:00Z' },
]

for (const width of [1280, 375]) {
  test.describe(`who can talk to the bot at ${width} px`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockChat(page)
    })

    test('two choices, Only people I choose by default, and Anyone shows the warning and hides the tables', async ({ page }) => {
      await installStudioAddons(page, { ids: ['slack'] })
      await connectorApi(page, [], 'slack', [], undefined, { kindExtra: ROLES_KIND }).install()
      await page.goto(SLACK)
      await page.getByRole('button', { name: 'New Slack app' }).click()
      await expandAll(page)
      const who = page.getByTestId('connector-who')
      await expect(who.getByRole('radio')).toHaveCount(2)
      await expect(who.getByRole('radio', { name: 'Only people I choose' })).toBeChecked()
      await expect(who.getByTestId('connector-allow')).toBeVisible()
      await who.getByRole('radio', { name: 'Anyone who can message it' }).check()
      await expect(who.getByTestId('anyone-warning')).toBeVisible()
      await expect(who.getByTestId('connector-allow')).toHaveCount(0)
      await expect(who.getByTestId('connector-who-channels')).toHaveCount(0)
      await who.getByRole('radio', { name: 'Only people I choose' }).check()
      await expect(who.getByTestId('anyone-warning')).toHaveCount(0)
    })

    test('Slack: one box puts C and G IDs in the channels and U, W and @names in the people', async ({ page }) => {
      await installStudioAddons(page, { ids: ['slack'] })
      const a = connectorApi(page, [base('slack')], 'slack', SENDERS, undefined, { kindExtra: ROLES_KIND })
      await a.install()
      await page.goto(SLACK)
      await openEdit(page)
      await expandAll(page)
      const box = page.getByRole('textbox', { name: 'Add a person or channel' })
      for (const id of ['U9001', 'W9002', '@grace', 'C9003', 'G9004']) { await box.fill(id); await box.press('Enter') }
      const people = page.getByTestId('connector-who-people')
      for (const id of ['U9001', 'W9002', '@grace']) await expect(people).toContainText(id)
      await expect(people).not.toContainText('C9003')
      const channels = page.getByTestId('connector-who-channels')
      await expect(channels.getByTestId('connector-trust-row')).toHaveCount(2)
      await expect(channels).toContainText('C9003')
      await expect(channels).toContainText('G9004')
      await expect(channels.getByTestId('connector-trust-level').first()).toHaveValue('act')
      await channels.getByTestId('connector-trust-level').first().selectOption('answer')
      await channels.getByRole('button', { name: 'Remove G9004' }).click()
      await page.getByTestId('connector-save').click()
      await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.allow).toEqual(['U9001', 'W9002', '@grace', 'C9003'])
      expect(a.s.calls.find(c => c.method === 'PUT').body).toMatchObject({ accept_any: false, allow_trust: { C9003: 'answer' } })
      if (SHOTS) await page.getByTestId('connector-who').screenshot({ path: `${SHOTS}/who-slack-${width}.png` })
    })

    test('Telegram: a negative ID is a group, a positive one a person', async ({ page }) => {
      await installStudioAddons(page, { ids: ['telegram'] })
      await connectorApi(page, [base('telegram')], 'telegram', []).install()
      await page.goto(TELEGRAM)
      await openEdit(page)
      await expandAll(page)
      const box = page.getByRole('textbox', { name: 'Add a person or channel' })
      for (const id of ['42', '-100123']) { await box.fill(id); await box.press('Enter') }
      await expect(page.getByTestId('connector-who-people')).toContainText('42')
      await expect(page.getByTestId('connector-who-people')).not.toContainText('-100123')
      await expect(page.getByTestId('connector-who-channels')).toContainText('-100123')
    })

    test('Discord cannot tell, so one list of people and channels shows the level on every ID', async ({ page }) => {
      await installStudioAddons(page, { ids: ['discord'] })
      await connectorApi(page, [base('discord')], 'discord', []).install()
      await page.goto(DISCORD)
      await openEdit(page)
      await expandAll(page)
      const box = page.getByRole('textbox', { name: 'Add a person or channel' })
      for (const id of [USER, CHANNEL]) { await box.fill(id); await box.press('Enter') }
      const who = page.getByTestId('connector-who')
      await expect(who.getByRole('heading', { name: 'People and channels' })).toBeVisible()
      await expect(who.getByTestId('connector-who-channels')).toHaveCount(0)
      await expect(who.getByTestId('connector-person-row')).toHaveCount(2)
      await expect(who.getByTestId('connector-trust-level')).toHaveCount(2)
      await who.getByTestId('connector-trust-level').nth(1).selectOption('answer')
      if (SHOTS) await who.screenshot({ path: `${SHOTS}/who-discord-${width}.png` })
    })

    test('recent people who wrote to the bot are chips; a click adds one and the chip goes away', async ({ page }) => {
      await installStudioAddons(page, { ids: ['slack'] })
      const a = connectorApi(page, [base('slack', { allow: ['U001'] })], 'slack', SENDERS, undefined, { kindExtra: ROLES_KIND })
      await a.install()
      await page.goto(SLACK)
      await openEdit(page)
      await expandAll(page)
      const chips = page.getByTestId('connector-recent-chip')
      // Newest first, with the name; a person who is on the list is not offered.
      await expect(chips).toHaveText(['Cleo Demo · U003', 'Dan Sample · U004'])
      await chips.first().click()
      await expect(page.getByTestId('connector-who-people')).toContainText('Cleo Demo')
      await expect(chips).toHaveText(['Dan Sample · U004'])
      await page.getByTestId('connector-save').click()
      await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body.allow).toEqual(['U001', 'U003'])
    })

    test('a channel shows its name from the sender log, and a new bot cannot verify before it is saved', async ({ page }) => {
      await installStudioAddons(page, { ids: ['slack'] })
      await connectorApi(page, [base('slack', { allow: ['U001', 'C0123'] })], 'slack', SENDERS, undefined, { kindExtra: ROLES_KIND }).install()
      await page.goto(SLACK)
      await openEdit(page)
      await expandAll(page)
      await expect(page.getByTestId('connector-who-channels')).toContainText('ops')
      await expect(page.getByTestId('connector-who-channels')).toContainText('C0123')
      await page.getByRole('button', { name: 'Cancel' }).click()
      await page.getByRole('button', { name: 'New Slack app' }).click()
      await expandAll(page)
      await expect(page.getByTestId('connector-pair-start')).toBeDisabled()
      await expect(page.getByTestId('connector-pairing')).toContainText('Save this bot first')
    })

    test('verifying uses the pairing of the host and the verified person shows with a badge once it is done', async ({ page }) => {
      await installStudioAddons(page, { ids: ['slack'] })
      const a = connectorApi(page, [base('slack', { allow: ['U001'] })], 'slack', SENDERS, undefined, { kindExtra: ROLES_KIND })
      await a.install()
      await page.goto(SLACK)
      await openEdit(page)
      await expandAll(page)
      const row = page.getByTestId('connector-person-row')
      await expect(row.getByTestId('connector-person-badge')).toHaveText('Listed')
      await row.getByRole('button', { name: 'Verify Ada Example' }).click()
      await expect(page.getByTestId('connector-pair-code')).toHaveText('LA-K7M2QX9P')
      expect(a.s.calls.some(c => c.url === '/api/chats/connectors/b1/pairing')).toBe(true)
    })
  })
}

// Names beside IDs, drawn by the Slack add-on.
const NAMES_PAGE = '/app/addons/slack/apps'
const NAMES_BINDING = {
  id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001', 'U002', 'U009'], has_token: true,
  caps: ['memory.write'], grants: [], channels: [],
  paired: [{ sender_id: 'U001', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }, { sender_id: 'U009', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }],
}
const NAMES_SENDERS = [
  { id: 'U001', name: 'Ada Example', count: 4, allowed: true, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:30:00Z', chat: 'D0AAA', chat_name: '' },
  { id: 'U002', name: 'Grace Sample', count: 1, allowed: true, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:10:00Z', chat: 'C0123', chat_name: 'ops' },
  { id: 'U777', name: 'Stranger Example', count: 2, allowed: false, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:20:00Z', chat: 'C0123', chat_name: 'ops', reason: 'not_listed' },
  { id: 'U888', name: 'Quiet Example', count: 1, allowed: false, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:21:00Z', chat: 'C0456', reason: 'not_addressed' },
]
const NAMES_ACCESS = {
  binding: 'b1',
  subjects: [{ id: 'listed', title: 'A listed person', decisions: [{ capability: 'memory.write', allowed: true }] }],
  recent: [
    { at: '2026-11-03T09:30:00Z', senders: ['U001'], sender_name: 'Ada Example', chat: 'C0123', chat_name: 'ops', capability: 'memory.write', allowed: true, via: 'paired' },
    { at: '2026-11-03T09:10:00Z', senders: ['U555'], capability: 'files.upload', allowed: false, reason: 'not enabled for this bot' },
  ],
  tool_outcomes: [{ at: '2026-11-05T09:41:00Z', senders: ['U002'], addon: 'github', tool: 'create-issue', outcome: 'ok' }],
}

for (const width of [1280, 375]) {
  test.describe(`names beside IDs at ${width} px`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockChat(page)
      await installStudioAddons(page, { ids: ['slack'] })
      await connectorApi(page, [NAMES_BINDING], 'slack', NAMES_SENDERS, undefined, { kindExtra: ROLES_KIND, access: NAMES_ACCESS }).install()
    })

    test('the table of people and the refusals show the name next to the ID, and the ID alone when no name is known', async ({ page }) => {
      await page.goto(NAMES_PAGE)
      await openEdit(page)
      await expandAll(page)
      // The table of people: the name big and the ID small, and the ID alone when no name is known.
      const row = (id) => page.getByTestId('connector-person-row').and(page.locator(`[data-id="${id}"]`))
      await expect(row('U001')).toContainText('Ada Example')
      await expect(row('U001')).toContainText('U001')
      await expect(row('U002')).toContainText('Grace Sample')
      await expect(row('U009')).toContainText('U009')
      await expect(row('U009').locator('.cn-who__name')).toHaveText('U009')
      await expect(row('U009').locator('.cn-who__id')).toHaveCount(0)
      // The badge says Verified for a verified person and Listed for the other, and the buttons name the person.
      await expect(row('U001').getByTestId('connector-person-badge')).toHaveText('Verified')
      await expect(row('U002').getByTestId('connector-person-badge')).toHaveText('Listed')
      await expect(row('U001').getByRole('button', { name: 'Remove Ada Example' })).toBeVisible()
      await expect(row('U009').getByRole('button', { name: 'Remove U009' })).toBeVisible()
      await expect(row('U002').getByRole('button', { name: 'Verify Grace Sample' })).toBeVisible()
      // Someone who wrote to the bot and is not listed is offered under the add box, by name.
      await expect(page.getByTestId('connector-recent-chip')).toHaveText(['Quiet Example · U888', 'Stranger Example · U777'])
      // The refusals: the name the event carried, the chat with its name, the ID alone for an unknown person.
      const audit = page.getByTestId('connector-audit')
      await expect(audit.getByRole('listitem').filter({ hasText: 'U555' })).toContainText('asked for: Receive files')
      await expect(audit.getByRole('listitem').filter({ hasText: 'U555' })).not.toContainText(' · U555')
      await expect(audit.getByRole('listitem').filter({ hasText: 'Stranger Example' })).toContainText('in ops (C0123)')
      await expect(audit.getByRole('listitem').filter({ hasText: 'Stranger Example' })).toContainText('not a listed person')
      await expect(audit.getByRole('button', { name: 'Allow Stranger Example' })).toBeVisible()
      // The tool calls take the name from the sender log.
      await expect(page.getByTestId('connector-tool-outcomes')).toContainText('Grace Sample · U002')
      // The log says it is kept in memory.
      await expect(page.getByTestId('connector-access')).toContainText('kept in memory only')
      if (process.env.UI_SHOTS_DIR) await page.getByTestId('connector-access').screenshot({ path: `${SHOTS}/names-decisions-${width}.png` })
      if (process.env.UI_SHOTS_DIR) await page.getByTestId('connector-pairing').screenshot({ path: `${SHOTS}/names-pairing-${width}.png` })
    })

    test('the sender log shows the chat, and a reason for each ignored message', async ({ page }) => {
      await page.goto(NAMES_PAGE)
      await openPane(page)
      const log = page.getByTestId('sender-log')
      await expect(log.getByRole('listitem')).toHaveCount(4)
      await expect(log.getByRole('listitem').nth(0)).toContainText('Ada Example')
      await expect(log.getByRole('listitem').nth(0)).toContainText('in D0AAA')
      await expect(log.getByRole('listitem').nth(1)).toContainText('in ops (C0123)')
      await expect(log.getByRole('listitem').nth(2).getByTestId('sender-reason')).toHaveAttribute('data-code', 'not_listed')
      await expect(log.getByRole('listitem').nth(2)).toContainText('not a listed person')
      await expect(log.getByRole('listitem').nth(3)).toContainText('the message did not name the bot')
      await expect(log.getByTestId('sender-reason')).toHaveCount(2)
      if (process.env.UI_SHOTS_DIR) await log.screenshot({ path: `${SHOTS}/names-senders-${width}.png` })
    })
  })
}
