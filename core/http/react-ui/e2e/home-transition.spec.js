import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// Send on Home opens the new chat as one move: the composer travels to the
// dock, the message is in the thread from the first frame of the chat page,
// and Home is gone by then (hooks/useComposerTransition.js). A probe in the
// page records every animation frame, so the specs can say what was never on
// screen: the empty chat, the route's loading placeholder, Home and the
// message together, the message twice.

const CONV = { id: 'n1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z', running: false }
const sleep = (ms) => new Promise(r => setTimeout(r, ms))

// The chat API, a little slow, as a model host is: the create, the send and
// the reads of the chat each take a moment.
async function server(page, { failSend = false, text = 'What changed in the 3.4 release?', attachments = [] } = {}) {
  const seen = { created: 0, sent: 0, deleted: 0, reads: 0 }
  let sent = false
  await page.route(/\/api\/chats(\?.*)?$/, async route => {
    if (route.request().method() === 'POST') {
      seen.created++
      await sleep(150)
      return route.fulfill({ status: 201, json: CONV })
    }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, async route => {
    if (route.request().method() === 'POST') {
      seen.sent++
      await sleep(150)
      if (failSend) return route.fulfill({ status: 500, json: { error: { message: 'the model is not available' } } })
      sent = true
      return route.fulfill({ json: { started: true } })
    }
    seen.reads++
    await sleep(300)
    return route.fulfill({ json: { messages: sent ? [{ seq: 1, turn: 1, role: 'user', text, attachments, at: '2026-10-30T10:00:01Z' }] : [] } })
  })
  await page.route('**/api/chats/n1/events', route => route.abort())
  await page.route('**/api/chats/n1', async route => {
    if (route.request().method() === 'DELETE') { seen.deleted++; return route.fulfill({ json: {} }) }
    await sleep(300)
    return route.fulfill({ json: { ...CONV, running: sent, pending: [] } })
  })
  await page.route(/\/api\/chats\/draft\/tools(\?.*)?$/, route => route.fulfill({ json: { builtins: [], addons: [], servers: [], connectors: [], off: [] } }))
  await page.route(/\/api\/chats\/draft\/addons(\?.*)?$/, route => route.fulfill({ json: { addons: [], commands: 'none' } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [] } }))
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents: [] } }))
  return seen
}

// One sample per animation frame from Send on.
function probe() {
  window.__frames = []
  const shown = (el) => {
    if (!el) return false
    const r = el.getBoundingClientRect()
    if (r.width === 0 || r.height === 0) return false
    for (let e = el; e; e = e.parentElement) if (Number(getComputedStyle(e).opacity) === 0) return false
    return true
  }
  const tick = () => {
    if (window.__probeOn) {
      window.__frames.push({
        path: location.pathname,
        home: shown(document.querySelector('.home-greeting')),
        empty: shown(document.querySelector('[data-testid="chat-empty"]')),
        placeholder: !!document.querySelector('.route-fallback'),
        chat: !!document.querySelector('[data-testid="chats-page"]'),
        users: document.querySelectorAll('[data-testid="chat-message"][data-role="user"]').length,
        handoff: document.documentElement.dataset.handoff || '',
      })
    }
    requestAnimationFrame(tick)
  }
  requestAnimationFrame(tick)
}

async function ready(page, { noViewTransitions = false } = {}) {
  await page.addInitScript(probe)
  if (noViewTransitions) await page.addInitScript(() => { delete Document.prototype.startViewTransition })
  await page.goto('/app')
  await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct', { timeout: 15_000 })
}

async function send(page, text) {
  await page.getByTestId('home-input').fill(text)
  await page.evaluate(() => { window.__probeOn = true })
  await page.getByTestId('home-input').press('Enter')
}

// What every frame of a hand-off must hold, whatever the motion mode.
async function expectOneMove(page, text) {
  await expect(page).toHaveURL(/\/app\/chats\/n1$/)
  const bubble = page.getByTestId('chat-message').filter({ hasText: text })
  await expect(bubble).toHaveCount(1)
  // The row drawn from the hand-off is the one that stays: mark it, wait for
  // the server's transcript, and it is still the same element.
  await bubble.evaluate(el => { el.__first = true })
  await expect(page.locator('html')).not.toHaveAttribute('data-handoff', /.+/)
  await page.waitForTimeout(900)
  await expect(bubble).toHaveCount(1)
  expect(await bubble.evaluate(el => el.__first === true)).toBe(true)
  // Docked and focused, ready for the next message.
  await expect(page.getByTestId('chat-input')).toBeFocused()
  await expect(page.getByTestId('chat-input')).toHaveValue('')
  await expect(page.getByTestId('chat-composer')).toBeInViewport()
  await expect(page.locator('.home-page')).toHaveCount(0)

  const frames = await page.evaluate(() => window.__frames)
  const onChat = frames.filter(f => f.path.startsWith('/app/chats/'))
  expect(onChat.length).toBeGreaterThan(0)
  expect(frames.filter(f => f.empty), 'the empty chat was never on screen').toEqual([])
  expect(frames.filter(f => f.placeholder), 'the loading placeholder was never on screen').toEqual([])
  expect(frames.filter(f => f.home && f.users > 0), 'Home and the message were never on screen together').toEqual([])
  expect(frames.filter(f => f.users > 1), 'the message was never drawn twice').toEqual([])
  // Once the chat page is there, the message is in it in every frame.
  expect(frames.filter(f => f.chat && f.users !== 1)).toEqual([])
  return frames
}

