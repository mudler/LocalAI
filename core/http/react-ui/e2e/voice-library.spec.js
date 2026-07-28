import { test, expect } from './coverage-fixtures.js'

const VOICE_ID = '00000000-0000-0000-0000-000000000001'

function pcmWav(seconds = 2) {
  const sampleRate = 16000
  const dataSize = sampleRate * seconds * 2
  const buffer = Buffer.alloc(44 + dataSize)
  buffer.write('RIFF', 0)
  buffer.writeUInt32LE(36 + dataSize, 4)
  buffer.write('WAVEfmt ', 8)
  buffer.writeUInt32LE(16, 16)
  buffer.writeUInt16LE(1, 20)
  buffer.writeUInt16LE(1, 22)
  buffer.writeUInt32LE(sampleRate, 24)
  buffer.writeUInt32LE(sampleRate * 2, 28)
  buffer.writeUInt16LE(2, 32)
  buffer.writeUInt16LE(16, 34)
  buffer.write('data', 36)
  buffer.writeUInt32LE(dataSize, 40)
  return buffer
}

const profile = {
  id: VOICE_ID,
  name: 'Documentary narrator',
  description: 'Measured and clear',
  language: 'en-US',
  transcript: 'The exact words spoken in this reference.',
  voice: `localai://voice-profiles/${VOICE_ID}`,
  consent_confirmed_at: '2026-07-01T12:00:00Z',
  created_at: '2026-07-01T12:00:00Z',
  updated_at: '2026-07-01T12:00:00Z',
  audio: { duration_ms: 2000, sample_rate: 16000, channels: 1, bit_depth: 16, size_bytes: 64044, mime_type: 'audio/wav' },
}

async function mockVoiceAPIs(page) {
  const state = { createdMultipart: null }
  await page.route('**/api/models/capabilities', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [
      { id: 'qwen-base', capabilities: ['FLAG_TTS'], backend: 'qwen3-tts-cpp', voice_cloning: { reference_transcript_required: true, accepted_audio_formats: ['audio/wav'] } },
      { id: 'piper-default', capabilities: ['FLAG_TTS'], backend: 'piper' },
    ] }),
  }))
  await page.route('**/api/voice-profiles', async route => {
    if (route.request().method() === 'POST') {
      state.createdMultipart = route.request().postDataBuffer()
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(profile) })
      return
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [profile] }) })
  })
  await page.route(`**/api/voice-profiles/${VOICE_ID}/audio`, route => route.fulfill({
    status: 200,
    contentType: 'audio/wav',
    body: pcmWav(),
  }))
  return state
}

