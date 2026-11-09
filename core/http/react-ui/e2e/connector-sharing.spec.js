import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// Sharing a messaging connection from the connectors page: the owner opens
// the Share dialog of a binding, with the one role use and no grant to
// everyone; the bindings other people shared are listed apart, read only. The
// API is stubbed.

const AGENTS = [{ id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] }]
const KIND = { addon: 'telegram', kind: 'telegram', title: 'Telegram', description: 'Talk to one of your saved agents from a Telegram bot.', page: 'bots' }
const RID = 'r0123456789abcdef0123456789abcde'
const MINE = { id: 'b1', name: 'Support bot', kind: 'telegram', addon: 'telegram', active: true, agent_id: 'a1', enabled: true, accept_any: false, allow: ['42'], has_token: true }
const THEIRS = { id: 's1', name: 'Team bot', kind: 'telegram', addon: 'telegram', enabled: true, active: true, owner: 'Alice Martin', role: 'use' }

async function api(page, { items = [MINE], shared = [THEIRS], sharing = true } = {}) {
  const s = { calls: [] }
  await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: AGENTS } }))
  await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds: [KIND] } }))
  await page.route(/\/api\/chats\/connectors\/[^/?]+\/share$/, r => {
    s.calls.push(`share ${new URL(r.request().url()).pathname.split('/')[4]}`)
    return r.fulfill({ json: { resource_id: RID, shared_with: 0 } })
  })
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => r.fulfill({ json: { connectors: items, shared, sharing } }))
  await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
  await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
  await page.route(`**/api/shares/connection/${RID}/grants`, r => {
    s.calls.push(`grants ${r.request().headers()['x-localai-confirm']}`)
    return r.fulfill({ json: { kind: 'connection', resourceId: RID, roles: ['use'], everyone: false, grants: [{ userId: 'u2', displayName: 'Bob Okafor', role: 'use' }] } })
  })
  return s
}

test.describe('Sharing connections', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('marks a shared binding of the owner and lists the bindings shared with the person, read only', async ({ page }) => {
    await api(page, { items: [{ ...MINE, share: { resource_id: RID, shared_with: 2 } }] })
    await page.goto('/app/chats/connectors?tab=messaging')
    const own = page.getByTestId('connector-row')
    await expect(own.getByTestId('connector-shared-with')).toHaveText('Shared with 2')
    await expect(own.getByRole('button', { name: 'Share Support bot' })).toBeVisible()

    const shared = page.getByTestId('connector-shared')
    await expect(shared.getByRole('heading', { name: 'Shared with you' })).toBeVisible()
    const row = shared.getByTestId('connector-shared-row')
    await expect(row).toContainText('Team bot')
    await expect(row).toContainText('Telegram')
    await expect(row).toContainText('Listening')
    await expect(row.getByTestId('shared-by-line')).toHaveText('Shared by Alice Martin')
    await expect(row.getByTestId('role-badge')).toHaveText('Can use')
    // Nothing a grantee could change: no switch, no Share, no edit or delete.
    await expect(row.getByRole('checkbox')).toHaveCount(0)
    await expect(row.getByRole('button')).toHaveCount(0)
    await expect(row.getByRole('link')).toHaveCount(0)
  })

  test('opens the Share dialog of a binding with the use role only and no grant to everyone', async ({ page }) => {
    const s = await api(page)
    await page.goto('/app/chats/connectors?tab=messaging')
    await page.getByRole('button', { name: 'Share Support bot' }).click()
    const dialog = page.getByTestId('connector-share-dialog')
    await expect(dialog.getByRole('heading', { name: 'Share "Support bot"' })).toBeVisible()
    await expect(dialog).toContainText('never its token or its settings')
    await expect(dialog.getByTestId('share-grant')).toContainText('Bob Okafor')
    await expect(dialog.getByTestId('share-everyone-add')).toHaveCount(0)
    expect(s.calls).toEqual(['share b1', `grants ${RID}`])
    await dialog.getByRole('button', { name: 'Close' }).last().click()
    await expect(dialog).toHaveCount(0)
  })

  test('uses the share ID the list already has', async ({ page }) => {
    const s = await api(page, { items: [{ ...MINE, share: { resource_id: RID, shared_with: 1 } }] })
    await page.goto('/app/chats/connectors?tab=messaging')
    await page.getByRole('button', { name: 'Share Support bot' }).click()
    await expect(page.getByTestId('connector-share-dialog').getByTestId('share-grant')).toBeVisible()
    expect(s.calls).toEqual([`grants ${RID}`])
  })

  test('shows nothing of sharing when the server cannot share', async ({ page }) => {
    await api(page, { sharing: false })
    await page.goto('/app/chats/connectors?tab=messaging')
    await expect(page.getByTestId('connector-row')).toBeVisible()
    await expect(page.getByTestId('connector-share')).toHaveCount(0)
    await expect(page.getByTestId('connector-shared')).toHaveCount(0)
  })

  test('fits a phone with a shared binding', async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 740 })
    await api(page)
    await page.goto('/app/chats/connectors?tab=messaging')
    await expect(page.getByTestId('connector-shared-row')).toBeVisible()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)
  })
})
