import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The chats page on a phone: the conversations are a menu the header opens
// (Ctrl K on a keyboard), so the thread and the composer get the whole width.

const chats = ['c1', 'c2'].map((id, i) => ({
  id, title: i ? 'Release notes' : 'Quotes', model: 'qwen3-8b', kind: 'chat',
  created: '2026-10-25T10:00:00Z', updated: `2026-10-25T10:0${i}:00Z`, running: false,
}))

async function stub(page) {
  await mockChat(page)
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: chats, persistent: true, files: true } }))
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'compare the quotes', at: '2026-10-25T10:00:00Z' }] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...chats[0], pending: [] } }))
}

test.describe('Chats on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test.beforeEach(async ({ page }) => { await stub(page) })

  test('the thread fits: no sideways scroll and a composer across the width', async ({ page }) => {
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-input')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const box = await page.getByTestId('chat-input').boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(390 * 0.7)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
  })

  test('the conversations menu opens from the header and lists the chats without sideways scroll', async ({ page }) => {
    await page.goto('/app/chats/c1')
    const trigger = page.getByTestId('chats-trigger')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await trigger.click()
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')
    const menu = page.getByTestId('chats-menu')
    await expect(menu.getByTestId('chats-row')).toHaveCount(2)
    await expect(menu.getByText('Release notes')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    await page.keyboard.press('Escape')
    await expect(menu).toBeHidden()
  })

  test('choosing a chat opens it and closes the menu', async ({ page }) => {
    await page.route('**/api/chats/c2/events', route => route.abort())
    await page.route(/\/api\/chats\/c2\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
    await page.route('**/api/chats/c2', route => route.fulfill({ json: { ...chats[1], pending: [] } }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chats-trigger').click()
    await page.getByTestId('chats-menu').getByText('Release notes').click()
    await expect(page).toHaveURL(/\/app\/chats\/c2$/)
    await expect(page.getByTestId('chats-menu')).toBeHidden()
  })
})

test.describe('Chats on a desktop', () => {
  test('Ctrl K opens the conversations menu from anywhere on the page', async ({ page }) => {
    await stub(page)
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-title')).toHaveText('Quotes')
    await page.keyboard.press('Control+k')
    await expect(page.getByTestId('chats-menu')).toBeVisible()
    await expect(page.getByTestId('chats-row').first()).toContainText('Release notes')
  })
})
