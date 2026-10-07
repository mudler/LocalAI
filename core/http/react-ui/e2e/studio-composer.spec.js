import { test, expect } from './coverage-fixtures.js'
import {
  INSTALLED, mockStudio, readStore, sampleHistory, seedHistory, seedThreeD, stubMedia,
} from './studio-fixtures.js'

// The Studio front page composer (src/components/studio/StudioComposer.jsx):
// a prompt box, a chip per type, a type suggestion from the words, starters,
// the options each workspace accepts, and a hand-off to that workspace.

const CHIP = (key) => page => page.locator(`.studio-types .studio-type[data-type="${key}"]`)
const chip = (page, key) => CHIP(key)(page)
const prompt = (page) => page.getByRole('textbox', { name: 'Prompt' })
const hint = (page) => page.locator('[data-testid="studio-hint"]')
// Choose a result in the "Start from" select by the words in its label.
async function pickSource(page, label) {
  const value = await page.locator('[data-testid="studio-source"] option', { hasText: label }).getAttribute('value')
  await page.locator('[data-testid="studio-source"]').selectOption(value)
}
const generate = (page) => page.locator('[data-testid="studio-generate"]')

test.describe('Studio composer: choosing a type', () => {
  test('opens on Images with a prompt box, starters and the model that will run', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await expect(page.getByRole('heading', { name: 'What do you want to make?' })).toBeVisible()
    await expect(chip(page, 'images')).toHaveAttribute('aria-pressed', 'true')
    await expect(prompt(page)).toHaveAttribute('placeholder', /Describe the image/)
    await expect(page.locator('[data-testid="studio-starters"] button')).toHaveCount(3)
    await expect(page.locator('[data-testid="studio-model"]')).toHaveValue('flux.1-schnell')
  })

  test('shows all seven types as chips, in order, each with its key', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    const types = page.locator('.studio-types .studio-type')
    await expect(types).toHaveCount(7)
    await expect(types).toHaveText(['Images', 'Video', '3D', 'TTS', 'Sound', 'Transform', 'Diarization'])
    await expect(chip(page, 'sound')).toHaveAttribute('title', 'Alt+5')
  })

  test('a starter fills the prompt and the starters go away', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await page.locator('[data-testid="studio-starters"] button', { hasText: 'Macro fern' }).click()
    await expect(prompt(page)).toHaveValue(/dew-covered fern/)
    await expect(page.locator('[data-testid="studio-starters"]')).toHaveCount(0)
  })

  test('size and count appear only where the workspace has them', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="studio-size"]')).toBeVisible()
    await expect(page.locator('[data-testid="studio-count"]')).toBeVisible()
    await chip(page, 'video').click()
    await expect(page.locator('[data-testid="studio-size"]')).toBeVisible()
    await expect(page.locator('[data-testid="studio-count"]')).toHaveCount(0)
    await chip(page, 'tts').click()
    await expect(page.locator('[data-testid="studio-size"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-count"]')).toHaveCount(0)
  })

  test('types that start from a file say so instead of offering a prompt box', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    for (const key of ['threed', 'transform', 'diarization']) {
      await chip(page, key).click()
      await expect(prompt(page)).toHaveCount(0)
      await expect(page.locator('[data-testid="studio-file-lead"]')).toBeVisible()
      await expect(generate(page)).toContainText('Open')
    }
  })
})

