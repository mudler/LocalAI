import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi as api } from './connector-fixtures.js'

// The page of a connector add-on (the real Telegram bundle) shares a binding
// with the host's Share dialog and lists the bindings of its kind that other
// people shared, read only. The core API is stubbed.

const PAGE = '/app/addons/telegram/bots'
const RID = 'r0123456789abcdef0123456789abcde'
const MINE = { id: 'b1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', enabled: true, accept_any: false, allow: ['42'], has_token: true }
const THEIRS = { id: 's1', name: 'Team bot', kind: 'telegram', addon: 'telegram', enabled: true, active: true, owner: 'Alice Martin', role: 'use' }
const OTHER_KIND = { ...THEIRS, id: 's2', name: 'Slack helper', kind: 'slack', addon: 'slack' }

async function sharing(page, { on = true } = {}) {
  const calls = []
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => (r.request().method() === 'GET'
    ? r.fulfill({ json: { connectors: [MINE], shared: [THEIRS, OTHER_KIND], sharing: on } })
    : r.fallback()))
  await page.route(/\/api\/chats\/connectors\/b1\/share$/, r => { calls.push('share'); return r.fulfill({ json: { resource_id: RID, shared_with: 0 } }) })
  await page.route(`**/api/shares/connection/${RID}/grants`, r => {
    calls.push(`grants ${r.request().headers()['x-localai-confirm']}`)
    return r.fulfill({ json: { grants: [] } })
  })
  return calls
}

test.describe('Sharing from a connector add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['telegram'] })
  })

  test('shares a binding with the use role only and lists the shared bindings of this kind, read only', async ({ page }) => {
    await api(page).install()
    const calls = await sharing(page)
    await page.goto(PAGE)
    const shared = page.getByTestId('connector-shared')
    await expect(shared.getByTestId('connector-shared-row')).toHaveCount(1)
    await expect(shared).toContainText('Team bot')
    await expect(shared).not.toContainText('Slack helper')
    await expect(shared.getByTestId('shared-by-line')).toHaveText('Shared by Alice Martin')
    await expect(shared.getByRole('button')).toHaveCount(0)

    // Share is at the foot of the pane of the row (and in the row menu).
    await page.getByTestId('connector-row').filter({ hasText: 'Support bot' }).getByTestId('connector-open').click()
    await expect(page.getByTestId('connector-pane')).toBeVisible()
    await page.getByRole('button', { name: 'Share Support bot' }).click()
    const dialog = page.getByTestId('connector-share-dialog')
    await expect(dialog.getByRole('heading', { name: 'Share "Support bot"' })).toBeVisible()
    await expect(dialog.getByTestId('share-no-grants')).toBeVisible()
    await expect(dialog.getByTestId('share-everyone-add')).toHaveCount(0)
    expect(calls).toEqual(['share', `grants ${RID}`])
  })

  test('shows no Share button where the server cannot share', async ({ page }) => {
    await api(page).install()
    await sharing(page, { on: false })
    await page.goto(PAGE)
    await expect(page.getByTestId('connector-row')).toContainText('Support bot')
    await page.getByTestId('connector-row').getByTestId('connector-open').click()
    await expect(page.getByTestId('connector-pane')).toBeVisible()
    await expect(page.getByTestId('connector-share')).toHaveCount(0)
    await expect(page.getByTestId('connector-shared')).toHaveCount(0)
  })
})
