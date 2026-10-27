import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The chats page on a phone: the list of chats is a panel the header opens, so
// the thread and the composer get the whole width.

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
    await expect(page.getByRole('textbox', { name: 'Message' })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const box = await page.getByRole('textbox', { name: 'Message' }).boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(390 * 0.7)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
  })

  test('the list of chats is closed at first, opens from the header and closes again', async ({ page }) => {
    await page.goto('/app/chats/c1')
    const toggle = page.getByRole('button', { name: 'Chats', exact: true })
    const list = page.getByRole('complementary', { name: 'Your chats' })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(list).toBeHidden()

    await toggle.focus()
    await page.keyboard.press('Enter')
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(list).toBeVisible()
    await expect(list.getByRole('button', { name: 'Release notes', exact: true })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)

    await page.keyboard.press('Enter')
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(list).toBeHidden()
  })

  test('choosing a chat closes the list', async ({ page }) => {
    await page.route('**/api/chats/c2/events', route => route.abort())
    await page.route(/\/api\/chats\/c2\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
    await page.route('**/api/chats/c2', route => route.fulfill({ json: { ...chats[1], pending: [] } }))
    await page.goto('/app/chats/c1')
    await page.getByRole('button', { name: 'Chats', exact: true }).click()
    await page.getByRole('complementary', { name: 'Your chats' }).getByRole('button', { name: 'Release notes', exact: true }).click()
    await expect(page).toHaveURL(/\/app\/chats\/c2$/)
    await expect(page.getByRole('complementary', { name: 'Your chats' })).toBeHidden()
  })
})

test.describe('Chats on a desktop', () => {
  test('the list sits beside the thread and the header button is hidden', async ({ page }) => {
    await stub(page)
    await page.goto('/app/chats/c1')
    await expect(page.getByRole('complementary', { name: 'Your chats' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Chats', exact: true })).toBeHidden()
  })
})
