import { mkdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// Live from Home: the button creates the chat the way Send does (without a
// message), opens it, and Live starts there. The API is stubbed; the browser
// runs with a fake microphone, as in chat-live.spec.js.

const AT = '2026-11-07T10:00:00Z'
const CONV = { id: 'n1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: AT, updated: AT, running: false }

function wav(seconds, rate, sample) {
  const n = Math.round(seconds * rate)
  const buf = Buffer.alloc(44 + n * 2)
  buf.write('RIFF', 0); buf.writeUInt32LE(36 + n * 2, 4); buf.write('WAVE', 8)
  buf.write('fmt ', 12); buf.writeUInt32LE(16, 16); buf.writeUInt16LE(1, 20); buf.writeUInt16LE(1, 22)
  buf.writeUInt32LE(rate, 24); buf.writeUInt32LE(rate * 2, 28); buf.writeUInt16LE(2, 32); buf.writeUInt16LE(16, 34)
  buf.write('data', 36); buf.writeUInt32LE(n * 2, 40)
  for (let i = 0; i < n; i++) buf.writeInt16LE(Math.round(sample(i / rate) * 32767), 44 + i * 2)
  return buf
}

// A long quiet stretch first, so a test can look at the Listening state
// before any speech is heard.
const micDir = join(tmpdir(), 'localai-live-home-e2e')
mkdirSync(micDir, { recursive: true })
const micFile = join(micDir, 'quiet.wav')
writeFileSync(micFile, wav(30, 16000, () => 0))

test.use({
  launchOptions: {
    args: ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream', `--use-file-for-fake-audio-capture=${micFile}`],
    ...(process.env.PLAYWRIGHT_CHROMIUM_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } : {}),
  },
})

const WITH_SPEECH = [
  { id: 'qwen3-8b-instruct', capabilities: ['FLAG_CHAT'] },
  { id: 'whisper-base', capabilities: ['FLAG_TRANSCRIPT'] },
  { id: 'kokoro', capabilities: ['FLAG_TTS'] },
]
const CHAT_ONLY = [WITH_SPEECH[0]]

async function server(page, { models = WITH_SPEECH, agents = [], failCreate = false, delayCreate = 0 } = {}) {
  const seen = { created: [], sent: 0, updates: [] }
  await page.addInitScript(() => {
    window.__mic = 0
    const get = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices)
    navigator.mediaDevices.getUserMedia = (...a) => { window.__mic++; return get(...a) }
  })
  await mockHome(page, { chatModels: ['qwen3-8b-instruct'] })
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models } }))
  await page.route(/\/api\/chats(\?.*)?$/, async (route) => {
    if (route.request().method() === 'POST') {
      seen.created.push(route.request().postDataJSON())
      if (delayCreate) await new Promise(r => setTimeout(r, delayCreate))
      if (failCreate) return route.fulfill({ status: 500, json: { error: { message: 'the server could not create the chat' } } })
      return route.fulfill({ status: 201, json: { ...CONV, model: seen.created.at(-1).model || CONV.model } })
    }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, (route) => {
    if (route.request().method() === 'POST') { seen.sent++; return route.fulfill({ json: { started: true } }) }
    return route.fulfill({ json: { messages: [] } })
  })
  await page.route('**/api/chats/n1/events', route => route.abort())
  await page.route('**/api/chats/n1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: [] } }))
  await page.route('**/api/chats/n1/addons', route => route.fulfill({ json: { addons: [] } }))
  await page.route('**/api/chats/n1', (route) => {
    if (route.request().method() === 'PATCH') { seen.updates.push(route.request().postDataJSON()); return route.fulfill({ json: {} }) }
    return route.fulfill({ json: { ...CONV, pending: [] } })
  })
  await page.route(/\/api\/chats\/draft\/tools(\?.*)?$/, route => route.fulfill({ json: {
    builtins: [{ group: 'web', tools: ['web_search'], off: false, usable: true, provider: 'builtin', providers: {} }],
    addons: [{ id: 'office', tools: 4, off: false }], servers: [], connectors: [], off: [],
  } }))
  await page.route(/\/api\/chats\/draft\/addons(\?.*)?$/, route => route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }], commands: 'none' } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [] } }))
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents } }))
  await page.route('**/v1/audio/transcriptions', route => route.fulfill({ json: { text: '' } }))
  await page.route('**/v1/audio/speech', route => route.fulfill({ status: 200, contentType: 'audio/wav', body: wav(0.5, 8000, () => 0) }))
  return seen
}

