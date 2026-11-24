import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { setTheme } from './home-fixtures.js'

// The limit on add-on tools of a chat, in Settings > Tools: it shows the value
// that applies, says what a higher value costs, refuses a value out of bounds
// and sends the key as before. The API is stubbed.

const SHOTS = process.env.TOOLCAP_SHOT_DIR
const row = (page) => page.locator('[data-field="chats_addon_tool_limit"]')

test.describe('Settings: add-on tools per chat', () => {
  let state
  test.beforeEach(async ({ page }) => {
    await setTheme(page, 'light')
    await page.setViewportSize({ width: 1280, height: 900 })
    state = await mockSettings(page, { chats_addon_tool_limit: 64 })
    await page.goto('/app/settings?group=tools')
    await expect(page.getByTestId('settings-page')).toBeVisible()
  })

  test('shows the limit that applies and the cost of raising it', async ({ page }) => {
    await expect(row(page)).toContainText('Add-on tools per chat')
    await expect(row(page).getByRole('spinbutton')).toHaveValue('64')
    await expect(row(page)).toContainText('from 16 to 256')
    await expect(row(page)).toContainText('costs model context')
    await expect(row(page)).toContainText('at most 32 tools')
    if (SHOTS) { await row(page).scrollIntoViewIfNeeded(); await page.waitForTimeout(300); await page.screenshot({ path: `${SHOTS}/settings-tool-cap.png` }) }
  })

  test('sends the new limit with Apply, and applies with no restart', async ({ page }) => {
    await row(page).getByRole('spinbutton').fill('96')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText('Settings saved successfully')).toBeVisible()
    expect(state.posts).toEqual([{ chats_addon_tool_limit: 96 }])
    await expect(page.getByText(/restart/i)).toHaveCount(0)
  })

  test('keeps the limit inside its bounds', async ({ page }) => {
    const input = row(page).getByRole('spinbutton')
    await input.fill('8')
    await expect(input).toHaveValue('16')
    await input.fill('300')
    await expect(input).toHaveValue('256')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText('Settings saved successfully')).toBeVisible()
    expect(state.posts).toEqual([{ chats_addon_tool_limit: 256 }])
  })

  test('opens from the link of the chat notice', async ({ page }) => {
    await page.goto('/app/settings?group=tools#settings-field-chats_addon_tool_limit')
    await expect(row(page)).toBeInViewport()
  })
})
