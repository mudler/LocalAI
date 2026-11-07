import { mkdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { MEMBER, sessionFor } from './addons-fixtures.js'

// Live: talk to an open chat hands-free. The API is stubbed and the event
// stream faked, so each moment of a turn can be held still. The browser runs
// with a fake microphone that plays a generated file in a loop: a second of
// quiet, a tone that the voice detector takes for speech, then quiet again.

const AT = '2026-11-07T10:00:00Z'
const CHAT = { id: 'n1', title: 'Talk', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, pending: [] }

// 16-bit mono PCM WAV from a sample function of the time in seconds.
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

// Quiet 1.0 s, tone 1.2 s, quiet 3.0 s: one utterance every 5.2 s.
const micDir = join(tmpdir(), 'localai-live-e2e')
mkdirSync(micDir, { recursive: true })
const micFile = join(micDir, 'speech-loop.wav')
writeFileSync(micFile, wav(5.2, 48000, (t) => (t >= 1.0 && t < 2.2 ? 0.4 * Math.sin(2 * Math.PI * 440 * t) : 0)))

test.use({
  launchOptions: {
    args: ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream', `--use-file-for-fake-audio-capture=${micFile}`],
    ...(process.env.PLAYWRIGHT_CHROMIUM_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } : {}),
  },
})

const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'] },
  { id: 'whisper-base', capabilities: ['FLAG_TRANSCRIPT'] },
  { id: 'kokoro', capabilities: ['FLAG_TTS'] },
]

const state = (page) => page.getByTestId('live-state')
const emit = (page, kind, payload) => page.evaluate(([k, p]) => window.__emit(k, p), [kind, payload])

