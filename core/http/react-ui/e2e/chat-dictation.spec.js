import { test, expect } from './coverage-fixtures.js'
import { chatOf, mockChat, openChat, MODELS } from './chat-fixtures.js'

// Dictation in the composer: record from the microphone, send the recording to
// /v1/audio/transcriptions with the installed transcription model, put the text
// into the message box. The browser has no microphone here, so getUserMedia and
// MediaRecorder are replaced with stand-ins that hand back a few bytes.

const WHISPER = { id: 'whisper-1', capabilities: ['FLAG_TRANSCRIPT'] }

async function fakeMicrophone(page) {
  await page.addInitScript(() => {
    navigator.mediaDevices.getUserMedia = async () => ({ getTracks: () => [{ stop() { window.__micStopped = true } }] })
    window.MediaRecorder = class {
      constructor() { this.state = 'inactive'; this.mimeType = 'audio/webm' }
      start() { this.state = 'recording' }
      stop() {
        this.state = 'inactive'
        this.ondataavailable?.({ data: new Blob(['voice'], { type: 'audio/webm' }) })
        this.onstop?.()
      }
    }
  })
}

const input = (page) => page.getByTestId('chat-input')

test.describe('Chat dictation', () => {
  test('records, transcribes with the transcription model and appends the text', async ({ page }) => {
    await fakeMicrophone(page)
    await mockChat(page, { models: [...MODELS, WHISPER] })
    let form = ''
    await page.route('**/v1/audio/transcriptions', (route) => {
      form = route.request().postDataBuffer()?.toString('latin1') || ''
      route.fulfill({ json: { text: ' Wie spät ist es? ' } })
    })
    await openChat(page, [chatOf('c1', 'Dictate', 'qwen3-8b', [])])

    await input(page).fill('Frage:')
    const btn = page.getByTestId('chat-dictate')
    await btn.click()
    await expect(btn).toHaveAttribute('data-state', 'recording')
    await expect(btn).toHaveAttribute('aria-pressed', 'true')
    await btn.click()

    await expect(input(page)).toHaveValue('Frage: Wie spät ist es?')
    await expect(btn).toHaveAttribute('data-state', 'idle')
    expect(form).toContain('name="model"\r\n\r\nwhisper-1')
    expect(form).toContain('filename="dictation.webm"')
    expect(await page.evaluate(() => window.__micStopped)).toBe(true)
  })

  test('shows an error and keeps the message when transcription fails', async ({ page }) => {
    await fakeMicrophone(page)
    await mockChat(page, { models: [...MODELS, WHISPER] })
    await page.route('**/v1/audio/transcriptions', (route) =>
      route.fulfill({ status: 500, json: { error: { message: 'backend down' } } }))
    await openChat(page, [chatOf('c1', 'Dictate', 'qwen3-8b', [])])

    await input(page).fill('bleibt')
    await page.getByTestId('chat-dictate').click()
    await page.getByTestId('chat-dictate').click()
    await expect(page.getByText(/Transcription failed/)).toBeVisible()
    await expect(input(page)).toHaveValue('bleibt')
  })

  test('is not offered without a transcription model', async ({ page }) => {
    await fakeMicrophone(page)
    await mockChat(page)
    await openChat(page, [chatOf('c1', 'Dictate', 'qwen3-8b', [])])
    await expect(page.getByRole('button', { name: 'Attach audio' })).toBeVisible()
    await expect(page.getByTestId('chat-dictate')).toHaveCount(0)
  })
})
