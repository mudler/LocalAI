import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { AT, INSTALLS, openPluginChat as open } from './chats-plugins-fixtures.js'

// The plugins of a chat with the API stubbed: their skills grouped under
// "Plugin: <name>" in the Skills menu with the chat's switch for the plugin,
// the / completion of a plugin command with its argument hint and the
// message it sends, the plugin switch of the Tools menu, and a connector a
// plugin owns, read only on the Connectors page.

const box = (page) => page.getByTestId('chat-input')

test.describe('Plugins in a chat', () => {
  test('the Skills menu groups plugin skills and switches the plugin off for the chat', async ({ page }) => {
    const api = await open(page)
    await page.getByTestId('chat-skills-button').click()
    const group = page.getByTestId('chat-skills-plugin')
    await expect(group).toContainText('Plugin: review')
    await expect(group.getByRole('switch', { name: 'review:check', exact: true })).toBeChecked()
    await group.getByTestId('chat-skills-plugin-switch').click()
    await expect.poll(() => api.puts).toEqual([{ off: ['in-1'] }])
    await expect(group).toContainText('Off in this chat')
    await expect(group.getByRole('switch', { name: 'review:check', exact: true })).toHaveCount(0)
    await group.getByTestId('chat-skills-plugin-switch').click()
    await expect.poll(() => api.puts.at(-1)).toEqual({ off: [] })
    await expect(group.getByRole('switch', { name: 'review:check', exact: true })).toBeChecked()
  })

  test('/ completes a plugin command with its hint and sends it as the skill', async ({ page }) => {
    const api = await open(page)
    await box(page).fill('/rev')
    const row = page.getByTestId('home-slash-plugin-review:pr')
    await expect(row).toBeVisible()
    await expect(row).toContainText('Review a pull request')
    await expect(row).toContainText('<number>')
    await expect(page.getByTestId('home-slash-plugin-review:check')).toContainText('<file>')
    await expect(page.locator('#home-slash-group-plugins')).toHaveText('Plugins')
    await row.click()
    await expect(box(page)).toHaveValue('/review:pr ')
    await box(page).pressSequentially('42')
    await box(page).press('Enter')
    await expect.poll(() => api.sent.length).toBe(1)
    expect(api.sent[0]).toEqual({ text: '42', skill: 'review:pr' })
    await expect(page.getByTestId('message-invoked').last()).toHaveText('Command: review:pr')
  })

  test('the Tools menu has one switch per plugin with connectors', async ({ page }) => {
    const api = await open(page)
    await page.getByTestId('chat-tools-button').click()
    const group = page.getByTestId('chat-tools-group-plugins')
    await expect(group).toContainText('review-search')
    await group.getByRole('switch', { name: 'review', exact: true }).click()
    await expect.poll(() => api.puts).toEqual([{ off: ['in-1'] }])
  })

  test('a connector of a plugin is read only on the Connectors page', async ({ page }) => {
    await mockChat(page)
    await page.route('**/api/plugins/installs', (route) => route.fulfill({ json: { installs: INSTALLS } }))
    await page.route(/\/api\/chats\/mcp-connectors(\?.*)?$/, (route) => route.fulfill({ json: { connectors: [
      { id: 'k1', name: 'review-search', transport: 'http', url: 'https://docs.example.com/mcp', args: [], has_token: false, has_headers: false, has_env: false, enabled: true, created: AT, updated: AT, plugin_install: 'in-1', plugin_server: 'search' },
      { id: 'k2', name: 'mine', transport: 'http', url: 'https://mcp.example.org', args: [], has_token: false, has_headers: false, has_env: false, enabled: true, created: AT, updated: AT },
    ] } }))
    await page.route(/\/api\/chats(\?.*)?$/, (route) => route.fulfill({ json: { conversations: [], persistent: true, files: true } }))
    await page.goto('/app/mcp-connectors')
    const owned = page.getByTestId('mcp-row').filter({ hasText: 'review-search' })
    await expect(owned.getByTestId('mcp-row-plugin')).toHaveText('Plugin: review')
    await expect(owned.getByTestId('mcp-switch')).toBeDisabled()
    await expect(page.getByTestId('mcp-row').filter({ hasText: 'mine' }).getByTestId('mcp-switch')).toBeEnabled()
    await owned.locator('.ad-tile__open').click()
    const pane = page.getByTestId('mcp-details')
    await expect(pane.getByTestId('mcp-details-plugin')).toContainText('Managed by the plugin review')
    await expect(pane.getByTestId('mcp-details-remove')).toHaveCount(0)
    await expect(pane.getByTestId('mcp-details-plugin-link')).toHaveAttribute('href', /\/app\/addons\?tab=plugins&view=installed&install=in-1$/)
  })

  test('an incognito chat keeps its plugin commands and connectors, and every plugin request carries the marker', async ({ page }) => {
    const api = await open(page, { incognito: 'zero-retention' })
    // The connector of the plugin is in the Tools menu, with its switch.
    await page.getByTestId('chat-tools-button').click()
    const group = page.getByTestId('chat-tools-group-plugins')
    await expect(group).toContainText('review-search')
    await group.getByRole('switch', { name: 'review', exact: true }).click()
    await expect.poll(() => api.puts).toEqual([{ off: ['in-1'] }])
    await group.getByRole('switch', { name: 'review', exact: true }).click()
    await expect.poll(() => api.puts.at(-1)).toEqual({ off: [] })
    await page.keyboard.press('Escape')
    // The command completes and goes as the skill of the message.
    await box(page).fill('/review:p')
    await page.getByTestId('home-slash-plugin-review:pr').click()
    await box(page).pressSequentially('7')
    await box(page).press('Enter')
    await expect.poll(() => api.sent.length).toBe(1)
    expect(api.sent[0]).toEqual({ text: '7', skill: 'review:pr' })
    // The requests of the open chat carry the marker: its plugin switch and
    // its messages.
    const unmarked = api.markers.filter(([what, level]) => ['switch', 'message'].includes(what) && level !== 'zero-retention')
    expect(unmarked).toEqual([])
    expect(api.markers.some(([what]) => what === 'switch')).toBe(true)
  })
})