// Opens chat n1. `speechSeconds` is the length of the voice the speech stub
// returns. Returns handles on what the page asked for.
async function open(page, { models = MODELS, speechSeconds = 0.6, transcript = { text: 'what time is it' }, transcribeStatus = 200, denyMic = false, insecure = false, noMic = false, speechStatus = 200, running = false, path = '/app/chats/n1', before = null } = {}) {
  await page.addInitScript(({ denyMic, insecure, noMic }) => {
    window.__sources = []
    window.EventSource = class {
      constructor(url) { this.url = url; this.l = {}; window.__sources.push(this); setTimeout(() => this.onopen && this.onopen(), 0) }
      addEventListener(kind, fn) { (this.l[kind] = this.l[kind] || []).push(fn) }
      close() { this.closed = true }
    }
    window.__emit = (kind, payload) => {
      const src = window.__sources.filter((x) => x.url.includes('/api/chats/n1/events') && !x.closed).pop()
      for (const fn of (src && src.l[kind]) || []) fn({ data: JSON.stringify(payload) })
    }
    if (denyMic) {
      navigator.mediaDevices.getUserMedia = async () => { const e = new Error('denied'); e.name = 'NotAllowedError'; throw e }
    }
    if (noMic) navigator.mediaDevices.getUserMedia = undefined
    if (insecure) Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    // The next play() the page asks for is refused, as a browser does when the
    // page has no user gesture.
    const play = HTMLMediaElement.prototype.play
    HTMLMediaElement.prototype.play = function (...args) {
      if (window.__blockPlay) {
        window.__blockPlay = false
        return Promise.reject(Object.assign(new Error('blocked'), { name: 'NotAllowedError' }))
      }
      return play.apply(this, args)
    }
  }, { denyMic, insecure, noMic })
  await mockChat(page)
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models } }))

  const chat = { running, messages: [], posts: [], stops: 0, transcriptions: [], speech: 0 }
  const detail = () => ({ ...CHAT, running: chat.running, last_turn: chat.running ? '' : 'done' })
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      chat.posts.push(body.text)
      chat.messages.push({ seq: chat.messages.length + 1, turn: 1, role: 'user', text: body.text, at: AT })
      chat.running = true
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: chat.messages } })
  })
  await page.route('**/api/chats/n1/stop', (route) => {
    chat.stops++
    chat.running = false
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/chats/n1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: [] } }))
  await page.route('**/api/chats/n1/addons', route => route.fulfill({ json: { addons: [] } }))
  await page.route('**/api/chats/n1', route => route.fulfill({ json: detail() }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail(), pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route('**/v1/audio/transcriptions', async (route) => {
    chat.transcriptions.push(route.request().postDataBuffer())
    // A test can hold the answer back by setting chat.gate to a promise.
    if (chat.gate) await chat.gate
    return route.fulfill({ status: transcribeStatus, json: transcribeStatus === 200 ? transcript : { error: { message: 'speech model is not loaded' } } })
  })
  await page.route('**/v1/audio/speech', (route) => {
    chat.speech++
    if (speechStatus !== 200) return route.fulfill({ status: speechStatus, json: { error: { message: 'speech model is busy' } } })
    return route.fulfill({ status: 200, contentType: 'audio/wav', body: wav(speechSeconds, 8000, () => 0) })
  })

  // The assistant answers: the turn ends and its text is in the transcript.
  chat.reply = async (text) => {
    chat.messages.push({ seq: chat.messages.length + 1, turn: 1, role: 'assistant', text, at: AT })
    chat.running = false
    await emit(page, 'turn_end', { kind: 'turn_end' })
  }
  // The turn fails and has no reply.
  chat.fail = async () => {
    chat.running = false
    await emit(page, 'error', { kind: 'error', text: 'The model failed.', code: 'upstream' })
    await emit(page, 'turn_end', { kind: 'turn_end' })
  }
  if (before) await before(page)
  await page.goto(path)
  return chat
}

const start = async (page) => {
  await page.getByTestId('live-button').click()
  await expect(page.getByTestId('live-dock')).toBeVisible()
}

test.describe('Live', () => {
  test('Live is offered in an open chat', async ({ page }) => {
    await open(page)
    await expect(page.getByTestId('live-button')).toBeEnabled()
    await expect(page.getByTestId('live-button')).toHaveAccessibleName('Live')
  })

  // The setup sheet: the models Live needs, installed from the sheet. The
  // capability list is read from `api.listed`, which a test fills after the
  // install, so the page sees the new model on its next read.
  const STT = { id: 'whisper-base', capabilities: ['FLAG_TRANSCRIPT'] }
  const TTS = { id: 'kokoro-multi-lang-v1.0-sherpa', capabilities: ['FLAG_TTS'] }
  async function setup(page, { models = [MODELS[0]], failInstall = false, ops = [], member = false } = {}) {
    const api = { listed: [...models], ops: [...ops], installs: [], failInstall }
    const chat = await open(page, {
      models,
      before: async (p) => {
        if (member) await p.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { chats: true }) }))
        await p.route('**/api/models/capabilities', route => route.fulfill({ json: { data: api.listed } }))
        await p.route('**/api/operations', route => route.fulfill({ json: { operations: api.ops } }))
        await p.route('**/api/operations/history', route => route.fulfill({ json: { operations: [] } }))
        await p.route('**/api/models/install/**', (route) => {
          const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
          api.installs.push(id)
          if (api.failInstall) return route.fulfill({ status: 500, json: { error: { message: 'the gallery is unreachable' } } })
          return route.fulfill({ json: { uuid: 'job-' + id, status: '/api/operations/job-' + id } })
        })
      },
    })
    return { chat, api }
  }
  const sheet = (page) => page.getByTestId('live-setup')
  // LIVE_SHOTS=<dir> saves a picture of the sheet in each state, in the theme
  // of LIVE_SCHEME (dark or light), for a design review.
  const shoot = async (page, name) => {
    if (!process.env.LIVE_SHOTS) return
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.evaluate((scheme) => document.documentElement.setAttribute('data-theme', scheme), process.env.LIVE_SCHEME || 'dark')
    await page.waitForTimeout(400)
    await page.screenshot({ path: `${process.env.LIVE_SHOTS}/${process.env.LIVE_SCHEME || 'dark'}-setup-${name}.png` })
  }

  test('without speech models the button opens the setup sheet and Install calls the install API', async ({ page }) => {
    const { chat, api } = await setup(page)
    await page.getByTestId('live-button').click()
    await expect(sheet(page)).toBeVisible()
    await expect(sheet(page)).toContainText('whisper-base')
    await expect(sheet(page)).toContainText('kokoro-multi-lang-v1.0-sherpa')
    // Focus moves into the sheet.
    await expect(sheet(page).locator(':focus')).toHaveCount(1)
    await expect(page.getByTestId('live-start')).toHaveCount(0)
    await shoot(page, '1-missing')
    await page.getByTestId('live-setup-install-stt').click()
    await expect.poll(() => api.installs).toEqual(['whisper-base'])
    // Progress from the operations list shows with a labelled bar and a live region.
    api.ops = [{ id: 'whisper-base', name: 'whisper-base', jobID: 'job-1', progress: 40, taskType: 'installation', isBackend: false, isDeletion: false }]
    await expect(sheet(page).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
    await shoot(page, '2-installing')
    await expect(sheet(page).locator('[aria-live="polite"]')).toContainText('whisper-base')
    expect(chat.posts).toHaveLength(0)
    // The sheet closes and focus returns to the Live button.
    await page.keyboard.press('Escape')
    await expect(sheet(page)).toHaveCount(0)
    await expect(page.getByTestId('live-button')).toBeFocused()
  })

  test('an install running from elsewhere shows its progress', async ({ page }) => {
    await setup(page, { ops: [{ id: 'kokoro-multi-lang-v1.0-sherpa', name: 'kokoro-multi-lang-v1.0-sherpa', jobID: 'j2', progress: 25, taskType: 'installation', isBackend: false, isDeletion: false }] })
    await page.getByTestId('live-button').click()
    await expect(sheet(page).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
    await expect(page.getByTestId('live-setup-install-tts')).toBeDisabled()
    await expect(page.getByTestId('live-setup-install-stt')).toBeEnabled()
  })

  test('a non-admin sees the names and the administrator note, no Install button', async ({ page }) => {
    await setup(page, { member: true })
    await page.getByTestId('live-button').click()
    await expect(sheet(page)).toContainText('whisper-base')
    await expect(sheet(page)).toContainText('kokoro-multi-lang-v1.0-sherpa')
    await expect(sheet(page)).toContainText('An administrator must install')
    await shoot(page, '4-non-admin')
    await expect(sheet(page).getByRole('button', { name: /Install/ })).toHaveCount(0)
    await expect(page.getByTestId('live-start')).toHaveCount(0)
  })

  test('a failed install shows an error and Retry', async ({ page }) => {
    const { api } = await setup(page, { failInstall: true })
    await page.getByTestId('live-button').click()
    await page.getByTestId('live-setup-install-stt').click()
    await expect(sheet(page).getByRole('alert')).toContainText('the gallery is unreachable')
    await shoot(page, '5-failed')
    api.failInstall = false
    await page.getByTestId('live-setup-retry-stt').click()
    await expect.poll(() => api.installs).toEqual(['whisper-base', 'whisper-base'])
    await expect(sheet(page).getByRole('alert')).toHaveCount(0)
  })

  test('an install that fails on the server shows the error and Retry', async ({ page }) => {
    await setup(page, { ops: [{ id: 'whisper-base', name: 'whisper-base', jobID: 'j3', progress: 10, error: 'download failed', taskType: 'installation', isBackend: false, isDeletion: false }] })
    await page.getByTestId('live-button').click()
    await expect(sheet(page).getByRole('alert')).toContainText('download failed')
    await expect(page.getByTestId('live-setup-retry-stt')).toBeVisible()
  })

  test('closing the sheet during an install and reopening it after the model landed shows Installed, with no second install', async ({ page }) => {
    const { api } = await setup(page)
    await page.getByTestId('live-button').click()
    await page.getByTestId('live-setup-install-stt').click()
    await expect.poll(() => api.installs).toEqual(['whisper-base'])
    await page.keyboard.press('Escape')
    await expect(sheet(page)).toHaveCount(0)
    api.listed = [MODELS[0], STT]
    await page.getByTestId('live-button').click()
    await expect(sheet(page).getByTestId('live-setup-done-stt')).toBeVisible()
    await expect(page.getByTestId('live-setup-install-stt')).toHaveCount(0)
    expect(api.installs).toEqual(['whisper-base'])
  })

  test('an install that never shows up ends with an error and Retry', async ({ page }) => {
    test.setTimeout(45_000)
    const { api } = await setup(page)
    await page.getByTestId('live-button').click()
    await page.getByTestId('live-setup-install-stt').click()
    // No operation is listed and the model never appears.
    await expect(page.getByTestId('live-setup-install-stt')).toBeDisabled()
    await expect(sheet(page).getByRole('alert')).toContainText('did not show up', { timeout: 20_000 })
    await page.getByTestId('live-setup-retry-stt').click()
    await expect.poll(() => api.installs).toEqual(['whisper-base', 'whisper-base'])
    await expect(page.getByTestId('live-setup-install-stt')).toBeDisabled()
  })

  test('after both are installed Start Live starts Live', async ({ page }) => {
    const { api } = await setup(page)
    await page.getByTestId('live-button').click()
    await page.getByTestId('live-setup-install-stt').click()
    await page.getByTestId('live-setup-install-tts').click()
    await expect.poll(() => api.installs.length).toBe(2)
    await expect(page.getByTestId('live-start')).toHaveCount(0)
    // The models appear in the list; the sheet reads it again with no reload.
    api.listed = [MODELS[0], STT, TTS]
    await expect(sheet(page).getByTestId('live-setup-done-stt')).toBeVisible({ timeout: 10_000 })
    await expect(sheet(page).getByTestId('live-setup-done-tts')).toBeVisible()
    await shoot(page, '3-ready')
    await page.getByTestId('live-start').click()
    await expect(page.getByTestId('live-dock')).toBeVisible()
    await expect(sheet(page)).toHaveCount(0)
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
  })

  test('with models installed Live starts, shows Listening and opens the microphone', async ({ page }) => {
    await open(page)
    await expect(page.getByTestId('chat-dictate')).toBeVisible()
    await start(page)
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
    await expect(state(page)).toContainText('Listening')
    await expect(page.getByTestId('live-dock')).toContainText('Standard')
    // Dictation is hidden while Live is on, and the button is replaced by the dock.
    await expect(page.getByTestId('chat-dictate')).toHaveCount(0)
    await expect(page.getByTestId('live-button')).toHaveCount(0)
    await expect(page.getByTestId('chat-input')).toHaveCount(0)
    // The state line is a polite live region.
    await expect(page.getByTestId('live-dock').locator('[aria-live="polite"]')).toContainText('Listening')
  })

  test('an utterance from the fake microphone is transcribed, sent as a chat message and the reply is spoken, then it listens again', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    // The clip is a real WAV the microphone captured: the mic opened with the fake device.
    expect(chat.transcriptions).toHaveLength(1)
    expect(chat.transcriptions[0].length).toBeGreaterThan(20_000)
    expect(chat.transcriptions[0].includes(Buffer.from('RIFF'))).toBe(true)
    expect(chat.posts[0]).toBe('what time is it')
    await expect(state(page)).toHaveAttribute('data-state', 'thinking')
    await chat.reply('It is ten past ten.')
    await expect(state(page)).toHaveAttribute('data-state', 'speaking')
    expect(chat.speech).toBeGreaterThan(0)
    await expect(state(page)).toHaveAttribute('data-state', 'listening', { timeout: 10_000 })
  })

  test('the dock stays on Speaking until the audio ends and the mic does not send during it', async ({ page }) => {
    test.setTimeout(60_000)
    const chat = await open(page, { speechSeconds: 8 })
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await chat.reply('A long answer.')
    await expect(state(page)).toHaveAttribute('data-state', 'speaking')
    // The loop plays a tone every 5.2 s; the voice runs 8 s, so one tone falls
    // inside it. Watch the transcription count for 6.5 s of the voice.
    const until = Date.now() + 6500
    let sentMore = false
    await expect.poll(() => {
      if (chat.transcriptions.length !== 1) sentMore = true
      return Date.now() >= until
    }, { intervals: [100], timeout: 10_000 }).toBe(true)
    expect(sentMore).toBe(false)
    await expect(state(page)).toHaveAttribute('data-state', 'speaking')
    // The voice ends by itself. The loop may then give a new utterance, so the
    // test only checks that the dock left Speaking.
    await expect(state(page)).not.toHaveAttribute('data-state', 'speaking', { timeout: 10_000 })
  })

  test('Stop voice returns to Listening', async ({ page }) => {
    const chat = await open(page, { speechSeconds: 8 })
    await start(page)
    await expect(page.getByTestId('live-stop-voice')).toHaveCount(0)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await chat.reply('A long answer.')
    await expect(state(page)).toHaveAttribute('data-state', 'speaking')
    await page.getByTestId('live-stop-voice').click()
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
    await expect(page.getByTestId('live-stop-voice')).toHaveCount(0)
  })

  test('Mute pauses listening and Unmute resumes it', async ({ page }) => {
    await open(page)
    await start(page)
    await page.getByTestId('live-mute').click()
    await expect(state(page)).toHaveAttribute('data-state', 'paused')
    await expect(page.getByTestId('live-mute')).toHaveText('Unmute')
    await page.getByTestId('live-mute').click()
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
  })

  test('a denied microphone shows the explanation and typing still works', async ({ page }) => {
    const chat = await open(page, { denyMic: true })
    await start(page)
    await expect(page.getByTestId('live-error')).toContainText('Microphone access was blocked')
    await expect(page.getByTestId('live-retry')).toBeVisible()
    await page.getByTestId('live-type').click()
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    await expect(page.getByTestId('chat-input')).toBeFocused()
    await page.getByTestId('chat-input').fill('typed instead')
    await page.getByTestId('chat-send').click()
    await expect.poll(() => chat.posts).toEqual(['typed instead'])
  })

  test('an insecure page disables the button with a reason', async ({ page }) => {
    await open(page, { insecure: true })
    await expect(page.getByTestId('live-button')).toBeDisabled()
    await expect(page.getByTestId('live-reason')).toContainText('HTTPS')
  })

  test('a failed transcription shows an error with Retry and the thread is unchanged', async ({ page }) => {
    const chat = await open(page, { transcribeStatus: 500 })
    await start(page)
    await expect(page.getByTestId('live-error')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('live-error')).toContainText('could not be turned into text')
    // The server's own words stay as a small detail.
    await expect(page.getByTestId('live-error-detail')).toContainText('speech model is not loaded')
    expect(chat.posts).toHaveLength(0)
    await expect(page.getByTestId('live-end')).toBeVisible()
    await page.getByTestId('live-retry').click()
    await expect(page.getByTestId('live-error')).toHaveCount(0)
    await expect(state(page)).toHaveAttribute('data-state', /listening|hearing|transcribing/)
  })

  test('a blocked audio element shows Play, and Play continues the voice', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await page.evaluate(() => { window.__blockPlay = true })
    await chat.reply('Hello there.')
    await expect(page.getByTestId('live-blocked')).toBeVisible()
    await expect(state(page)).toHaveAttribute('data-state', 'blocked')
    await page.getByTestId('live-play').click()
    await expect(state(page)).toHaveAttribute('data-state', 'speaking')
    await expect(state(page)).toHaveAttribute('data-state', 'listening', { timeout: 10_000 })
  })

  test('an errored turn returns to Listening and nothing is spoken', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await chat.fail()
    await expect(state(page)).toHaveAttribute('data-state', 'listening', { timeout: 10_000 })
    expect(chat.speech).toBe(0)
  })

  test('Type instead ends Live and focuses the message box', async ({ page }) => {
    await open(page)
    await start(page)
    await page.getByTestId('live-type').click()
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    await expect(page.getByTestId('chat-input')).toBeFocused()
    await expect(page.getByTestId('live-button')).toBeVisible()
  })

  test('End Live closes the dock', async ({ page }) => {
    await open(page)
    await start(page)
    await page.getByTestId('live-end').click()
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    await expect(page.getByTestId('chat-input')).toBeVisible()
  })

  test('hiding the tab pauses Live', async ({ page }) => {
    await open(page)
    await start(page)
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await expect(state(page)).toHaveAttribute('data-state', 'paused')
    await expect(page.getByTestId('live-hidden')).toBeVisible()
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
    await expect(page.getByTestId('live-hidden')).toHaveCount(0)
  })

  test('Live is hidden while dictation records', async ({ page }) => {
    await open(page)
    await page.getByTestId('chat-dictate').click()
    await expect(page.getByTestId('chat-dictate')).toHaveAttribute('data-state', 'recording')
    await expect(page.getByTestId('live-button')).toHaveCount(0)
    await page.getByTestId('chat-dictate').click()
    await expect(page.getByTestId('live-button')).toBeVisible()
  })

  test('Send is disabled while the dock shows and the draft is kept', async ({ page }) => {
    const chat = await open(page)
    await page.getByTestId('chat-input').fill('foo')
    await start(page)
    await expect(page.getByTestId('chat-send')).toBeDisabled()
    await page.getByTestId('chat-send').click({ force: true })
    await page.waitForTimeout(300)
    expect(chat.posts).toEqual([])
    await page.getByTestId('live-type').click()
    await expect(page.getByTestId('chat-input')).toHaveValue('foo')
  })

  test('focus moves into the dock on start and back to the Live button after End Live', async ({ page }) => {
    await open(page)
    await page.getByTestId('live-button').focus()
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('live-dock')).toBeFocused()
    await page.getByTestId('live-end').focus()
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('live-dock')).toHaveCount(0)
    await expect(page.getByTestId('live-button')).toBeFocused()
  })

  test('each error code has its own message', async ({ page }) => {
    await open(page, { denyMic: true })
    await start(page)
    await expect(page.getByTestId('live-error')).toContainText('Microphone access was blocked')
    await expect(page.getByTestId('live-error-detail')).toHaveCount(0)
  })

  test('Live is hidden while dictation transcribes', async ({ page }) => {
    const chat = await open(page)
    let release
    chat.gate = new Promise((r) => { release = r })
    await page.getByTestId('chat-dictate').click()
    await expect(page.getByTestId('chat-dictate')).toHaveAttribute('data-state', 'recording')
    await page.getByTestId('chat-dictate').click()
    await expect(page.getByTestId('chat-dictate')).toHaveAttribute('data-state', 'transcribing')
    await expect(page.getByTestId('live-button')).toHaveCount(0)
    release()
    await expect(page.getByTestId('live-button')).toBeVisible()
  })

  test('Esc during a voice turn stops it, speaks nothing and goes back to Listening', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await expect(state(page)).toHaveAttribute('data-state', 'thinking')
    // The model has said part of its answer when the person stops it.
    chat.messages.push({ seq: 2, turn: 1, role: 'assistant', text: 'Well, the time is', at: AT })
    await page.keyboard.press('Escape')
    await expect.poll(() => chat.stops).toBe(1)
    await emit(page, 'turn_end', { kind: 'turn_end' })
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
    await page.waitForTimeout(800)
    expect(chat.speech).toBe(0)
  })

  test('the composer Stop button during a voice turn speaks nothing either', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    chat.messages.push({ seq: 2, turn: 1, role: 'assistant', text: 'Well, the time is', at: AT })
    await page.getByTestId('chat-stop').click()
    await expect.poll(() => chat.stops).toBe(1)
    await emit(page, 'turn_end', { kind: 'turn_end' })
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
    await page.waitForTimeout(800)
    expect(chat.speech).toBe(0)
  })

  test('the dock offers Stop while the reply is being made', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await expect(state(page)).toHaveAttribute('data-state', 'thinking')
    await page.getByTestId('live-stop-voice').click()
    await expect.poll(() => chat.stops).toBe(1)
    await emit(page, 'turn_end', { kind: 'turn_end' })
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
    expect(chat.speech).toBe(0)
  })

  test('a browser without a microphone API explains it and typing works', async ({ page }) => {
    const chat = await open(page, { noMic: true })
    await expect(page.getByTestId('live-button')).toBeDisabled()
    await expect(page.getByTestId('live-reason')).toContainText('microphone')
    await page.getByTestId('chat-input').fill('typed')
    await page.getByTestId('chat-send').click()
    await expect.poll(() => chat.posts).toEqual(['typed'])
  })

  test('a failed voice shows an error with Retry and nothing more is sent', async ({ page }) => {
    const chat = await open(page, { speechStatus: 503 })
    await start(page)
    await expect.poll(() => chat.posts.length, { timeout: 15_000 }).toBe(1)
    await chat.reply('Hello there.')
    await expect(page.getByTestId('live-error')).toContainText('reply voice could not be played')
    await expect(page.getByTestId('live-retry')).toBeVisible()
    expect(chat.posts).toHaveLength(1)
    await page.getByTestId('live-retry').click()
    await expect(page.getByTestId('live-error')).toHaveCount(0)
  })

  test('Esc during a running turn stops the turn and Live stays on', async ({ page }) => {
    const chat = await open(page)
    await start(page)
    chat.running = true
    await emit(page, 'turn_start', { kind: 'turn_start', turn: 1 })
    await expect(page.getByTestId('chat-stop')).toBeVisible()
    await expect(page.getByTestId('live-dock')).toContainText('Waiting for the reply')
    await page.keyboard.press('Escape')
    await expect.poll(() => chat.stops).toBe(1)
    await expect(page.getByTestId('live-dock')).toBeVisible()
    await expect(state(page)).toHaveAttribute('data-state', 'listening')
  })
})