test.describe('Voice Library', () => {
  let apiState

  test.beforeEach(async ({ page }) => {
    apiState = await mockVoiceAPIs(page)
  })

  test('renders the library-first master/detail view', async ({ page }) => {
    await page.goto('/app/voice-library')
    await expect(page.getByRole('heading', { name: /Voice Library/i })).toBeVisible()
    await expect(page.locator('.voice-row', { hasText: 'Documentary narrator' })).toBeVisible()
    await expect(page.locator('.voice-library-detail')).toContainText('The exact words spoken in this reference.')
    await expect(page.locator('.voice-library-detail')).toContainText('Consent confirmed')
    await expect(page.getByRole('button', { name: /Use in Text to Speech/i })).toBeEnabled()
  })

  test('shows inline API usage and installed model compatibility', async ({ page }) => {
    await page.goto('/app/voice-library')
    await page.getByText('API usage and compatible models').click()
    const apiHelp = page.locator('.voice-detail__api')
    await expect(apiHelp).toContainText('qwen-base')
    await expect(apiHelp).toContainText('qwen3-tts-cpp')
    await expect(apiHelp.locator('code')).toContainText('/v1/audio/speech')
    await expect(apiHelp.locator('code')).toContainText(`localai://voice-profiles/${VOICE_ID}`)
  })

  test('offers server-declared gallery models when none are installed', async ({ page }) => {
    let galleryCapability = null
    let installedModel = null
    await page.route('**/api/models/capabilities', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ data: [] }),
    }))
    await page.route('**/api/models?**', route => {
      galleryCapability = new URL(route.request().url()).searchParams.get('capability')
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ models: [{
          id: 'localai@omnivoice-cpp',
          name: 'omnivoice-cpp',
          backend: 'omnivoice-cpp',
          installed: false,
          voice_cloning: { reference_transcript_required: true, accepted_audio_formats: ['audio/wav'] },
        }] }),
      })
    })
    await page.route('**/api/models/install/**', route => {
      installedModel = decodeURIComponent(route.request().url().split('/').pop())
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ jobID: 'voice-install' }) })
    })

    await page.goto('/app/voice-library')
    await expect(page.getByRole('heading', { name: 'Install a voice-cloning model' })).toBeVisible()
    await expect(page.locator('.voice-detail__model-list')).toContainText('omnivoice-cpp')
    expect(galleryCapability).toBe('voice_cloning')
    await page.locator('.voice-detail__model-list').getByRole('button', { name: 'Install' }).click()
    await expect.poll(() => installedModel).toBe('localai@omnivoice-cpp')
    await expect(page.getByText(/Installing omnivoice-cpp/)).toBeVisible()
  })

  test('keeps the Operate rail compact and accessible on small screens', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/app/voice-library')
    const rail = page.locator('.console-rail')
    await expect(rail.getByRole('link', { name: 'Backends' })).toBeHidden()
    await rail.getByRole('button', { name: 'Expand Operate navigation' }).click()
    await expect(rail.getByRole('link', { name: 'Backends' })).toBeVisible()
    await expect(rail.getByRole('button', { name: 'Collapse Operate navigation' })).toBeVisible()
  })

  test('passes the stable voice URI from the library into TTS', async ({ page }) => {
    let ttsBody = null
    await page.route('**/tts', async route => {
      ttsBody = route.request().postDataJSON()
      await route.fulfill({
        status: 200,
        contentType: 'audio/wav',
        headers: { 'Content-Disposition': 'attachment; filename="speech.wav"' },
        body: pcmWav(1),
      })
    })

    await page.goto(`/app/tts?voice=${VOICE_ID}`)
    await expect(page.locator('#tts-voice')).toHaveValue(VOICE_ID)
    await page.getByPlaceholder('Enter text to synthesize...').fill('Hello from the saved voice.')
    await page.getByRole('button', { name: /Generate$/ }).click()
    await expect.poll(() => ttsBody?.voice).toBe(`localai://voice-profiles/${VOICE_ID}`)
    expect(ttsBody.model).toBe('qwen-base')
  })

  test('normalizes an upload and creates a consented profile', async ({ page }) => {
    await page.goto('/app/voice-library/new')
    await page.locator('#voice-profile-audio-file').setInputFiles({
      name: 'reference.wav',
      mimeType: 'audio/wav',
      buffer: pcmWav(2),
    })
    await expect(page.getByText('Normalized reference')).toBeVisible()
    await page.locator('#voice-profile-name').fill('Documentary narrator')
    await page.locator('#voice-profile-transcript').fill('The exact words spoken in this reference.')
    await page.getByText('I confirm that this voice may be cloned').click()
    await page.getByRole('button', { name: /Save voice/i }).click()
    await expect(page).toHaveURL(new RegExp(`/app/voice-library\\?selected=${VOICE_ID}$`))

    const wavOffset = apiState.createdMultipart.indexOf(Buffer.from('RIFF'))
    expect(wavOffset).toBeGreaterThanOrEqual(0)
    expect(apiState.createdMultipart.readUInt16LE(wavOffset + 22)).toBe(1)
    expect(apiState.createdMultipart.readUInt32LE(wavOffset + 24)).toBe(24000)
    expect(apiState.createdMultipart.readUInt16LE(wavOffset + 34)).toBe(16)
  })
})
