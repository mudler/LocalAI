import { test, expect } from './coverage-fixtures.js'
import {
  mockStudio, readStore, sampleHistory, seedHistory, silentWav, stubMedia,
} from './studio-fixtures.js'

// The shared frame of the seven Studio workspaces (src/components/studio/
// Workspace.jsx): type tabs, a compose card with an Advanced fold, a run area
// and a strip of recent results, and what the result toolbar does. Images is
// the reference; the other types have one run-through each.

const compose = (page) => page.locator('[data-testid="ws-compose"]')
const prompt = (page) => compose(page).locator('textarea').first()
const generate = (page) => compose(page).locator('button[type="submit"]')
const tile = (page) => page.locator('[data-testid="media-history-item"]')

async function setup(page, opts = {}) {
  await mockStudio(page, opts)
  await stubMedia(page)
  await page.route('**/generated-videos/**', route => route.fulfill({ status: 404, body: '' }))
}

test.describe('Workspace frame (Images)', () => {
  test('the type tabs switch the workspace', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio/images')
    await expect(page.locator('[data-testid="studio-workspace"]')).toHaveAttribute('data-type', 'images')
    await expect(page.locator('.studio-tab')).toHaveCount(8)
    await page.locator('.studio-tab[data-tab="tts"]').click()
    await expect(page).toHaveURL(/\/app\/studio\/tts$/)
    await expect(page.locator('[data-testid="studio-workspace"]')).toHaveAttribute('data-type', 'tts')
  })

  test('the compose card has the model chip, the essentials, an estimate and one action', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio/images')
    await expect(compose(page).getByRole('heading', { level: 1 })).toContainText('Image')
    await expect(compose(page).getByRole('button', { name: 'flux.1-schnell' })).toBeVisible()
    await expect(page.locator('[data-testid="ws-size"]')).toHaveValue('512x512')
    await expect(page.locator('[data-testid="ws-count"]')).toHaveValue('1')
    await expect(page.locator('[data-testid="ws-estimate"]')).toContainText('Needs')
    // The reason is on screen while Generate cannot run.
    await expect(generate(page)).toBeDisabled()
    await expect(page.locator('[data-testid="ws-why"]')).toHaveText('Write a prompt first')
    await prompt(page).fill('a brass orrery')
    await expect(generate(page)).toBeEnabled()
    await expect(page.locator('[data-testid="ws-why"]')).toHaveCount(0)
  })

  test('starters fill the prompt and go away once there are words', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio/images')
    await page.locator('[data-testid="ws-starters"] button').first().click()
    await expect(prompt(page)).not.toHaveValue('')
    await expect(page.locator('[data-testid="ws-starters"]')).toHaveCount(0)
  })

  test('the Advanced fold names what is inside, opens on demand and keeps what is set in its name', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio/images')
    const bar = compose(page).getByRole('button', { name: /Advanced Settings/ })
    await expect(bar).toHaveAttribute('aria-expanded', 'false')
    await expect(bar).toContainText('negative prompt, steps, seed')
    await expect(page.locator('#image-steps')).toHaveCount(0)
    await bar.click()
    await expect(bar).toHaveAttribute('aria-expanded', 'true')
    await page.locator('#image-steps').fill('4')
    await bar.click()
    await expect(bar).toContainText('steps 4')
  })

  test('a held request shows a job card with the time that has passed and no invented progress', async ({ page }) => {
    await setup(page)
    let release
    await page.route('**/v1/images/generations', async route => { await new Promise(r => { release = r }); await route.fulfill({ json: { data: [{ url: '/generated-images/x.png' }] } }) })
    await page.goto('/app/studio/images')
    await prompt(page).fill('a lighthouse')
    await generate(page).click()
    const job = page.locator('[data-testid="ws-job"]')
    await expect(job).toBeVisible()
    await expect(job.getByRole('progressbar')).toHaveAttribute('data-indeterminate', 'true')
    await expect(job.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow', /.+/)
    await expect(job).toContainText('flux.1-schnell')
    await expect(job).not.toContainText('%')
    release()
    await expect(page.locator('[data-testid="ws-result"]')).toBeVisible()
  })

  test('a failed run says what the server said, keeps the form and offers one action', async ({ page }) => {
    await setup(page)
    let calls = 0
    await page.route('**/v1/images/generations', route => {
      calls += 1
      if (calls === 1) return route.fulfill({ status: 500, json: { error: { message: 'out of memory while sampling' } } })
      return route.fulfill({ json: { data: [{ url: '/generated-images/ok.png' }] } })
    })
    await page.goto('/app/studio/images')
    await prompt(page).fill('a lighthouse')
    await generate(page).click()
    const failed = page.locator('[data-testid="ws-failed"]')
    await expect(failed).toContainText('out of memory while sampling')
    await expect(failed).toContainText('still here')
    await expect(prompt(page)).toHaveValue('a lighthouse')
    await failed.getByRole('button', { name: 'Try again' }).click()
    await expect(page.locator('[data-testid="ws-result"]')).toBeVisible()
    expect(calls).toBe(2)
  })

  test('with no model for the type the install note shows and Generate says why it cannot run', async ({ page }) => {
    await setup(page, { types: ['tts'] })
    await page.goto('/app/studio/images')
    await expect(page.locator('[data-testid="studio-install-note"]')).toBeVisible()
    await prompt(page).fill('a lighthouse')
    await expect(generate(page)).toBeDisabled()
    await expect(page.locator('[data-testid="ws-why"]')).toContainText('Install the Images model first')
    // The words stay.
    await expect(prompt(page)).toHaveValue('a lighthouse')
  })

  test('the strip lists this type from the shared history, filters favourites and toggles them', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory(), { favourites: ['img-forest'] })
    await page.goto('/app/studio/images')
    await expect(tile(page)).toHaveCount(4)
    await page.getByRole('tab', { name: /Favourites/ }).click()
    await expect(tile(page)).toHaveCount(1)
    await expect(tile(page)).toContainText('Misty pine forest')
    await page.getByRole('tab', { name: 'All' }).click()
    // Opening a tile shows it above; its star is the same favourite list the front page keeps.
    await tile(page).filter({ hasText: 'ceramic bowls' }).locator('.ws-tile__open').click()
    const star = page.locator('[data-testid="ws-favourite"]')
    await expect(star).toHaveAttribute('aria-pressed', 'false')
    await star.click()
    await expect(star).toHaveAttribute('aria-pressed', 'true')
    await expect.poll(() => readStore(page, 'localai_studio_favourites')).toContain('img-bowls')
    await star.click()
    await expect.poll(async () => (await readStore(page, 'localai_studio_favourites')) || []).not.toContain('img-bowls')
  })

  test('Use in lists the next steps and opens the destination with the result as its source', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/images')
    await tile(page).filter({ hasText: 'Misty pine forest' }).locator('.ws-tile__open').click()
    await page.locator('[data-testid="ws-use-in"]').click()
    await expect(page.locator('[data-testid="use-in-animate"]')).toBeEnabled()
    await expect(page.locator('[data-testid="use-in-to-3d"]')).toBeEnabled()
    await expect(page.locator('[data-testid="use-in-variation"]')).toBeEnabled()
    await page.locator('[data-testid="use-in-animate"]').click()
    await expect(page).toHaveURL(/\/app\/studio\/video\?.*from=img-forest.*edge=animate/)
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
  })

  test('a step the destination cannot start from is listed, disabled, with the reason', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/video')
    await tile(page).locator('.ws-tile__open').click()
    await page.locator('[data-testid="ws-use-in"]').click()
    const item = page.locator('[data-testid="use-in-soundtrack"]')
    await expect(item).toBeDisabled()
    await expect(item).toContainText('cannot start from a video yet')
  })

  test('a type with nothing to send on has Use in disabled with the reason', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/diarization')
    await tile(page).locator('.ws-tile__open').click()
    await expect(page.locator('[data-testid="ws-use-in"]')).toBeDisabled()
  })

  test('Lineage opens the front page lineage view for the result', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/images')
    await tile(page).filter({ hasText: 'Misty pine forest' }).locator('.ws-tile__open').click()
    await page.locator('[data-testid="ws-lineage"]').click()
    await expect(page).toHaveURL(/\/app\/studio\?work=img-forest/)
    await expect(page.locator('[data-testid="studio-lineage"]')).toBeVisible()
  })

  test('a result made through a hand-off names where it came from and links to it', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/video')
    await tile(page).locator('.ws-tile__open').click()
    const from = page.locator('[data-testid="ws-from"]')
    await expect(from).toContainText('animate')
    await from.getByRole('link').click()
    await expect(page).toHaveURL(/\/app\/studio\?work=img-harbour$/)
  })

  test('Re-run with edits loads the take, outlines and lists what changed, and runs the edited values', async ({ page }) => {
    await setup(page)
    const history = sampleHistory()
    history.image[1].params = { size: '768x768' }
    await seedHistory(page, history)
    let body
    await page.route('**/v1/images/generations', route => { body = route.request().postDataJSON(); return route.fulfill({ json: { data: [{ url: '/generated-images/again.png' }] } }) })
    await page.goto('/app/studio/images')
    await tile(page).filter({ hasText: 'Misty pine forest' }).locator('.ws-tile__open').click()
    await page.locator('[data-testid="ws-rerun"]').click()
    await expect(prompt(page)).toHaveValue(/Misty pine forest/)
    await expect(page.locator('[data-testid="ws-size"]')).toHaveValue('768x768')
    await expect(page.locator('[data-testid="ws-changes"]')).toHaveCount(0)
    await expect(generate(page)).toHaveText(/Run again/)
    await page.locator('[data-testid="ws-size"]').selectOption('1024x1024')
    await prompt(page).fill('Misty pine forest at dusk')
    const changes = page.locator('[data-testid="ws-changes"]')
    await expect(changes).toContainText('size 768x768 to 1024x1024')
    await expect(changes).toContainText('text edited')
    await expect(page.locator('.ws-chip[data-changed]')).toHaveCount(1)
    await generate(page).click()
    await expect.poll(() => body?.size).toBe('1024x1024')
    expect(body.prompt).toBe('Misty pine forest at dusk')
    // After the run the comparison is cleared.
    await expect(generate(page)).toHaveText(/Generate/)
  })

  test('Re-run with edits is disabled, with the reason, when the input was not kept', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/diarization')
    await tile(page).locator('.ws-tile__open').click()
    await expect(page.locator('[data-testid="ws-rerun"]')).toBeDisabled()
    await expect(page.locator('[data-testid="ws-rerun"]')).toHaveAttribute('title', /not kept/)
  })

  test('deleting and clearing from the strip remove entries', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/images')
    await expect(tile(page)).toHaveCount(4)
    await tile(page).first().hover()
    await tile(page).first().getByTestId('media-history-delete').click()
    await expect(tile(page)).toHaveCount(3)
    await page.locator('.media-history-clear-btn').click()
    await expect(tile(page)).toHaveCount(0)
  })

  test('the hand-off note and source chip show when the page starts from a result', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/video?prompt=Slow%20push-in&from=img-forest&edge=animate')
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
    await expect(compose(page).locator('.ws-source--filled img')).toBeVisible()
    await expect(prompt(page)).toHaveValue('Slow push-in')
  })
})

