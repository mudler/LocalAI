import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, openChat } from './chat-fixtures.js'

// The thread: your turns are raised blocks on the right, the model's turns are
// plain prose under its name, both at one 760 px measure. Both turns still say
// who is speaking in words, so the thread reads aloud and prints well.

const THREAD = chatOf('c1', 'Transcript', 'qwen3-8b', pair('Which backends do I have?', 'Seven are installed.'))

test.describe('Chat thread layout', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await openChat(page, [THREAD])
    await expect(page.getByTestId('chat-message').first()).toBeVisible()
  })

  test('your turn is a raised block, the reply is plain prose', async ({ page }) => {
    const user = page.locator('[data-role="user"] .cx-bubble')
    const cs = await user.evaluate(el => {
      const s = getComputedStyle(el)
      return { radius: parseFloat(s.borderTopLeftRadius), bg: s.backgroundColor, shadow: s.boxShadow }
    })
    expect(cs.radius).toBeGreaterThan(8)
    expect(cs.bg).not.toBe('rgba(0, 0, 0, 0)')
    expect(cs.shadow).not.toBe('none')

    const prose = await page.locator('[data-role="assistant"] .cx-prose').evaluate(el => {
      const s = getComputedStyle(el)
      return { bg: s.backgroundColor, border: s.borderLeftWidth, shadow: s.boxShadow }
    })
    // No fill, no rail, no shadow: the reply is text on the page.
    expect(prose.bg).toBe('rgba(0, 0, 0, 0)')
    expect(prose.border).toBe('0px')
    expect(prose.shadow).toBe('none')
  })

  test('your turn sits on the right and the reply on the left of one 760 px column', async ({ page }) => {
    const thread = await page.getByTestId('chat-thread').boundingBox()
    expect(thread.width).toBeLessThanOrEqual(760)
    const user = await page.locator('[data-role="user"] .cx-bubble').boundingBox()
    const reply = await page.locator('[data-role="assistant"] .cx-prose').boundingBox()
    expect(Math.abs(user.x + user.width - (thread.x + thread.width))).toBeLessThan(2)
    expect(Math.abs(reply.x - thread.x)).toBeLessThan(2)
  })

  test('every turn says who is speaking', async ({ page }) => {
    await expect(page.locator('[data-role="assistant"] .cx-who b')).toHaveText('qwen3-8b')
    // Your turn carries the word for assistive technology and for reading aloud.
    await expect(page.locator('[data-role="user"]')).toHaveAttribute('aria-label', 'You')
  })

  test('the reply names whether its model is loaded', async ({ page }) => {
    // qwen3-8b is in the stubbed loaded list, so its dot is filled.
    await expect(page.locator('[data-role="assistant"] .cx-who .home-dot')).not.toHaveClass(/home-dot--cold/)
  })
})