test.describe('Studio composer: suggesting a type', () => {
  test('typing a video-sounding sentence suggests Video and offers to switch', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('Slow dolly through a misty pine forest, drone shot')
    await expect(hint(page)).toContainText('Sounds like Video')
    await page.locator('[data-testid="studio-switch"]').click()
    await expect(chip(page, 'video')).toHaveAttribute('aria-pressed', 'true')
    await expect(prompt(page)).toHaveValue(/Slow dolly/)
    await expect(hint(page)).toHaveText('')
  })

  test('the suggestion never switches the type on its own', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('Read this aloud in a warm voice')
    await expect(hint(page)).toContainText('Sounds like TTS')
    await expect(chip(page, 'images')).toHaveAttribute('aria-pressed', 'true')
  })

  test('Alt+Enter takes the suggestion', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('Lo-fi piano with rain on glass')
    await expect(hint(page)).toContainText('Sounds like Sound')
    await page.keyboard.press('Alt+Enter')
    await expect(chip(page, 'sound')).toHaveAttribute('aria-pressed', 'true')
  })

  test('a short or ordinary sentence suggests nothing', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('hello')
    await expect(hint(page)).toHaveText('')
    await prompt(page).fill('My cat sleeping on the sofa')
    await expect(hint(page)).toHaveText('')
  })

  test('no suggestion when the sentence already fits the chosen type', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('A poster of a lighthouse at dusk')
    await expect(hint(page)).toHaveText('')
  })

  test('a suggested type with no model says so and offers to show what it needs', async ({ page }) => {
    await mockStudio(page, { types: ['images'] })
    await page.goto('/app/studio')
    await prompt(page).fill('Slow dolly through a misty pine forest')
    await expect(hint(page)).toContainText('no Video model is installed yet')
    await page.locator('[data-testid="studio-switch"]').click()
    await expect(chip(page, 'video')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.locator('[data-testid="studio-install-note"]')).toBeVisible()
    await expect(prompt(page)).toHaveValue(/Slow dolly/)
  })
})

test.describe('Studio composer: keys', () => {
  test('/ focuses the prompt from anywhere on the page', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await page.locator('h1.page-title').click()
    await page.keyboard.press('/')
    await expect(prompt(page)).toBeFocused()
    // Typing a slash in the prompt is a slash, not a jump.
    await page.keyboard.type('a/b')
    await expect(prompt(page)).toHaveValue('a/b')
  })

  test('Alt+1 to Alt+7 pick a type', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    const order = ['images', 'video', 'threed', 'tts', 'sound', 'transform', 'diarization']
    for (let i = 0; i < order.length; i++) {
      await page.keyboard.press(`Alt+Digit${i + 1}`)
      await expect(chip(page, order[i])).toHaveAttribute('aria-pressed', 'true')
    }
  })

  test('Ctrl+Enter generates, and does nothing with an empty prompt', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).focus()
    await page.keyboard.press('Control+Enter')
    await expect(page).toHaveURL(/\/app\/studio$/)
    await prompt(page).fill('a brass orrery')
    await page.keyboard.press('Control+Enter')
    await expect(page).toHaveURL(/\/app\/studio\/images\?/)
  })
})

test.describe('Studio composer: Generate', () => {
  test('is disabled with a reason until there is a prompt', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await expect(generate(page)).toBeDisabled()
    await expect(page.locator('[data-testid="studio-why"]')).toHaveText('Write a prompt first')
    await prompt(page).fill('a brass orrery')
    await expect(generate(page)).toBeEnabled()
    await expect(page.locator('[data-testid="studio-why"]')).toHaveCount(0)
  })

  test('is disabled with the install reason when the type has no model', async ({ page }) => {
    await mockStudio(page, { types: ['images'] })
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    await prompt(page).fill('waves on black sand')
    await expect(generate(page)).toBeDisabled()
    await expect(page.locator('[data-testid="studio-why"]')).toHaveText('Install the Video model first')
  })

  test('the model select offers the installed models of the chosen type', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="studio-model"] option')).toHaveText(INSTALLED.images)
  })

  test('shows how the model fits this machine when the gallery can say', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="studio-fit"]')).toContainText('Needs 13.0 GB')
    await expect(page.locator('[data-testid="studio-fit"]')).toHaveAttribute('data-fit', 'fits')
  })

  test('shows no fit at all when nothing is known about the model', async ({ page }) => {
    await mockStudio(page)
    await page.route(/\/api\/models\/estimate\//, route => route.fulfill({ json: {} }))
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="studio-model"]')).toHaveValue('flux.1-schnell')
    await expect(page.locator('[data-testid="studio-fit"]')).toHaveCount(0)
  })
})