async function open(page) {
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
}

// Two painted frames: what a handler did in response to an event has run.
// Bounded, unlike a fixed sleep, and used before a "nothing happened" check.
const settle = (page) => page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))))
const live = (page) => page.getByTestId('live-button')
const state = (page) => page.getByTestId('live-state')

test.describe('Live on Home', () => {
  test('Live is offered on Home next to the attach buttons', async ({ page }) => {
    await server(page)
    await open(page)
    await expect(page.locator('.home-attach-buttons').getByTestId('live-button')).toBeEnabled()
    await expect(live(page)).toHaveAccessibleName('Live')
  })

  test('pressing Live creates one chat with the Home model and the setup switches and opens it with Live listening', async ({ page }) => {
    const seen = await server(page)
    await open(page)
    // A switch changed on Home goes with the new chat, as it does with Send.
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-tools-menu').getByRole('switch', { name: 'Look things up online', exact: true }).click()
    await page.keyboard.press('Escape')
    await live(page).click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect(page.getByTestId('live-dock')).toBeVisible()
    await expect(state(page)).toContainText('Listening')
    expect(seen.created).toHaveLength(1)
    expect(seen.created[0].model).toBe('qwen3-8b-instruct')
    expect(seen.created[0].switches).toEqual({ builtins_off: ['web'] })
    expect(seen.sent).toBe(0)
  })

  test('the create body of Live equals the one of Send', async ({ page }) => {
    const seen = await server(page)
    await open(page)
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-tools-menu').getByRole('switch', { name: 'Look things up online', exact: true }).click()
    await page.keyboard.press('Escape')
    await live(page).click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    const viaLive = seen.created[0]
    await page.goto('/app')
    await expect(page.locator('.home-greeting')).toBeVisible()
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-tools-menu').getByRole('switch', { name: 'Look things up online', exact: true }).click()
    await page.keyboard.press('Escape')
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.created).toHaveLength(2)
    expect(viaLive).toEqual(seen.created[1])
  })

  test('with no speech models the chat opens with the setup sheet', async ({ page }) => {
    const seen = await server(page, { models: CHAT_ONLY })
    await open(page)
    await live(page).click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect(page.getByTestId('live-setup')).toBeVisible()
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    expect(seen.created).toHaveLength(1)
    expect(await page.evaluate(() => window.__mic)).toBe(0)
  })

  test('an insecure page disables the button with a reason and creates nothing', async ({ page }) => {
    const seen = await server(page)
    await page.addInitScript(() => Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true }))
    await open(page)
    await expect(live(page)).toBeDisabled()
    await expect(page.getByTestId('live-reason')).toContainText('Needs HTTPS or localhost')
    await live(page).dispatchEvent('click')
    await settle(page)
    expect(seen.created).toHaveLength(0)
    await expect(page).toHaveURL(/\/app\/?$/)
  })

  test('a page without a microphone disables the button with a reason', async ({ page }) => {
    const seen = await server(page)
    await page.addInitScript(() => { navigator.mediaDevices.getUserMedia = undefined })
    await open(page)
    await expect(live(page)).toBeDisabled()
    await expect(page.getByTestId('live-reason')).toContainText('No microphone available')
    expect(seen.created).toHaveLength(0)
  })

  test('a failed create keeps Home and shows the error', async ({ page }) => {
    const seen = await server(page, { failCreate: true })
    await open(page)
    await live(page).click()
    await expect(page.getByText('the server could not create the chat')).toBeVisible()
    await expect(page).toHaveURL(/\/app\/?$/)
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    // Home works again: the button is back and a second try goes out.
    await expect(live(page)).toBeEnabled()
    expect(seen.created).toHaveLength(1)
  })

  test('double press creates one chat', async ({ page }) => {
    const seen = await server(page, { delayCreate: 400 })
    await open(page)
    await live(page).dblclick()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await settle(page)
    expect(seen.created).toHaveLength(1)
  })

  // The second action is sent as a real DOM event, so it reaches the guard
  // even while Home fades; nothing is forced or swallowed.
  test('Live then Send creates one chat and sends nothing', async ({ page }) => {
    const seen = await server(page, { delayCreate: 400 })
    await open(page)
    await page.getByTestId('home-input').fill('hello')
    await live(page).click()
    await page.getByTestId('home-input').press('Enter')
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect(state(page)).toContainText('Listening')
    await settle(page)
    expect(seen.created).toHaveLength(1)
    expect(seen.sent).toBe(0)
  })

  test('Send then Live creates one chat, sends the message and does not start Live', async ({ page }) => {
    const seen = await server(page, { delayCreate: 400 })
    await open(page)
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await live(page).dispatchEvent('click')
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.sent).toBe(1)
    await settle(page)
    expect(seen.created).toHaveLength(1)
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    expect(await page.evaluate(() => window.__mic)).toBe(0)
  })

  test('Back and reload do not restart Live', async ({ page }) => {
    await server(page)
    await open(page)
    await live(page).click()
    await expect(state(page)).toContainText('Listening')
    expect(await page.evaluate(() => window.__mic)).toBe(1)
    // The models are read again on reload: Live would start after that answer.
    const caps = page.waitForResponse('**/api/models/capabilities')
    await page.reload()
    await caps
    await expect(live(page)).toBeVisible()
    await settle(page)
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    expect(await page.evaluate(() => window.__mic)).toBe(0)
    // Back to Home and forward to the chat: still off.
    await page.goBack()
    await expect(page.locator('.home-greeting')).toBeVisible()
    const again = page.waitForResponse('**/api/models/capabilities')
    await page.goForward()
    await again
    await expect(live(page)).toBeVisible()
    await settle(page)
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
  })

  test('a second hand-over in the same page session starts Live again', async ({ page }) => {
    const seen = await server(page)
    await open(page)
    await live(page).click()
    await expect(state(page)).toContainText('Listening')
    // Back is a client navigation: the page is not reloaded.
    await page.goBack()
    await expect(page.locator('.home-greeting')).toBeVisible()
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    await live(page).click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect(state(page)).toContainText('Listening')
    expect(await page.evaluate(() => window.__mic)).toBe(2)
    expect(seen.created).toHaveLength(2)
  })

  test('a denied microphone on autostart shows the error, Retry and Type instead', async ({ page }) => {
    const seen = await server(page)
    await page.addInitScript(() => {
      navigator.mediaDevices.getUserMedia = async () => { const e = new Error('denied'); e.name = 'NotAllowedError'; throw e }
    })
    await open(page)
    await live(page).click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect(page.getByTestId('live-error')).toContainText('Microphone access was blocked')
    await expect(page.getByTestId('live-retry')).toBeVisible()
    await page.getByTestId('live-type').click()
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    await expect(page.getByTestId('chat-input')).toBeFocused()
    await page.getByTestId('chat-input').fill('typed instead')
    await page.getByTestId('chat-send').click()
    await expect.poll(() => seen.sent).toBe(1)
    expect(seen.created).toHaveLength(1)
  })

  test('Live from Home applies the picked setup and agent as Send does', async ({ page }) => {
    const seen = await server(page, { agents: [{ id: 'a1', name: 'Reviewer', model: '' }] })
    await open(page)
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    await page.getByRole('radio', { name: 'Reviewer' }).click()
    await page.getByTestId('chat-effort-button').click()
    await page.getByTestId('chat-effort-low').click()
    await live(page).click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.created).toHaveLength(1)
    expect(seen.created[0].agent_id).toBe('a1')
    expect(seen.updates).toHaveLength(1)
    expect(JSON.stringify(seen.updates[0])).toContain('low')
  })
})
