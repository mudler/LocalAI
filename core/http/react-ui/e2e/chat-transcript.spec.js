import { test, expect } from './coverage-fixtures.js'

// Chat reads as a transcript rather than a bubble thread (mock 04).

const CHAT = {
  chats: [{
    id: 'c1', name: 'Transcript', model: 'mock-model',
    history: [
      { role: 'user', content: 'Which backends do I have?' },
      { role: 'assistant', content: 'Seven are installed.' },
    ],
  }],
  activeChatId: 'c1',
}

test.describe('Chat transcript', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(chat => {
      localStorage.setItem('localai_chats_data', JSON.stringify(chat))
    }, CHAT)
    await page.goto('/app/chat')
  })

  test('neither role is a filled, rounded bubble', async ({ page }) => {
    const user = page.locator('.chat-message-user .chat-message-content').first()
    await expect(user).toBeVisible()
    const cs = await user.evaluate(el => {
      const s = getComputedStyle(el)
      return { radius: s.borderTopLeftRadius, shadow: s.boxShadow }
    })
    // A rounded filled bubble carries the speaker in shape and side; a
    // transcript carries it in words, which survives being read aloud.
    expect(cs.radius).toBe('0px')
    expect(cs.shadow).toBe('none')
  })

  test('both turns run full width in one column, not left and right', async ({ page }) => {
    const user = page.locator('.chat-message-user').first()
    const assistant = page.locator('.chat-message-assistant').first()
    const [u, a] = [await user.boundingBox(), await assistant.boundingBox()]
    expect(Math.abs(u.x - a.x)).toBeLessThan(2)
  })

  test('every turn says who is speaking', async ({ page }) => {
    await expect(page.locator('.chat-message-user .chat-message-model')).toHaveText('You')
    await expect(page.locator('.chat-message-assistant .chat-message-model').first())
      .toHaveText('mock-model')
  })

  test('turns are separated by a rule', async ({ page }) => {
    const border = await page.locator('.chat-message').first()
      .evaluate(el => getComputedStyle(el).borderBottomStyle)
    expect(border).toBe('solid')
  })
})
