import { test, expect } from '@playwright/test'

function mockCapabilities(page, capabilities) {
  return page.route('**/api/models/capabilities', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: capabilities }),
    })
  })
}

// Returns a (Promise, resolver) pair that records the multipart form fields
// the page submitted to /audio/transformations. The handler returns a tiny
// fake WAV blob so the page can render its result waveforms.
function mockAudioTransform(page, filename = 'transformed.wav') {
  let resolveSubmit
  const submitted = new Promise((resolve) => { resolveSubmit = resolve })

  page.route('**/audio/transformations', (route) => {
    if (route.request().method() !== 'POST') return route.continue()
    const req = route.request()
    const body = req.postData() || ''
    resolveSubmit({
      contentType: req.headers()['content-type'] || '',
      bodySize: body.length,
      // Naive multipart field name extraction so a test can assert the
      // form-data shape without parsing the multipart body.
      fields: Array.from(body.matchAll(/name="([^"]+)"/g)).map((m) => m[1]),
    })
    const wavHeader = new Uint8Array(44) // 44-byte RIFF/WAVE skeleton
    route.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'audio/wav',
        'Content-Disposition': `attachment; filename="${filename}"`,
      },
      body: Buffer.from(wavHeader),
    })
  })

  return submitted
}

// Build a tiny in-memory WAV file (44-byte header, 4 silent samples) so
// Playwright's setInputFiles + the page's audio decoder both have valid
// bytes to chew on. Returns { name, mimeType, buffer } for setInputFiles.
function makeFakeWav(name) {
  const sampleRate = 16000
  const samples = 4
  const dataLen = samples * 2
  const buf = Buffer.alloc(44 + dataLen)
  buf.write('RIFF', 0)
  buf.writeUInt32LE(36 + dataLen, 4)
  buf.write('WAVE', 8)
  buf.write('fmt ', 12)
  buf.writeUInt32LE(16, 16)            // PCM chunk size
  buf.writeUInt16LE(1, 20)             // PCM format
  buf.writeUInt16LE(1, 22)             // channels = 1
  buf.writeUInt32LE(sampleRate, 24)    // sample rate
  buf.writeUInt32LE(sampleRate * 2, 28)// byte rate
  buf.writeUInt16LE(2, 32)             // block align
  buf.writeUInt16LE(16, 34)            // bits per sample
  buf.write('data', 36)
  buf.writeUInt32LE(dataLen, 40)
  // body left as zeros (silence)
  return { name, mimeType: 'audio/wav', buffer: buf }
}

test.describe('Audio Transform', () => {
  test.beforeEach(async ({ page }) => {
    await mockCapabilities(page, [
      { id: 'localvqe', capabilities: ['FLAG_AUDIO_TRANSFORM'] },
    ])
  })

  test('audio input has Upload + Record tabs', async ({ page }) => {
    await page.goto('/app/transform')
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible({ timeout: 10_000 })

    // The Audio (required) input should expose both tabs.
    const tabs = page.getByRole('tab')
    await expect(tabs.filter({ hasText: 'Upload' }).first()).toBeVisible()
    await expect(tabs.filter({ hasText: 'Record' }).first()).toBeVisible()
  })

  test('echo-test button only appears once a reference is loaded', async ({ page }) => {
    await page.goto('/app/transform')
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible({ timeout: 10_000 })

    // No reference yet → echo button hidden.
    await expect(page.getByRole('button', { name: /Echo test/ })).toHaveCount(0)

    // Upload a reference into the second AudioInput's file picker.
    await page.locator('input[type="file"]').nth(1).setInputFiles(makeFakeWav('ref.wav'))
    await expect(page.getByRole('button', { name: /Echo test/ })).toBeVisible()
  })

  test('renders the AudioTransform page directly', async ({ page }) => {
    await page.goto('/app/transform')
    await expect(page.getByRole('heading', { name: /Audio Transform/ })).toBeVisible({ timeout: 10_000 })
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible()
    // Audio (required) + Reference (optional) drop zones
    await expect(page.getByText(/Audio \(required\)/)).toBeVisible()
    await expect(page.getByText(/Reference \(optional\)/)).toBeVisible()
  })

  test('uploads an audio file, posts multipart, renders enhanced waveform', async ({ page }) => {
    const submitted = mockAudioTransform(page, 'enhanced.wav')

    await page.goto('/app/transform')
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible({ timeout: 10_000 })

    // Upload mic file via the hidden file input under "Audio (required)".
    const audioInput = page.locator('input[type="file"]').first()
    await audioInput.setInputFiles(makeFakeWav('mic.wav'))
    await expect(page.getByText('mic.wav')).toBeVisible()

    // Set a backend tuning param so the form posts params[noise_gate]=true.
    await page.locator('.textarea').fill('noise_gate=true')

    await page.getByRole('button', { name: /Transform/ }).last().click()

    const form = await submitted
    expect(form.contentType).toContain('multipart/form-data')
    expect(form.fields).toContain('model')
    expect(form.fields).toContain('audio')
    expect(form.fields).toContain('params[noise_gate]')

    // After processing, the output WaveformPlayer mounts with a download button.
    await expect(page.getByRole('link', { name: /Download/ })).toBeVisible({ timeout: 10_000 })
  })

  test('reference file is forwarded as a multipart field when provided', async ({ page }) => {
    const submitted = mockAudioTransform(page)

    await page.goto('/app/transform')
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible({ timeout: 10_000 })

    const inputs = page.locator('input[type="file"]')
    await inputs.nth(0).setInputFiles(makeFakeWav('mic.wav'))
    // After the audio file is set, that AudioInput collapses to a filename +
    // Clear button and removes its <input>. The reference AudioInput, which
    // was at nth(1), is now the sole remaining input — query afresh.
    await inputs.first().setInputFiles(makeFakeWav('loopback.wav'))

    await page.getByRole('button', { name: /Transform/ }).last().click()

    const form = await submitted
    expect(form.fields).toContain('audio')
    expect(form.fields).toContain('reference')
  })

  test('history entry appears after a successful transform and persists across navigation', async ({ page }) => {
    mockAudioTransform(page, 'enhanced.wav')

    await page.goto('/app/transform')
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible({ timeout: 10_000 })

    await page.locator('input[type="file"]').first().setInputFiles(makeFakeWav('mic.wav'))
    await page.getByRole('button', { name: /Transform/ }).last().click()
    await expect(page.getByRole('link', { name: /Download/ })).toBeVisible({ timeout: 10_000 })

    await expect(page.getByTestId('media-history-item')).toHaveCount(1)
    await expect(page.getByTestId('media-history-item')).toContainText('mic.wav')

    // Persist across page reloads via localStorage.
    await page.waitForTimeout(600)
    await page.goto('/app/transform')
    await expect(page.getByTestId('media-history-item')).toHaveCount(1)
  })

  test('shows an error banner when the backend returns 4xx', async ({ page }) => {
    await page.route('**/audio/transformations', (route) => {
      if (route.request().method() !== 'POST') return route.continue()
      route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({ error: { message: 'audio sample rate 44100 != model 16000' } }),
      })
    })

    await page.goto('/app/transform')
    await expect(page.getByRole('button', { name: 'localvqe' })).toBeVisible({ timeout: 10_000 })

    await page.locator('input[type="file"]').first().setInputFiles(makeFakeWav('mic.wav'))
    await page.getByRole('button', { name: /Transform/ }).last().click()

    await expect(page.getByText(/sample rate/)).toBeVisible({ timeout: 10_000 })
  })
})
