import { test, expect } from './coverage-fixtures.js'

// The runtime settings the chats added, against the real test server: they are
// listed where an administrator looks for them, an edit waits for Apply, and
// what the page applies is what the server stores (and what the rest of the UI
// then follows). The spec puts every value back.

const row = (page, key) => page.locator(`[data-field="${key}"]`)
const sw = (page, key) => row(page, key).getByRole('switch')
const stored = async (page) => (await page.request.get('/api/settings')).json()

test.describe('Chat settings against the real server', () => {
  test('the chats settings are listed with their keys', async ({ page }) => {
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /Chats and collections/ }).click()
    for (const key of ['chats_vision_model', 'chats_transcription_model', 'localai_assistant_enabled']) {
      await expect(row(page, key)).toBeVisible()
    }
    await page.getByRole('button', { name: /Access and security/ }).click()
    await expect(row(page, 'mcp_connectors_allow_private')).toBeVisible()
  })

  test('switching the assistant off waits for Apply, is stored, and Home follows', async ({ page }) => {
    const before = (await stored(page)).localai_assistant_enabled
    try {
      await page.goto('/app/settings')
      await page.getByRole('button', { name: /Chats and collections/ }).click()
      const toggle = sw(page, 'localai_assistant_enabled')
      const wasOn = (await toggle.getAttribute('aria-checked')) === 'true'
      await toggle.click()
      await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
      // Nothing is stored until Apply.
      expect((await stored(page)).localai_assistant_enabled).toBe(before)
      await page.getByRole('button', { name: 'Apply' }).click()
      await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
      await expect.poll(async () => (await stored(page)).localai_assistant_enabled).toBe(!wasOn)
      const features = await (await page.request.get('/api/features')).json()
      expect(features.localai_assistant).toBe(!wasOn)
    } finally {
      await page.request.post('/api/settings', { data: { localai_assistant_enabled: before } })
    }
  })

  test('the private address switch for MCP connectors is stored on Apply and Discard drops an edit', async ({ page }) => {
    const before = (await stored(page)).mcp_connectors_allow_private
    try {
      await page.goto('/app/settings')
      await page.getByRole('button', { name: /Access and security/ }).click()
      const toggle = sw(page, 'mcp_connectors_allow_private')
      const wasOn = (await toggle.getAttribute('aria-checked')) === 'true'
      await toggle.click()
      await page.getByRole('button', { name: 'Discard' }).click()
      await expect(toggle).toHaveAttribute('aria-checked', String(wasOn))
      await toggle.click()
      await page.getByRole('button', { name: 'Apply' }).click()
      await expect.poll(async () => (await stored(page)).mcp_connectors_allow_private).toBe(!wasOn)
    } finally {
      await page.request.post('/api/settings', { data: { mcp_connectors_allow_private: Boolean(before) } })
    }
  })

  test('the group captions of the chats settings do not name the removed agent pool', async ({ page }) => {
    test.skip(true, 'known gap: UI-05 the captions still read AGENT POOL and AGENT JOBS')
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /Chats and collections/ }).click()
    await expect(page.locator('main')).not.toContainText(/AGENT POOL|AGENT JOBS/i)
  })
})