test.describe('Home to chat', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('the composer glides into the chat and the message is there from the first frame', async ({ page }) => {
    await server(page)
    await ready(page)
    const text = 'What changed in the 3.4 release?'
    await send(page, text)
    const frames = await expectOneMove(page, text)
    expect(frames.some(f => f.handoff === 'glide')).toBe(true)
    // The model is working: its indicator follows the message.
    await expect(page.getByTestId('chat-streaming')).toBeVisible()
  })

  test('without view transitions the composer and the message slide in from Home', async ({ page }) => {
    const text = 'Draft the release post'
    await server(page, { text })
    await ready(page, { noViewTransitions: true })
    await page.getByTestId('home-input').fill(text)
    // Catch the slide on the dock's composer as it starts.
    await page.evaluate(() => {
      window.__slid = false
      const check = () => {
        const card = document.querySelector('.cx-dock form.home-cmd')
        if (card && card.getAnimations().length > 0) { window.__slid = true; return }
        requestAnimationFrame(check)
      }
      requestAnimationFrame(check)
      window.__probeOn = true
    })
    await page.getByTestId('home-input').press('Enter')
    const frames = await expectOneMove(page, text)
    expect(frames.some(f => f.handoff === 'flip')).toBe(true)
    expect(await page.evaluate(() => window.__slid)).toBe(true)
  })

  test('the files travel with the message and show once', async ({ page }) => {
    await server(page, { text: 'What is in this file?', attachments: [{ path: '/Chats/n1/notes.txt', name: 'notes.txt', size: 17, mime: 'text/plain' }] })
    await ready(page)
    await page.getByTestId('chat-file-input').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('remember the milk') })
    await send(page, 'What is in this file?')
    await expectOneMove(page, 'What is in this file?')
    const message = page.getByTestId('chat-message').filter({ hasText: 'What is in this file?' })
    await expect(message.getByTestId('attachment')).toHaveCount(1)
    await expect(message.getByTestId('attachment')).toContainText('notes.txt')
    // The server's copy has a path: the chip can be downloaded now.
    await expect(message.getByTestId('attachment').locator('a')).toHaveCount(1)
  })

  test('a message that cannot go stays on Home, with Home back and no stuck overlay', async ({ page }) => {
    const seen = await server(page, { failSend: true })
    await ready(page)
    await send(page, 'This will not go')
    await expect(page.getByText('the model is not available')).toBeVisible()
    await expect(page).toHaveURL(/\/app$/)
    await expect(page.getByTestId('home-input')).toHaveValue('This will not go')
    await expect(page.getByTestId('home-input')).toBeFocused()
    await expect(page.locator('.home-greeting')).toBeVisible()
    await expect(page.locator('.home-page')).not.toHaveAttribute('data-depart', /.+/)
    await expect(page.locator('html')).not.toHaveAttribute('data-handoff', /.+/)
    await expect.poll(() => seen.deleted).toBe(1)
    const frames = await page.evaluate(() => window.__frames)
    expect(frames.some(f => f.path !== '/app')).toBe(false)
    // It can be sent again.
    await expect(page.getByTestId('home-send')).toBeEnabled()
  })

  test('a second Enter while the first goes sends nothing more', async ({ page }) => {
    const seen = await server(page)
    await ready(page)
    await page.getByTestId('home-input').fill('Only once')
    await page.getByTestId('home-input').press('Enter')
    await page.getByTestId('home-input').press('Enter').catch(() => {})
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await page.keyboard.press('Enter')
    await page.waitForTimeout(500)
    expect(seen.created).toBe(1)
    expect(seen.sent).toBe(1)
  })
})

test.describe('Home to chat, less motion', () => {
  test('it is a cross-fade, with the same guarantees', async ({ page }) => {
    // test.use({ reducedMotion }) does not reach the extended page fixture.
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockHome(page)
    const text = 'Short answers, please'
    await server(page, { text })
    await ready(page)
    await send(page, text)
    const frames = await expectOneMove(page, text)
    expect(frames.some(f => f.handoff === 'glide')).toBe(false)
    // Home does not fade away while the message goes.
    const leaving = frames.filter(f => f.path === '/app')
    expect(leaving.length).toBeGreaterThan(0)
    expect(leaving.every(f => f.home)).toBe(true)
  })
})
