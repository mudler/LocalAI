import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The composer on a phone with the soft keyboard open. The keyboard leaves a
// short viewport (the Android app and a browser with
// interactive-widget=resizes-content both resize the page), and the composer's
// chips and buttons must still sit side by side: none drawn over another, none
// out of the card, and the chat's composer in view above the keyboard.

const CHAT = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '' }

async function stubChat(page) {
  await mockChat(page)
  await page.route('**/api/notifications/events', route => route.abort())
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'compare the quotes', at: '2026-10-25T10:00:00Z' }] } }))
  // Skills put one more chip on the bar, as on a real server.
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [
    { id: 'personal:notes', name: 'notes', layer: 'personal', enabled: true },
    { id: 'personal:pdf', name: 'pdf', layer: 'personal', enabled: true },
  ] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...CHAT, pending: [] } }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [CHAT], persistent: true, files: true, files_reason: '' } }))
}

// The boxes of the visible buttons of the composer, and the box of its card.
async function controls(page, testId) {
  return page.getByTestId(testId).evaluate((card) => {
    const box = (el) => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width } }
    const buttons = [...card.querySelectorAll('button')]
      .filter(b => b.checkVisibility() && b.getBoundingClientRect().height > 0)
      .map(b => ({ name: b.getAttribute('aria-label') || b.getAttribute('data-testid') || b.textContent.trim().slice(0, 24), ...box(b) }))
    return { card: box(card), buttons }
  })
}

function expectApart({ card, buttons }) {
  expect(buttons.length).toBeGreaterThan(2)
  // Layout rounds to 1/64 px: a sub-pixel touch is not an overlap.
  const eps = 0.5
  for (const b of buttons) {
    expect(b.width, `${b.name} keeps its width`).toBeGreaterThanOrEqual(20)
    expect(b.left, `${b.name} starts inside the card`).toBeGreaterThanOrEqual(card.left - eps)
    expect(b.right, `${b.name} ends inside the card`).toBeLessThanOrEqual(card.right + eps)
  }
  for (let i = 0; i < buttons.length; i++) {
    for (let j = i + 1; j < buttons.length; j++) {
      const a = buttons[i], b = buttons[j]
      const overlap = Math.min(a.right, b.right) - Math.max(a.left, b.left) > eps && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > eps
      expect(overlap, `${a.name} and ${b.name} do not overlap`).toBe(false)
    }
  }
}

// The keyboard opens: the viewport loses its lower part.
async function openKeyboard(page, width, height) {
  await page.setViewportSize({ width, height })
  await page.waitForFunction(h => window.visualViewport && Math.round(window.visualViewport.height) === h, height)
}

for (const [width, height] of [[360, 400], [320, 360]]) {
  test.describe(`The composer on a ${width} px phone with the keyboard open`, () => {
    test.use({ viewport: { width, height: 740 } })

    test('Home: the chips and buttons do not overlap', async ({ page }) => {
      await mockHome(page)
      await page.goto('/app')
      const input = page.getByTestId('home-input')
      await expect(input).toBeVisible({ timeout: 15_000 })
      await input.click()
      await openKeyboard(page, width, height)
      await input.scrollIntoViewIfNeeded()
      await expect(input).toBeFocused()
      expectApart(await controls(page, 'home-composer'))
    })

    test('Chat: the chips and buttons do not overlap and the composer stays in view', async ({ page }) => {
      await stubChat(page)
      await page.goto('/app/chats/c1')
      const input = page.getByTestId('chat-input')
      await expect(input).toBeVisible()
      await expect(page.getByTestId('chat-skills-button')).toBeVisible()
      await input.click()
      await openKeyboard(page, width, height)
      await expect(input).toBeFocused()
      const found = await controls(page, 'chat-composer')
      expectApart(found)
      // The chat page does not scroll: the whole composer must fit above the keyboard.
      expect(found.card.top).toBeGreaterThanOrEqual(0)
      expect(found.card.bottom).toBeLessThanOrEqual(height)
      await expect(page.getByTestId('chat-send')).toBeInViewport()
    })
  })
}
