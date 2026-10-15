import { test, expect } from './coverage-fixtures.js'
import {
  chatOf, pair, mockChat, openChat, storedChats, controlStream, answerWith,
} from './chat-fixtures.js'
import { sampleChats } from './home-fixtures.js'

// The states around the thread: an empty chat, no model, a reply waiting for a
// model, the phone layout and reduced motion.

test.describe('Empty chat', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('says what the page is for, offers starters and says whether the model is ready', async ({ page }) => {
    await openChat(page, [chatOf('n1', 'New Chat', 'qwen3-8b', [])])
    await expect(page.getByTestId('chat-empty').getByRole('heading')).toHaveText('What are we working on?')
    await expect(page.getByTestId('chat-starters').getByRole('button')).toHaveCount(4)
    await expect(page.getByTestId('chat-ready')).toContainText('qwen3-8b is loaded and ready.')
    // The composer sits under the line, not at the bottom of the page.
    const head = await page.getByTestId('chat-empty').boundingBox()
    const bar = await page.getByTestId('chat-composer').boundingBox()
    expect(bar.y).toBeGreaterThan(head.y)
    expect(bar.y).toBeLessThan(head.y + 200)
  })

  test('a model that is not loaded says the first reply loads it', async ({ page }) => {
    await openChat(page, [chatOf('n1', 'New Chat', 'gemma-3-12b', [])])
    await expect(page.getByTestId('chat-ready')).toContainText('gemma-3-12b is not loaded yet. The first reply loads it.')
  })

  test('a starter fills the box and focuses it', async ({ page }) => {
    await openChat(page, [chatOf('n1', 'New Chat', 'qwen3-8b', [])])
    await page.getByTestId('chat-starters').getByRole('button', { name: 'Help me write code' }).click()
    await expect(page.getByTestId('chat-input')).toHaveValue('Help me write code')
    await expect(page.getByTestId('chat-input')).toBeFocused()
  })

  test('lists the conversations like Home does, and a row resumes one', async ({ page }) => {
    const chats = sampleChats()
    await openChat(page, [chatOf('n1', 'New Chat', 'qwen3-8b', []), ...chats], 'n1')
    const list = page.getByTestId('chat-under')
    await expect(list.getByTestId('home-day-today')).toBeVisible()
    await expect(list.getByTestId('home-day-yesterday')).toBeVisible()
    await expect(list.getByTestId('home-conversation')).toHaveCount(5)
    await list.getByTestId('home-conversation').filter({ hasText: 'Regex for semver' }).getByRole('button').first().click()
    await expect(page.getByTestId('chat-title')).toHaveText('Regex for semver with prerelease tags')
    await expect(page.getByTestId('chat-empty')).toHaveCount(0)
  })

  test('deleting from the list offers an undo', async ({ page }) => {
    await openChat(page, [chatOf('n1', 'New Chat', 'qwen3-8b', []), ...sampleChats()], 'n1')
    const row = page.getByTestId('chat-under').getByTestId('home-conversation').filter({ hasText: 'Draft a polite reply' })
    await row.hover()
    await row.getByRole('button', { name: /Delete conversation/ }).click()
    await expect(page.getByTestId('chat-undo-toast')).toContainText('Deleted "Draft a polite reply about the invoice"')
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('chat-under').getByTestId('home-conversation')).toHaveCount(5)
  })

  test('the first message moves the composer to the bottom without losing the box', async ({ page }) => {
    await answerWith(page, 'hello there')
    await openChat(page, [chatOf('n1', 'New Chat', 'qwen3-8b', [])])
    await page.getByTestId('chat-input').fill('hi')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-role="assistant"]')).toContainText('hello there')
    await expect(page.getByTestId('chat-empty')).toHaveCount(0)
    const bar = await page.getByTestId('chat-composer').boundingBox()
    expect(bar.y + bar.height).toBeGreaterThan(page.viewportSize().height - 80)
  })
})

test.describe('No chat model', () => {
  test('shows an install card, keeps the composer and what was typed, and cannot send', async ({ page }) => {
    await mockChat(page, { models: [], loaded: [] })
    await openChat(page, [chatOf('n1', 'New Chat', '', [])])
    const card = page.getByTestId('chat-no-model')
    await expect(card).toBeVisible()
    await expect(card.getByRole('heading')).toHaveText('Chat needs a language model')
    await expect(card.getByRole('button', { name: 'Browse the gallery' })).toBeVisible()
    await expect(card.getByRole('button', { name: 'Import a model' })).toBeVisible()
    await expect(page.getByTestId('chat-input')).toHaveAttribute('placeholder', 'Install a model to start chatting')
    await expect(page.getByTestId('home-model-chip')).toContainText('No chat model')
    await page.getByTestId('chat-input').fill('what I wanted to ask')
    await expect(page.getByTestId('chat-send')).toBeDisabled()
    await expect(page.getByTestId('chat-input')).toHaveValue('what I wanted to ask')
  })

  test('the gallery button opens the Models page', async ({ page }) => {
    await mockChat(page, { models: [], loaded: [] })
    await openChat(page, [chatOf('n1', 'New Chat', '', [])])
    await page.getByTestId('chat-no-model').getByRole('button', { name: 'Browse the gallery' }).click()
    await expect(page).toHaveURL(/\/app\/models/)
  })
})