// What each workspace takes from the front page: the page opens with the form
// already holding the prompt and the options that were chosen.
test.describe('Studio composer: hand-off to the workspaces', () => {
  test('Images opens with the prompt, model, size and count filled in', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('A brass orrery on a walnut desk')
    await page.locator('[data-testid="studio-model"]').selectOption('sd-1.5-lcm')
    await page.locator('[data-testid="studio-size"]').selectOption('768x768')
    await page.locator('[data-testid="studio-count"]').selectOption('3')
    await generate(page).click()
    await expect(page).toHaveURL(/\/app\/studio\/images\?/)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue('A brass orrery on a walnut desk')
    await expect(page.locator('[data-testid="ws-size"]').first()).toHaveValue('768x768')
    await expect(page.locator('[data-testid="ws-count"]').first()).toHaveValue('3')
    await expect(page.locator('[data-testid="ws-compose"]')).toContainText('sd-1.5-lcm')
    // The page started from the hand-off but is still the normal workspace.
    await expect(page.getByRole('button', { name: /generate/i })).toBeVisible()
  })

  test('Video opens with the prompt, model and size', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    await prompt(page).fill('Waves rolling onto black sand, drone shot')
    await page.locator('[data-testid="studio-size"]').selectOption('1280x720')
    await generate(page).click()
    await expect(page).toHaveURL(/\/app\/studio\/video\?/)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue('Waves rolling onto black sand, drone shot')
    await expect(page.locator('[data-testid="ws-size"]').first()).toHaveValue('1280x720')
    await expect(page.locator('[data-testid="ws-compose"]')).toContainText('wan2.1-t2v-1.3b')
  })

  test('TTS opens with the words in the text box', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await chip(page, 'tts').click()
    await prompt(page).fill('Welcome to the harbour tour.')
    await generate(page).click()
    await expect(page).toHaveURL(/\/app\/studio\/tts\?/)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue('Welcome to the harbour tour.')
    await expect(page.locator('[data-testid="ws-compose"]')).toContainText('kokoro-82m')
  })

  test('Sound opens with the description in the simple prompt', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await chip(page, 'sound').click()
    await prompt(page).fill('Wind through pines, no music')
    await generate(page).click()
    await expect(page).toHaveURL(/\/app\/studio\/sound\?/)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue('Wind through pines, no music')
  })

  test('3D opens with the chosen picture as its input and the page says so', async ({ page }) => {
    await mockStudio(page)
    await stubMedia(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio')
    await chip(page, 'threed').click()
    await pickSource(page, /ceramic bowls/)
    await generate(page).click()
    await expect(page).toHaveURL(/\/app\/studio\/threed\?.*from=img-bowls.*edge=to-3d/)
    const note = page.locator('[data-testid="studio-handoff"]')
    await expect(note).toHaveAttribute('data-status', 'ready')
    await expect(note).toContainText('ceramic bowls')
    // The picture is in the input control, so a run can start at once.
    await expect(page.locator('[data-testid="ws-compose"] img').first()).toBeVisible()
  })

  test('Transform and Diarization open with the chosen recording as their file', async ({ page }) => {
    await mockStudio(page)
    await stubMedia(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio')
    for (const [key, edge] of [['transform', 'transform'], ['diarization', 'diarize']]) {
      await page.goto('/app/studio')
      await chip(page, key).click()
      await pickSource(page, /Welcome to the harbour tour/)
      await generate(page).click()
      await expect(page).toHaveURL(new RegExp(`/app/studio/${key}\\?.*edge=${edge}`))
      await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
    }
    // The file reached the Diarization form: its run button is enabled.
    await expect(page.getByRole('button', { name: 'Diarize' })).toBeEnabled()
  })

  test('a source whose file is gone is reported plainly and the page still works', async ({ page }) => {
    await mockStudio(page)
    await page.route('**/generated-audio/**', route => route.fulfill({ status: 404, body: '' }))
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio/diarization?from=tts-welcome&edge=diarize')
    const note = page.locator('[data-testid="studio-handoff"]')
    await expect(note).toHaveAttribute('data-status', 'error')
    await expect(note).toContainText('could not be loaded')
    await expect(page.locator('#diarization-file')).toBeVisible()
  })

  test('a source that is not in this browser is reported plainly', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio/video?from=gone&edge=animate')
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'missing')
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toBeVisible()
  })

  test('a workspace opened by hand has no hand-off note and an empty form', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio/images')
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue('')
  })

  test('a result made through a hand-off records which result it came from', async ({ page }) => {
    await mockStudio(page)
    await stubMedia(page)
    await seedHistory(page, sampleHistory())
    await page.route('**/video', route => route.fulfill({ json: { data: [{ url: '/generated-videos/new.mp4' }] } }))
    await page.goto('/app/studio/video?prompt=Slow%20push-in&from=img-forest&edge=animate&model=wan2.1-t2v-1.3b')
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
    await page.getByRole('button', { name: /generate/i }).click()
    await expect(page.locator('[data-testid="media-history-item"]').first()).toBeVisible()
    await expect.poll(async () => (await readStore(page, 'localai_video_history'))?.[0]?.parentId).toBe('img-forest')
    const saved = (await readStore(page, 'localai_video_history'))[0]
    expect(saved.edge).toBe('animate')
    expect(saved.prompt).toBe('Slow push-in')
    // And the front page now stacks the forest picture with its clip.
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="work-project"][data-id="img-forest"]')).toHaveAttribute('data-count', '2')
  })

  test('a result made without a hand-off records no link', async ({ page }) => {
    await mockStudio(page)
    await page.route('**/v1/images/generations', route => route.fulfill({ json: { data: [{ url: '/generated-images/solo.png' }] } }))
    await stubMedia(page)
    await page.goto('/app/studio/images')
    await page.locator('[data-testid="ws-compose"] textarea').first().fill('a lone picture')
    await page.getByRole('button', { name: /generate/i }).click()
    await expect.poll(async () => (await readStore(page, 'localai_image_history'))?.length).toBe(1)
    const saved = (await readStore(page, 'localai_image_history'))[0]
    expect(saved.parentId).toBeUndefined()
    expect(saved.edge).toBeUndefined()
  })

  test('a very long prompt is cut before it is stored', async ({ page }) => {
    await mockStudio(page)
    await stubMedia(page)
    await page.route('**/v1/images/generations', route => route.fulfill({ json: { data: [{ url: '/generated-images/long.png' }] } }))
    await page.goto('/app/studio/images')
    await page.locator('[data-testid="ws-compose"] textarea').first().fill('x'.repeat(5000))
    await page.getByRole('button', { name: /generate/i }).click()
    await expect.poll(async () => (await readStore(page, 'localai_image_history'))?.length).toBe(1)
    expect((await readStore(page, 'localai_image_history'))[0].prompt.length).toBe(2000)
  })

  test('a diarization run is listed with its file name and counts, never its audio', async ({ page }) => {
    await mockStudio(page)
    await page.route('**/v1/audio/diarization', route => route.fulfill({
      json: { segments: [{ speaker: 'SPEAKER_00', label: 0, start: 0, end: 4 }, { speaker: 'SPEAKER_01', label: 1, start: 4, end: 9 }], speakers: [{ label: 0, id: 'SPEAKER_00' }, { label: 1, id: 'SPEAKER_01' }] },
    }))
    await page.goto('/app/studio/diarization')
    await page.locator('#diarization-file').setInputFiles({ name: 'meeting.wav', mimeType: 'audio/wav', buffer: Buffer.alloc(64) })
    await page.getByRole('button', { name: 'Diarize' }).click()
    await expect.poll(async () => (await readStore(page, 'localai_diarization_history'))?.length).toBe(1)
    const saved = (await readStore(page, 'localai_diarization_history'))[0]
    expect(saved.prompt).toBe('meeting.wav')
    expect(saved.params).toEqual({ speakers: 2, seconds: 9 })
    expect(saved.results).toEqual([])
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="work-tile"][data-type="diarization"]')).toContainText('meeting.wav')
  })
})

