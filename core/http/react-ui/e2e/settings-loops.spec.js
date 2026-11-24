import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'

// The loop limits of Settings > Tools: three limits first, the rest under an
// Advanced disclosure, the run limit of a chat of a messaging app printed
// next to the run limit, and the same keys sent as before.

const SHOTS = process.env.UI_SHOTS_DIR
const row = (page, key) => page.locator(`[data-field="${key}"]`)
// What GET /api/settings returns for the loops when nothing was set: the
// values that apply.
const LOOP_DEFAULTS = {
  chats_loop_min_interval: '1m0s', chats_loop_max_runs: 100, chats_loop_max_per_chat: 3, chats_loop_max_per_user: 10,
  chats_loop_daily_runs: 200, chats_loop_daily_script_checks: 1500,
}

for (const width of [1280, 390]) {
  test.describe(`Settings: loops and waits at ${width} px`, () => {
    let state
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      state = await mockSettings(page, LOOP_DEFAULTS)
      await page.goto('/app/settings?group=tools')
      await expect(page.getByTestId('settings-page')).toBeVisible()
    })

    test('shows three limits, then the others under Advanced, closed while they hold their defaults', async ({ page }) => {
      const head = page.locator('.st-item[data-block="loops"] .st-h3')
      await expect(head).toHaveText('Loops and waits')
      for (const key of ['chats_loop_min_interval', 'chats_loop_daily_runs', 'chats_loop_max_per_chat']) {
        await expect(row(page, key)).toBeVisible()
      }
      await expect(row(page, 'chats_loop_max_per_chat')).toContainText('Loops and waits per chat')
      const adv = page.getByTestId('settings-advanced-loops')
      await expect(adv).not.toHaveAttribute('open', '')
      for (const key of ['chats_loop_max_per_user', 'chats_loop_max_runs', 'chats_loop_daily_script_checks']) {
        await expect(row(page, key)).toBeHidden()
      }
      if (SHOTS) await page.locator('.st-content').screenshot({ path: `${SHOTS}/settings-loops-${width}.png` })

      await adv.locator('> summary').click()
      await expect(row(page, 'chats_loop_max_per_user')).toContainText('Loops and waits per user')
      await expect(page.getByTestId('st-extra-chats_loop_max_runs')).toContainText('at most 100 times')
      await expect(page.getByTestId('st-extra-chats_loop_max_runs')).toContainText('at most 20 times')
      await expect(row(page, 'chats_loop_daily_script_checks')).toContainText('Command checks per day')
      if (SHOTS) await adv.screenshot({ path: `${SHOTS}/settings-loops-advanced-${width}.png` })
    })

    test('the effective run limit follows the field, and Apply sends the same keys', async ({ page }) => {
      await page.getByTestId('settings-advanced-loops').locator('> summary').click()
      await row(page, 'chats_loop_max_runs').getByRole('spinbutton').fill('50')
      await expect(page.getByTestId('st-extra-chats_loop_max_runs')).toContainText('at most 10 times')
      await row(page, 'chats_loop_daily_script_checks').getByRole('spinbutton').fill('600')
      await page.getByRole('button', { name: 'Apply' }).click()
      await expect(page.getByText('Settings saved successfully')).toBeVisible()
      expect(state.posts).toEqual([{ chats_loop_max_runs: 50, chats_loop_daily_script_checks: 600 }])
    })

    test('opens Advanced by itself when a value there differs from its default', async ({ page }) => {
      await mockSettings(page, { ...LOOP_DEFAULTS, chats_loop_daily_script_checks: 300 })
      await page.reload()
      await expect(page.getByTestId('settings-advanced-loops')).toHaveAttribute('open', '')
      await expect(row(page, 'chats_loop_daily_script_checks')).toBeVisible()
    })
  })
}