test.describe('A reply that waits for a model', () => {
  test('a model that is not loaded gets a plain note, with no invented phases', async ({ page }) => {
    await mockChat(page)
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Cold', 'gemma-3-12b', pair('hi', 'hello'))])
    await page.getByTestId('chat-input').fill('go')
    await page.keyboard.press('Enter')
    const card = page.getByTestId('chat-load')
    await expect(card).toBeVisible()
    await expect(card).toContainText('Loading')
    await expect(card).toContainText('gemma-3-12b')
    await expect(card).toContainText('This model is not loaded yet, so the first reply takes longer.')
    await expect(card.getByTestId('chat-load-phase')).toHaveCount(0)
    await expect(card.getByRole('progressbar')).toHaveCount(0)
    // The first token replaces the card.
    await page.evaluate(() => window.__sse.push({ choices: [{ delta: { content: 'Hello' } }] }))
    await expect(page.getByTestId('chat-load')).toHaveCount(0)
    await expect(page.getByTestId('chat-streaming')).toContainText('Hello')
  })

  test('a loaded model waits behind quiet dots, not a card', async ({ page }) => {
    await mockChat(page)
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Warm', 'qwen3-8b', pair('hi', 'hello'))])
    await page.getByTestId('chat-input').fill('go')
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('chat-streaming').locator('.cx-dots')).toBeVisible()
    await expect(page.getByTestId('chat-load')).toHaveCount(0)
  })

  test('a staging operation shows its progress when no load job names the phase', async ({ page }) => {
    await mockChat(page)
    await page.route('**/api/operations', route => route.fulfill({ json: { operations: [{
      id: 'op1', name: 'gemma-3-12b', taskType: 'staging', nodeName: 'node-2', progress: 55, message: 'file 1 of 2',
    }] } }))
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Stage', 'gemma-3-12b', [])])
    await page.getByTestId('chat-input').fill('go')
    await page.keyboard.press('Enter')
    const card = page.getByTestId('chat-load')
    await expect(card.getByTestId('chat-load-phase')).toContainText('node-2')
    await expect(card.getByTestId('chat-load-pct')).toHaveText('55%')
    await expect(card.getByTestId('chat-load-detail')).toHaveText('file 1 of 2')
  })
})

test.describe('Chat on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('fits the width, keeps the composer in view and keeps message actions visible', async ({ page }) => {
    await mockChat(page)
    await openChat(page, [chatOf('c1', 'Phone', 'qwen3-8b', [...pair('one', 'first'), ...pair('two', 'second')], { contextSize: 8192, tokenUsage: { prompt: 10, completion: 5, total: 15 } })])
    await expect(page.getByTestId('chat-message')).toHaveCount(4)
    const scroll = await page.evaluate(() => ({ w: document.documentElement.scrollWidth, v: window.innerWidth }))
    expect(scroll.w).toBeLessThanOrEqual(scroll.v)
    await expect(page.getByTestId('chat-input')).toBeInViewport()
    // The header drops its words and keeps the buttons.
    await expect(page.locator('.cx-hist__label')).toBeHidden()
    await expect(page.getByTestId('chat-settings-button')).toBeVisible()
    // Actions need no hover on a touch screen.
    const opacity = await page.getByTestId('message-actions').first().evaluate(el => getComputedStyle(el).opacity)
    expect(opacity).toBe('1')
    // On a turn that is not the last one, only the first two buttons stay.
    const visible = await page.getByTestId('message-actions').nth(2).locator('button').evaluateAll(els => els.filter(e => e.offsetParent !== null).length)
    expect(visible).toBe(2)
  })

  test('the conversations menu fits the screen', async ({ page }) => {
    await mockChat(page)
    await openChat(page, sampleChats())
    await page.getByTestId('chats-trigger').click()
    const box = await page.getByTestId('chats-menu').boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeLessThanOrEqual(844)
  })

  test('the empty chat fits too', async ({ page }) => {
    await mockChat(page)
    await openChat(page, [chatOf('n1', 'New Chat', 'qwen3-8b', [])])
    await expect(page.getByTestId('chat-empty')).toBeVisible()
    const scroll = await page.evaluate(() => ({ w: document.documentElement.scrollWidth, v: window.innerWidth }))
    expect(scroll.w).toBeLessThanOrEqual(scroll.v)
    await expect(page.getByTestId('chat-composer')).toBeInViewport()
  })
})

test.describe('Chat with reduced motion', () => {
  test('nothing in the thread animates', async ({ page }) => {
    // The fixture option test.use({ reducedMotion }) does not reach our extended page.
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockChat(page)
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Calm', 'qwen3-8b', pair('hi', 'hello'))])
    const msgAnim = await page.getByTestId('chat-message').first().evaluate(el => getComputedStyle(el).animationName)
    expect(msgAnim).toBe('none')
    await page.getByTestId('chat-input').fill('go')
    await page.keyboard.press('Enter')
    await page.evaluate(() => window.__sse.push({ choices: [{ delta: { reasoning: 'thinking it over' } }] }))
    const shimmer = await page.getByTestId('chat-activity').locator('.cx-shimmer, .cx-fold__head span').first().evaluate(el => getComputedStyle(el).animationName)
    expect(shimmer).toBe('none')
    expect((await storedChats(page)).chats).toHaveLength(1)
  })
})
