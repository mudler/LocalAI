import { test, expect } from './coverage-fixtures.js'
import { stubServerChat } from './chat-settings-fixtures.js'

// The chat settings: the reasoning effort chip of the composer and the
// settings sheet show the same level, and the sampling values are sliders that
// save as numbers. The API is stubbed; the real server keeps them in
// chats-features-real.spec.js.

const openSheet = async (page) => {
  await page.getByRole('button', { name: 'Chat settings' }).click()
  const sheet = page.getByTestId('chat-settings')
  await expect(sheet).toBeVisible()
  return sheet
}

test.describe('Reasoning effort chip', () => {
  test('shows the level of the chat, changes it through the chat settings and the sheet shows it too', async ({ page }) => {
    const api = await stubServerChat(page, { settings: { temperature: 0.4, reasoning_effort: 'low' } })
    await page.goto('/app/chats/c1')
    const chip = page.getByTestId('chat-effort-button')
    await expect(chip).toContainText('Effort: Low')

    await chip.click()
    await expect(page.getByTestId('chat-effort-low')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('chat-effort-high').click()
    await expect(chip).toContainText('Effort: High')
    // The server replaces the settings as a whole: the other values go along.
    expect(api.patches).toEqual([{ settings: { temperature: 0.4, reasoning_effort: 'high' } }])
    await expect(page.getByTestId('chat-effort-menu')).toHaveCount(0)

    const sheet = await openSheet(page)
    await expect(sheet.getByTestId('chat-set-effort-high')).toHaveAttribute('aria-pressed', 'true')
    // The sheet applies the level at once, and the chip follows it.
    await sheet.getByTestId('chat-set-effort-none').click()
    await expect(sheet.getByTestId('chat-set-effort-none')).toHaveAttribute('aria-pressed', 'true')
    await expect(chip).toContainText('Effort: None')
    expect(api.patches.at(-1)).toEqual({ settings: { temperature: 0.4, reasoning_effort: 'none' } })
    // The save button stays off: the effort is not a pending change.
    await expect(sheet.getByTestId('chat-settings-save')).toBeDisabled()
  })

  test('"Default" returns the choice to the model and leaves the field out', async ({ page }) => {
    const api = await stubServerChat(page, { settings: { reasoning_effort: 'medium' } })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-effort-button').click()
    await page.getByTestId('chat-effort-default').click()
    await expect(page.getByTestId('chat-effort-button')).toContainText('Effort: Default')
    expect(api.patches).toEqual([{ settings: {} }])
  })

  test('the /effort slash action opens the menu on the level of the chat, and Escape gives the focus back', async ({ page }) => {
    await stubServerChat(page, { settings: { reasoning_effort: 'minimal' } })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-input').fill('/effort')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByTestId('chat-effort-menu')).toBeVisible()
    await expect(page.getByTestId('chat-effort-minimal')).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-effort-menu')).toHaveCount(0)
    await expect(page.getByTestId('chat-effort-button')).toBeFocused()
  })

  test('a level picked before the chat exists is saved to the chat once it is created', async ({ page }) => {
    const api = await stubServerChat(page)
    // A bare /app/chats with router state is a new chat page: Home sends there.
    await page.goto('/app')
    await page.evaluate(() => {
      window.history.pushState({ usr: { agentId: '' }, key: 'draft', idx: 1 }, '', '/app/chats')
      window.dispatchEvent(new PopStateEvent('popstate', { state: window.history.state }))
    })
    await page.getByTestId('chat-effort-button').click()
    await page.getByTestId('chat-effort-medium').click()
    await expect(page.getByTestId('chat-effort-button')).toContainText('Effort: Medium')
    await page.getByTestId('chat-input').fill('hello')
    await page.getByTestId('chat-send').click()
    await expect.poll(() => api.patches).toEqual([{ settings: { reasoning_effort: 'medium' } }])
    expect(api.creates).toHaveLength(1)
  })
})

test.describe('Sampling sliders', () => {
  test('keys, the number box and reset change the values, and Save sends them as numbers', async ({ page }) => {
    const api = await stubServerChat(page, { settings: { temperature: 0.7 } })
    await page.goto('/app/chats/c1')
    const sheet = await openSheet(page)
    const save = sheet.getByTestId('chat-settings-save')
    await expect(save).toBeDisabled()

    // The range is a native input: arrow keys move one step (0.05).
    const range = sheet.getByTestId('chat-set-temperature-range')
    await expect(range).toHaveAttribute('min', '0')
    await expect(range).toHaveAttribute('max', '2')
    await range.focus()
    await page.keyboard.press('ArrowRight')
    await page.keyboard.press('ArrowRight')
    await expect(sheet.getByTestId('chat-set-temperature-input')).toHaveValue('0.8')
    await page.keyboard.press('End')
    await expect(sheet.getByTestId('chat-set-temperature-input')).toHaveValue('2')

    // The number box moves the range, and a value past the ends is refused.
    await sheet.getByTestId('chat-set-top_p-input').fill('0.9')
    await expect(sheet.getByTestId('chat-set-top_p-range')).toHaveValue('0.9')
    await sheet.getByTestId('chat-set-top_k-input').fill('40')
    await expect(sheet.getByTestId('chat-set-top_k-range')).toHaveValue('40')
    await sheet.getByTestId('chat-set-temperature-input').fill('3')
    await expect(sheet.getByText('Use a number from 0 to 2.')).toBeVisible()
    await expect(save).toBeDisabled()

    // Reset returns a value to the model's own and the button goes off.
    await sheet.getByTestId('chat-set-temperature-reset').click()
    await expect(sheet.getByTestId('chat-set-temperature-input')).toHaveValue('')
    await expect(sheet.getByTestId('chat-set-temperature-reset')).toBeDisabled()
    await sheet.getByTestId('chat-set-temperature-input').fill('0.35')
    await expect(save).toBeEnabled()
    await save.click()
    await expect(sheet.getByRole('status')).toContainText('Saved')
    expect(api.patches).toEqual([{ settings: { temperature: 0.35, top_p: 0.9, top_k: 40 } }])
  })

  test('a slider value cannot go below the first step of top P, which the server needs above 0', async ({ page }) => {
    await stubServerChat(page)
    await page.goto('/app/chats/c1')
    const sheet = await openSheet(page)
    const range = sheet.getByTestId('chat-set-top_p-range')
    await expect(range).toHaveAttribute('min', '0.01')
    // Unset, the thumb rests on the first step; one key press sets the value.
    await range.focus()
    await page.keyboard.press('ArrowRight')
    await expect(sheet.getByTestId('chat-set-top_p-input')).toHaveValue('0.02')
    await page.keyboard.press('ArrowLeft')
    await page.keyboard.press('ArrowLeft')
    await expect(sheet.getByTestId('chat-set-top_p-input')).toHaveValue('0.01')
  })
})

test.describe('Settings on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  for (const theme of ['light', 'dark']) {
    test(`the chip row and the sheet fit 390 px without sideways scroll, ${theme}`, async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
      await stubServerChat(page, { settings: { temperature: 0.7, top_p: 0.9, reasoning_effort: 'high' } })
      await page.goto('/app/chats/c1')
      await expect(page.getByTestId('chat-effort-button')).toBeVisible()
      const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(await overflow()).toBeLessThanOrEqual(0)

      const sheet = await openSheet(page)
      expect(await overflow()).toBeLessThanOrEqual(0)
      const box = await sheet.boundingBox()
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(390)
      for (const key of ['temperature', 'top_p', 'top_k']) {
        const row = await sheet.getByTestId(`chat-set-${key}`).boundingBox()
        expect(row.x + row.width).toBeLessThanOrEqual(box.x + box.width)
        await expect(sheet.getByTestId(`chat-set-${key}-reset`)).toBeVisible()
      }
      // The save button is in the footer and stays on screen.
      // The sheet slides in: wait until it rests.
      await expect.poll(async () => {
        const save = await sheet.getByTestId('chat-settings-save').boundingBox()
        return save.y + save.height
      }).toBeLessThanOrEqual(844)
    })
  }
})
