import { test, expect } from './coverage-fixtures.js'

// Seeds two-message chat into localStorage so we don't need a live model.
async function seedChat(page, history) {
  await page.addInitScript((h) => {
    const chat = {
      id: 'seed1', name: 'Seeded Chat', model: 'test-model',
      history: h, systemPrompt: '', mcpMode: false, mcpServers: [],
      clientMCPServers: [], temperature: null, topP: null, topK: null,
      tokenUsage: { prompt: 0, completion: 0, total: 0 },
      contextSize: null, createdAt: Date.now(), updatedAt: Date.now(),
    }
    localStorage.setItem('localai_chats_data', JSON.stringify({
      chats: [chat], activeChatId: 'seed1', lastSaved: Date.now(),
    }))
  }, history)
}

async function mockModels(page) {
  await page.route('**/api/models/capabilities', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'] }] }),
  }))
  await page.route('**/api/operations', (route) => route.fulfill({
    contentType: 'application/json', body: JSON.stringify({ operations: [] }),
  }))
}

const TWO_TURNS = [
  { role: 'user', content: 'first question' },
  { role: 'assistant', content: 'first answer' },
  { role: 'user', content: 'second question' },
  { role: 'assistant', content: 'second answer' },
]

test('duplicate creates an independent copy and switches to it', async ({ page }) => {
  await mockModels(page)
  await seedChat(page, TWO_TURNS)
  await page.goto('/app/chat')

  // Open the chats menu (Ctrl/Cmd+K) and duplicate the seeded chat.
  // Wait for the menu trigger to mount so its global keydown listener is armed
  // before we dispatch the shortcut.
  await page.getByTitle('Conversations (Ctrl/Cmd+K)').waitFor()
  await page.keyboard.press('Control+k')
  await page.getByTitle('Duplicate chat').first().click()

  // A new active chat named "Seeded Chat (fork)" with the same 4 messages.
  await expect(page.locator('.chat-header-title')).toHaveText('Seeded Chat (fork)')
  await expect(page.locator('.chat-message-user')).toHaveCount(2)
  await expect(page.locator('.chat-message-assistant')).toHaveCount(2)
})
