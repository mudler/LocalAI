import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, ROLES_KIND } from './connector-fixtures.js'

// Names beside IDs on the shared connection page (drawn here by the Slack
// add-on): the people list, the pairing list, the latest decisions and the
// sender log show the display name and the chat next to the ID, and fall back
// to the ID alone when the host sent none. The IDs and names are invented.

const PAGE = '/app/addons/slack/apps'
const SHOTS = process.env.UI_SHOTS_DIR
const BINDING = {
  id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001', 'U002', 'U009'], has_token: true,
  caps: ['memory.write'], grants: [], channels: [],
  paired: [{ sender_id: 'U001', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }, { sender_id: 'U009', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }],
}
const SENDERS = [
  { id: 'U001', name: 'Ada Example', count: 4, allowed: true, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:30:00Z', chat: 'D0AAA', chat_name: '' },
  { id: 'U002', name: 'Grace Sample', count: 1, allowed: true, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:10:00Z', chat: 'C0123', chat_name: 'ops' },
  { id: 'U777', name: 'Stranger Example', count: 2, allowed: false, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:20:00Z', chat: 'C0123', chat_name: 'ops', reason: 'not_listed' },
  { id: 'U888', name: 'Quiet Example', count: 1, allowed: false, first: '2026-11-03T08:00:00Z', last: '2026-11-03T09:21:00Z', chat: 'C0456', reason: 'not_addressed' },
]
const ACCESS = {
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
      await connectorApi(page, [BINDING], 'slack', SENDERS, undefined, { kindExtra: ROLES_KIND, access: ACCESS }).install()
    })

    test('the people list, the pairing list and the decisions show the name next to the ID, and the ID alone when no name is known', async ({ page }) => {
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Edit' }).click()
      const chips = page.getByTestId('connector-allow').or(page.locator('[data-testid^="connector-allow"]')).first()
      await expect(page.getByRole('list', { name: 'Listed people' })).toContainText('Ada Example · U001')
      await expect(page.getByRole('list', { name: 'Listed people' })).toContainText('Grace Sample · U002')
      await expect(page.getByRole('list', { name: 'Listed people' })).toContainText('U009')
      await expect(page.getByRole('list', { name: 'Listed people' })).not.toContainText('· U009')
      // A chip keeps its accessible name, the ID, so the button to remove it does not change.
      await expect(page.getByRole('button', { name: 'U001 ×' })).toBeVisible()
      await expect(chips).toBeVisible()
      // The pairing list: the name for a person of the sender log, the ID for the other.
      const paired = page.getByTestId('connector-paired')
      await expect(paired).toContainText('Ada Example · U001')
      await expect(paired.getByRole('listitem').nth(1)).toContainText('U009')
      await expect(paired.getByRole('listitem').nth(1)).not.toContainText('· ')
      // The decisions: the name the event carried, the chat with its name, and the ID alone for an unknown person.
      const audit = page.getByTestId('connector-audit')
      await expect(audit.getByRole('listitem').nth(0)).toContainText('Ada Example · U001')
      await expect(audit.getByRole('listitem').nth(0)).toContainText('in ops (C0123)')
      await expect(audit.getByRole('listitem').nth(1)).toContainText('sender U555')
      await expect(audit.getByRole('listitem').nth(1)).not.toContainText(' · U555')
      await expect(audit.getByTestId('connector-audit-chat')).toHaveCount(1)
      // The tool calls take the name from the sender log.
      await expect(page.getByTestId('connector-tool-outcomes')).toContainText('Grace Sample · U002')
      // The log says it is kept in memory.
      await expect(page.getByTestId('connector-access')).toContainText('kept in memory only')
      if (process.env.UI_SHOTS_DIR) await page.getByTestId('connector-access').screenshot({ path: `${SHOTS}/names-decisions-${width}.png` })
      if (process.env.UI_SHOTS_DIR) await page.getByTestId('connector-pairing').screenshot({ path: `${SHOTS}/names-pairing-${width}.png` })
    })

    test('the sender log shows the chat, and a reason for each ignored message', async ({ page }) => {
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'Senders' }).click()
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