test.describe('Studio composer: a model that is missing', () => {
  test('the missing type is a dashed chip that opens its note only when picked', async ({ page }) => {
    await mockStudio(page, { types: ['images', 'tts'] })
    await page.goto('/app/studio')
    await expect(chip(page, 'video')).toHaveAttribute('data-missing', 'true')
    await expect(chip(page, 'images')).not.toHaveAttribute('data-missing', 'true')
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveCount(0)
    await chip(page, 'video').click()
    const note = page.locator('[data-testid="studio-install-note"]')
    await expect(note).toBeVisible()
    await expect(note).toContainText('No Video model installed')
    // The smallest listed download, with what it needs and what is free.
    await expect(note).toContainText('wan2.1-t2v-1.3b')
    await expect(note).toContainText('5.4 GB to download')
    await expect(note).toContainText('6.0 GB of memory')
    await expect(note).toContainText('15.2 GB free now')
    await expect(note).toContainText('Nothing starts until you install it')
    await expect(note.getByRole('button', { name: 'Install wan2.1-t2v-1.3b' })).toBeVisible()
    // Switching back to a type with a model closes it.
    await chip(page, 'images').click()
    await expect(note).toHaveCount(0)
  })

  test('Install asks the gallery for that model, keeps the typed words, and the note goes when the model arrives', async ({ page }) => {
    const state = await mockStudio(page, { types: ['images'], installOnPost: true })
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    await prompt(page).fill('Waves rolling onto black sand, drone shot')
    await page.locator('[data-testid="studio-install"]').click()
    await expect.poll(() => state.installs).toEqual(['wan2.1-t2v-1.3b'])
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveAttribute('data-state', 'installing')
    await expect(prompt(page)).toHaveValue('Waves rolling onto black sand, drone shot')
    // The page polls while it installs; the model shows up and the note goes.
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveCount(0, { timeout: 15_000 })
    await expect(chip(page, 'video')).not.toHaveAttribute('data-missing', 'true')
    await expect(prompt(page)).toHaveValue('Waves rolling onto black sand, drone shot')
    await expect(generate(page)).toBeEnabled()
  })

  test('shows the progress of the install from the operations list', async ({ page }) => {
    await mockStudio(page, { types: ['images'], operations: [{ id: 'wan2.1-t2v-1.3b', name: 'wan2.1-t2v-1.3b', jobID: 'j1', progress: 42, isBackend: false, isDeletion: false, taskType: 'installation' }] })
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    const note = page.locator('[data-testid="studio-install-note"]')
    await expect(note).toContainText('Installing wan2.1-t2v-1.3b 42%')
    await expect(note.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '42')
    await expect(page.locator('[data-testid="studio-install"]')).toHaveCount(0)
  })

  test('a failed install says why, in words, and offers the button again', async ({ page }) => {
    await mockStudio(page, { types: ['images'], installStatus: 500 })
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    await page.locator('[data-testid="studio-install"]').click()
    await expect(page.getByRole('alert')).toContainText('The install did not start')
    await expect(page.locator('[data-testid="studio-install"]')).toBeVisible()
  })

  test('when the gallery lists nothing for a type it says so and points to the models page', async ({ page }) => {
    await mockStudio(page, { types: ['images'] })
    await page.route(/\/api\/models(\?.*)?$/, route => route.fulfill({ json: { models: [], total_pages: 1 } }))
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    const note = page.locator('[data-testid="studio-install-note"]')
    await expect(note).toContainText('The gallery lists no Video model')
    await expect(note.getByRole('link', { name: 'Browse all models' })).toHaveAttribute('href', /\/app\/models/)
  })

  test('when the gallery cannot be reached it says so', async ({ page }) => {
    await mockStudio(page, { types: ['images'] })
    await page.route(/\/api\/models(\?.*)?$/, route => route.fulfill({ status: 500, json: { error: 'down' } }))
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    await expect(page.locator('[data-testid="studio-install-note"]')).toContainText('could not be reached')
  })

  test('a download size the server does not know is not invented', async ({ page }) => {
    await mockStudio(page, { types: ['images'] })
    await page.route(/\/api\/models\/estimate\//, route => route.fulfill({ json: {} }))
    await page.goto('/app/studio')
    await chip(page, 'video').click()
    const note = page.locator('[data-testid="studio-install-note"]')
    await expect(note).toContainText('has no download size listed')
    await expect(note).toContainText('memory need is not listed')
  })

  test('a new user with no models at all gets one honest path, not a wall', async ({ page }) => {
    await mockStudio(page, { types: [] })
    await page.goto('/app/studio')
    await expect(page.locator('.studio-types .studio-type[data-missing="true"]')).toHaveCount(7)
    await expect(page.locator('[data-testid="studio-first-run"]')).toContainText('Nothing is installed yet')
    await expect(page.locator('[data-testid="studio-install-note"]')).toContainText('No Images model installed')
    await expect(page.locator('[data-testid="studio-model"]')).toBeDisabled()
    await expect(generate(page)).toBeDisabled()
  })

  test('a machine with one model has that type solid and the rest dashed', async ({ page }) => {
    await mockStudio(page, { types: ['tts'] })
    await page.goto('/app/studio')
    await expect(page.locator('.studio-types .studio-type[data-missing="true"]')).toHaveCount(6)
    // It opens on the first type that works, not on one that cannot.
    await expect(chip(page, 'tts')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveCount(0)
  })

  test('when the installed models cannot be read it says so and offers one action', async ({ page }) => {
    const state = await mockStudio(page, { capabilitiesStatus: 500 })
    await page.goto('/app/studio')
    const error = page.locator('[data-testid="studio-models-error"]')
    await expect(error).toContainText('could not read the installed models')
    await expect(error.getByRole('button')).toHaveCount(1)
    const before = state.capabilityCalls
    await error.getByRole('button', { name: 'Try again' }).click()
    await expect.poll(() => state.capabilityCalls).toBeGreaterThan(before)
    // Your work is not held up by it.
    await expect(page.locator('[data-testid="studio-work"]')).toBeVisible()
  })
})

test.describe('Studio composer: layout', () => {
  test('on a phone the composer fits the width and Generate stays reachable', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockStudio(page)
    await page.goto('/app/studio')
    await prompt(page).fill('a brass orrery')
    await expect(generate(page)).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const box = await generate(page).boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
  })

  test('while installed models load there is a skeleton, not a flash of "nothing installed"', async ({ page }) => {
    await mockStudio(page)
    await page.route('**/api/models/capabilities', async route => {
      await new Promise(r => setTimeout(r, 800))
      route.fulfill({ json: { data: [] } })
    })
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="studio-composer-loading"]')).toBeVisible()
    await expect(page.locator('[data-testid="studio-first-run"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-composer"]')).toBeVisible()
  })

  test('with reduced motion the tiles do not animate in', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockStudio(page)
    await stubMedia(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio')
    const name = await page.locator('[data-testid="work-tile"]').first().evaluate(el => getComputedStyle(el).animationName)
    expect(name).toBe('none')
  })
})

test.describe('Studio composer: draft survives', () => {
  test('the words typed are kept when a result is opened and closed', async ({ page }) => {
    await mockStudio(page)
    await stubMedia(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio')
    await prompt(page).fill('half a thought')
    await page.locator('[data-testid="work-tile"][data-id="img-forest"]').getByRole('button').first().click()
    await expect(page.locator('[data-testid="studio-lineage"]')).toBeVisible()
    await page.locator('[data-testid="studio-back"]').click()
    await expect(prompt(page)).toHaveValue('half a thought')
  })
})

test('3D history written by the 3D workspace shows on the front page', async ({ page }) => {
  await mockStudio(page)
  await page.goto('/app/studio')
  await seedThreeD(page, [{ id: 'm1', createdAt: Date.now() - 1000, name: 'vase.glb', label: 'Ribbed vase', model: 'trellis-image-to-3d', outputType: 'mesh', operation: 'generate' }])
  await page.reload()
  const tile = page.locator('[data-testid="work-tile"][data-type="threed"]')
  await expect(tile).toHaveCount(1)
  await expect(tile).toContainText('Ribbed vase')
})
