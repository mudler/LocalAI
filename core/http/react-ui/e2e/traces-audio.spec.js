import { test, expect } from './coverage-fixtures.js'

// Audio snippets on the Traces page must play through a blob: object URL —
// the CSP's connect-src allows blob: but not data:, and the waveform peaks
// renderer fetch()es the player src — and must degrade to a readable note
// (not a broken player) when the stored payload is the "<truncated: N bytes>"
// marker an older server stamped into oversized fields.

// Minimal valid 16 kHz mono 16-bit PCM WAV (0.1s 440 Hz sine), base64-encoded.
function wavBase64(samples = 1600, rate = 16000) {
  const dataSize = samples * 2
  const buf = Buffer.alloc(44 + dataSize)
  buf.write('RIFF', 0)
  buf.writeUInt32LE(36 + dataSize, 4)
  buf.write('WAVE', 8)
  buf.write('fmt ', 12)
  buf.writeUInt32LE(16, 16)
  buf.writeUInt16LE(1, 20) // PCM
  buf.writeUInt16LE(1, 22) // mono
  buf.writeUInt32LE(rate, 24)
  buf.writeUInt32LE(rate * 2, 28)
  buf.writeUInt16LE(2, 32)
  buf.writeUInt16LE(16, 34)
  buf.write('data', 36)
  buf.writeUInt32LE(dataSize, 40)
  for (let i = 0; i < samples; i++) {
    buf.writeInt16LE(Math.round(8000 * Math.sin((2 * Math.PI * 440 * i) / rate)), 44 + i * 2)
  }
  return buf.toString('base64')
}

function transcriptionTrace(audioWavBase64) {
  return {
    type: 'transcription',
    timestamp: Date.now() * 1_000_000,
    model_name: 'parakeet-test',
    summary: 'transcribed utterance',
    duration: 500_000_000,
    error: null,
    data: {
      audio_wav_base64: audioWavBase64,
      audio_duration_s: 0.1,
      audio_snippet_s: 0.1,
      audio_sample_rate: 16000,
      audio_samples: 1600,
      audio_rms_dbfs: -12.0,
      audio_peak_dbfs: -6.0,
      audio_dc_offset: 0,
    },
  }
}

async function openBackendTraceRow(page, traces) {
  await page.route('**/api/traces?*', (route) => {
    route.fulfill({ contentType: 'application/json', body: JSON.stringify([]) })
  })
  await page.route('**/api/backend-traces?*', (route) => {
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(traces) })
  })
  await page.goto('/app/traces')
  await expect(page.locator('text=Tracing is')).toBeVisible({ timeout: 10_000 })
  await page.locator('button', { hasText: 'Backend Traces' }).click()
  await page.locator('td', { hasText: 'parakeet-test' }).first().click()
}

test.describe('Traces - Audio Snippets', () => {
  test('plays a clip through a blob: URL, not a CSP-blocked data: URL', async ({ page }) => {
    await openBackendTraceRow(page, [transcriptionTrace(wavBase64())])

    // The expanded row carries the snippet metrics and a player whose source
    // is an object URL (connect-src allows blob:, so the peaks fetch works).
    await expect(page.locator('text=Audio Snippet')).toBeVisible()
    const audio = page.locator('audio')
    await expect(audio).toHaveCount(1)
    const src = await audio.getAttribute('src')
    expect(src).toMatch(/^blob:/)
    await expect(page.getByTestId('audio-snippet-unavailable')).toHaveCount(0)
  })

  test('shows a readable note instead of a broken player for truncated payloads', async ({ page }) => {
    await openBackendTraceRow(page, [transcriptionTrace('<truncated: 281660 bytes>')])

    await expect(page.locator('text=Audio Snippet')).toBeVisible()
    await expect(page.getByTestId('audio-snippet-unavailable')).toBeVisible()
    await expect(page.locator('audio')).toHaveCount(0)
  })
})