test.describe('Workspace run-throughs', () => {
  test('Video: runs, plays the clip and the result carries the toolbar', async ({ page }) => {
    await setup(page)
    let body
    await page.route('**/video', route => { if (route.request().method() !== 'POST') return route.fallback(); body = route.request().postDataJSON(); return route.fulfill({ json: { data: [{ url: '/generated-videos/new.mp4' }] } }) })
    await page.goto('/app/studio/video')
    await prompt(page).fill('Boats drift on calm water')
    await page.locator('[data-testid="ws-size"]').selectOption('832x480')
    await page.locator('[data-testid="ws-duration"]').fill('3')
    await generate(page).click()
    const result = page.locator('[data-testid="ws-result"]')
    await expect(result.locator('video')).toHaveAttribute('src', '/generated-videos/new.mp4')
    await expect(result.locator('[data-testid="ws-download"]')).toHaveAttribute('href', '/generated-videos/new.mp4')
    expect(body).toMatchObject({ width: 832, height: 480, seconds: '3', fps: 16 })
    await expect(tile(page)).toHaveCount(1)
  })

  test('TTS: runs with a typed voice, shows the waveform player and the words', async ({ page }) => {
    await setup(page)
    let body
    await page.route('**/tts', route => {
      if (route.request().method() !== 'POST') return route.fallback()
      body = route.request().postDataJSON()
      return route.fulfill({ status: 200, headers: { 'Content-Type': 'audio/wav', 'Content-Disposition': 'attachment; filename="line.wav"' }, body: silentWav(2) })
    })
    await page.goto('/app/studio/tts')
    await prompt(page).fill('Welcome to the harbour.')
    await page.locator('[data-testid="ws-voice"]').fill('af_bella')
    await generate(page).click()
    const result = page.locator('[data-testid="ws-result"]')
    await expect(result.locator('audio')).toBeVisible()
    await expect(result).toContainText('Welcome to the harbour.')
    expect(body).toMatchObject({ model: 'kokoro-82m', input: 'Welcome to the harbour.', voice: 'af_bella' })
    await expect.poll(async () => (await readStore(page, 'localai_tts_history'))?.[0]?.params?.voice).toBe('af_bella')
  })

  test('Sound: switching to Advanced swaps the fields and a run sends them', async ({ page }) => {
    await setup(page)
    let body
    await page.route('**/v1/sound-generation', route => {
      body = route.request().postDataJSON()
      return route.fulfill({ status: 200, headers: { 'Content-Type': 'audio/wav', 'Content-Disposition': 'attachment; filename="pads.wav"' }, body: silentWav(2) })
    })
    await page.goto('/app/studio/sound')
    await page.getByRole('tab', { name: 'Advanced' }).click()
    await compose(page).getByLabel('Caption').fill('slow pads')
    await compose(page).getByLabel('Lyrics').fill('la la')
    await compose(page).getByRole('button', { name: /More options/ }).click()
    await compose(page).getByLabel('BPM').fill('72')
    await generate(page).click()
    await expect(page.locator('[data-testid="ws-result"] audio')).toBeVisible()
    expect(body).toMatchObject({ model_id: 'ace-step', caption: 'slow pads', lyrics: 'la la', bpm: 72 })
    await expect.poll(async () => (await readStore(page, 'localai_sound_history'))?.[0]?.params?.bpm).toBe('72')
  })

  test('Transform: a chosen file shows its input before the run, and the result shows both spectra', async ({ page }) => {
    await setup(page)
    await page.route('**/audio/transformations', route => route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'audio/wav', 'Content-Disposition': 'attachment; filename="clean.wav"', 'X-Audio-Input-Url': '/generated-audio/in.wav' },
      body: silentWav(2),
    }))
    await page.goto('/app/studio/transform')
    await expect(page.locator('[data-testid="ws-why"]')).toContainText('Add an audio file first')
    await page.locator('input[type="file"]').first().setInputFiles({ name: 'mic.wav', mimeType: 'audio/wav', buffer: silentWav(2) })
    await expect(page.locator('[data-testid="ws-view"]')).toContainText('waiting to be transformed')
    await generate(page).click()
    await expect(page.locator('[data-testid="spectrogram-output"]')).toBeVisible()
    await expect(page.locator('[data-testid="ws-result"]').locator('[data-testid="ws-download"]')).toBeVisible()
    await expect.poll(async () => (await readStore(page, 'localai_audio_transform_history'))?.[0]?.results?.length).toBe(2)
  })

  test('3D: a result has the viewer, the download and a disabled Re-run with the reason', async ({ page }) => {
    await setup(page)
    await page.route('**/3d/generations', route => route.fulfill({ json: { data: [{ url: '/generated-3d/t.glb' }] } }))
    await page.route('**/generated-3d/t.glb', route => route.fulfill({ contentType: 'model/gltf-binary', body: tinyGlb() }))
    await page.goto('/app/studio/threed')
    await expect(page.locator('[data-testid="ws-why"]')).toContainText('Add a picture first')
    await page.locator('#threed-image-file').setInputFiles({ name: 'in.png', mimeType: 'image/png', buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64') })
    await generate(page).click()
    await expect(page.locator('[data-testid="glb-download"]')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('[data-testid="ws-rerun"]')).toBeDisabled()
    await expect(tile(page)).toHaveCount(1)
  })

  test('Diarization: a run draws a timeline lane per speaker, talk time, segments and exports', async ({ page }) => {
    await setup(page)
    await page.route('**/v1/audio/diarization', route => route.fulfill({ json: {
      speakers: [{ id: 'SPEAKER_00', label: '0' }, { id: 'SPEAKER_01', label: '1' }],
      segments: [
        { id: 0, speaker: 'SPEAKER_00', label: '0', start: 0, end: 6, text: 'Morning everyone.' },
        { id: 1, speaker: 'SPEAKER_01', label: '1', start: 6, end: 10, text: 'Hello.' },
      ],
    } }))
    await page.goto('/app/studio/diarization')
    await page.locator('#diarization-file').setInputFiles({ name: 'call.wav', mimeType: 'audio/wav', buffer: silentWav(2) })
    await generate(page).click()
    await expect(page.locator('[data-testid="ws-timeline"] .ws-timeline__lane')).toHaveCount(2)
    await expect(page.locator('[data-testid="speaker-0"]')).toContainText('60%')
    await expect(page.locator('[data-testid="segments"] li')).toHaveCount(2)
    const download = page.waitForEvent('download')
    await page.locator('[data-testid="export-rttm"]').click()
    expect((await download).suggestedFilename()).toBe('call.rttm')
    await expect(page.locator('[data-testid="export-srt"]')).toBeVisible()
    await expect(page.locator('[data-testid="export-json"]')).toBeVisible()
  })
})

test.describe('Workspace on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  for (const type of ['images', 'video', 'threed', 'tts', 'sound', 'transform', 'diarization']) {
    test(`${type} fits the width and its action stays reachable`, async ({ page }) => {
      await setup(page)
      await seedHistory(page, sampleHistory())
      await page.goto(`/app/studio/${type}`)
      await expect(compose(page)).toBeVisible()
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
      await expect(generate(page)).toBeVisible()
      const box = await generate(page).boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(390)
    })
  }

  test('the result toolbar and the strip fit after a run', async ({ page }) => {
    await setup(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/images')
    await tile(page).first().locator('.ws-tile__open').click()
    await expect(page.locator('[data-testid="ws-result"]')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})

test('with reduced motion the job bar is still, not a moving stripe', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await setup(page)
  await page.route('**/v1/images/generations', () => new Promise(() => {}))
  await page.goto('/app/studio/images')
  await prompt(page).fill('a lighthouse')
  await generate(page).click()
  const bar = page.locator('[data-testid="ws-job"] .dk-progress-bar')
  await expect(bar).toBeVisible()
  expect(await bar.evaluate(el => getComputedStyle(el).animationName)).toBe('none')
})

// A one-triangle GLB, enough for the viewer to parse.
function tinyGlb() {
  const positions = new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0])
  const indices = new Uint32Array([0, 1, 2])
  const bin = Buffer.concat([Buffer.from(positions.buffer), Buffer.from(indices.buffer)])
  const json = {
    asset: { version: '2.0' }, scene: 0, scenes: [{ nodes: [0] }], nodes: [{ mesh: 0 }],
    meshes: [{ primitives: [{ attributes: { POSITION: 0 }, indices: 1 }] }],
    accessors: [
      { bufferView: 0, componentType: 5126, count: 3, type: 'VEC3', min: [0, 0, 0], max: [1, 1, 0] },
      { bufferView: 1, componentType: 5125, count: 3, type: 'SCALAR' },
    ],
    bufferViews: [{ buffer: 0, byteOffset: 0, byteLength: 36, target: 34962 }, { buffer: 0, byteOffset: 36, byteLength: 12, target: 34963 }],
    buffers: [{ byteLength: bin.length }],
  }
  let text = JSON.stringify(json)
  while (text.length % 4) text += ' '
  const jb = Buffer.from(text)
  const total = 12 + 8 + jb.length + 8 + bin.length
  const out = Buffer.alloc(total)
  out.writeUInt32LE(0x46546c67, 0); out.writeUInt32LE(2, 4); out.writeUInt32LE(total, 8)
  out.writeUInt32LE(jb.length, 12); out.writeUInt32LE(0x4e4f534a, 16); jb.copy(out, 20)
  const at = 20 + jb.length
  out.writeUInt32LE(bin.length, at); out.writeUInt32LE(0x004e4942, at + 4); bin.copy(out, at + 8)
  return out
}
