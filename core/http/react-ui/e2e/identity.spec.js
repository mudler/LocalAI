import { test, expect } from './coverage-fixtures.js'
import {
  FACE_KEY, VOICE_KEY, clipFile, faces, match, mockIdentity, people, photoFile,
} from './identity-fixtures.js'

const marta = match('v-marta', 'Marta Rossi', 0.18)
const jonas = match('v-jonas', 'Jonas Weber', 0.41)
const ilse = match('v-ilse', 'Ilse Janssen', 0.52)
const closeSet = { matches: [marta, jonas, ilse] }
const farSet = { matches: [match('v-jonas', 'Jonas Weber', 0.31), match('v-ilse', 'Ilse Janssen', 0.47), match('v-marta', 'Marta Rossi', 0.55)] }

async function addProbe(page, name = 'board-sync-clip.wav', seconds = 8) {
  await page.locator('#voice-probe-audio-file').setInputFiles(clipFile(name, seconds))
  await expect(page.getByTestId('voice-probe-clip')).toHaveAttribute('data-state', 'filled')
}

test.describe('Voices: Speakers', () => {
  test('empty: says nobody is enrolled and that the list lives in the browser', async ({ page }) => {
    await mockIdentity(page)
    await page.goto('/app/voice')
    await expect(page.getByRole('heading', { name: 'Voices', level: 1 })).toBeVisible()
    await expect(page.getByTestId('known-empty')).toContainText('Nobody enrolled in this browser yet')
    await expect(page.getByTestId('registry-note')).toContainText('kept in this browser')
    await expect(page.getByTestId('registry-note')).toContainText('no list call')
    await expect(page.getByTestId('identify-go')).toBeDisabled()
    // The Build bar keeps the Voices tab lit and has no second row.
    await expect(page.locator('.dk-hubtabs [data-hub-tab="voices"]')).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.hub-subnav')).toHaveCount(0)
  })

  test('who is this: a match is explained in words, on a scale, with the real numbers', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: people, identify: closeSet })
    await page.goto('/app/voice')
    await addProbe(page)
    await expect(page.getByText('good length')).toBeVisible()
    await page.getByTestId('identify-go').click()
    const verdict = page.getByTestId('verdict')
    await expect(verdict).toContainText('Probably Marta Rossi')
    await expect(verdict).toContainText('Likely match')
    await expect(verdict).toContainText('0.18 away')
    await expect(verdict).toContainText('under 0.25')
    await expect(page.getByTestId('distance-scale')).toBeVisible()
    await expect(page.getByTestId('rank-row')).toHaveCount(3)
    await expect(page.getByTestId('rank-row').first()).toContainText('within cut-off')
    await expect(page.getByTestId('cutoff-value')).toHaveText('0.25')
    // The request carries the model, the clip, a top_k that covers everyone, and the cut-off.
    expect(calls.identify).toHaveLength(1)
    expect(calls.identify[0]).toMatchObject({ model: 'wespeaker-resnet34', threshold: 0.25 })
    expect(calls.identify[0].audio).toMatch(/^data:audio\//)
    expect(calls.identify[0].top_k).toBeGreaterThanOrEqual(25)
    await expect(page.getByTestId('known-row-v-marta')).toContainText('best match')
  })

  test('the cut-off slider re-reads the answer without another call', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: people, identify: closeSet })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toContainText('Probably Marta Rossi')
    const slider = page.locator('#voice-find-cutoff')
    await slider.fill('0.1')
    await expect(page.getByTestId('cutoff-value')).toHaveText('0.10')
    await expect(page.getByTestId('verdict')).toContainText('Nobody enrolled sounds like this')
    await expect(page.getByRole('button', { name: 'Reset to 0.25' })).toBeVisible()
    await page.getByRole('button', { name: 'Reset to 0.25' }).click()
    await expect(page.getByTestId('verdict')).toContainText('Probably Marta Rossi')
    expect(calls.identify).toHaveLength(1)
  })

  test('no match: names the closest person and says why it may be wrong', async ({ page }) => {
    await mockIdentity(page, { stored: people, identify: farSet })
    await page.goto('/app/voice')
    await addProbe(page, 'hallway-recording.wav', 2.4)
    await expect(page.getByText('short', { exact: true })).toBeVisible()
    await page.getByTestId('identify-go').click()
    const verdict = page.getByTestId('verdict')
    await expect(verdict).toContainText('Nobody enrolled sounds like this')
    await expect(verdict).toContainText('closest was Jonas Weber at 0.31')
    await expect(page.getByRole('button', { name: 'Enrol this voice' })).toBeVisible()
    await page.getByText('Why this might be wrong').click()
    await expect(page.getByTestId('why-list')).toContainText('not a security check')
    await expect(page.getByTestId('why-list')).toContainText('short: 2.4 s')
  })

  test('working: a progress bar and a disabled button while the call is out', async ({ page }) => {
    await mockIdentity(page, { stored: people, identify: closeSet, delay: 1500 })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('identify-working')).toBeVisible()
    await expect(page.getByTestId('identify-working')).toHaveAttribute('aria-busy', 'true')
    await expect(page.getByTestId('identify-go')).toBeDisabled()
    await expect(page.getByTestId('verdict')).toBeVisible()
  })

  test('failed: plain words, the server message, a retry and a trace link', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: people, identify: { status: 500, body: { error: { message: 'backend crashed' } } } })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    const err = page.getByTestId('identity-error')
    await expect(err).toContainText('The call failed')
    await expect(err).toContainText('backend crashed')
    await expect(err.getByRole('link', { name: 'Open traces' })).toHaveAttribute('href', /\/app\/traces/)
    await err.getByRole('button', { name: 'Try again' }).click()
    await expect.poll(() => calls.identify.length).toBe(2)
  })

  test('failed: a missing model is named as such', async ({ page }) => {
    await mockIdentity(page, { stored: people, identify: { status: 404, body: { error: { message: 'model not found' } } } })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('identity-error')).toContainText('cannot find this model')
  })

  test('same person: uses the model cut-off from the answer and shows the pair on the scale', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: people, verify: { verified: false, distance: 0.31, threshold: 0.28, confidence: 0, model: 'x' } })
    await page.goto('/app/voice')
    await page.getByRole('tab', { name: 'Same person?' }).click()
    await page.locator('#voice-a-audio-file').setInputFiles(clipFile('a.wav'))
    await page.locator('#voice-b-audio-file').setInputFiles(clipFile('b.wav', 6))
    await page.getByTestId('compare-go').click()
    const verdict = page.getByTestId('verdict')
    await expect(verdict).toContainText('Probably different people')
    await expect(verdict).toContainText('0.31 apart')
    await expect(verdict).toContainText('under 0.28')
    expect(calls.verify[0]).toMatchObject({ model: 'wespeaker-resnet34' })
    expect(calls.verify[0]).not.toHaveProperty('threshold')
    await expect(page.getByTestId('cutoff-value')).toHaveText('0.28')
    await page.locator('#voice-pair-cutoff').fill('0.4')
    await expect(verdict).toContainText('Probably the same person')
    expect(calls.verify).toHaveLength(1)
  })

  test('mic blocked: says so and offers a file', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'mediaDevices', {
        value: { getUserMedia: () => Promise.reject(Object.assign(new Error('Permission denied'), { name: 'NotAllowedError' })) },
        configurable: true,
      })
    })
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await page.getByTestId('voice-probe-clip').getByRole('button', { name: 'Record' }).click()
    await expect(page.getByTestId('clip-notice')).toContainText('microphone is blocked')
    await expect(page.getByTestId('voice-probe-clip').getByRole('button', { name: 'Choose a file' })).toBeVisible()
    await expect(page.getByTestId('voice-probe-clip').getByRole('button', { name: 'Record' })).toHaveCount(0)
  })

  test('mic allowed: shows the state when the browser reports it', async ({ page }) => {
    await page.addInitScript(() => {
      const status = { state: 'granted', onchange: null }
      Object.defineProperty(navigator, 'permissions', { value: { query: () => Promise.resolve(status) }, configurable: true })
      Object.defineProperty(navigator, 'mediaDevices', { value: { getUserMedia: () => Promise.reject(new Error('unused')) }, configurable: true })
    })
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await expect(page.getByTestId('mic-allowed')).toContainText('Microphone allowed')
  })

  test('mic on an insecure origin is explained', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
      Object.defineProperty(navigator, 'mediaDevices', { value: undefined, configurable: true })
    })
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await expect(page.getByTestId('clip-notice')).toContainText('https or on localhost')
  })
})

test.describe('Voices: the registry tells the truth', () => {
  const priya = { id: 'v-priya', name: 'Priya Nair', labels: { team: 'platform' }, registeredAt: new Date(Date.now() - 86400000).toISOString() }

  test('a person the search did not return is marked, and a stranger is listed', async ({ page }) => {
    await mockIdentity(page, {
      stored: [...people, priya],
      identify: { matches: [marta, jonas, ilse, match('v-other', 'Omar Haddad', 0.6)] },
    })
    await page.goto('/app/voice')
    // Before a search nothing is claimed.
    await expect(page.getByTestId('not-on-server')).toHaveCount(0)
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toBeVisible()
    await expect(page.getByTestId('known-row-v-priya').getByTestId('not-on-server')).toContainText('not on the server')
    await expect(page.getByTestId('known-row-v-priya')).toContainText('may have restarted')
    await expect(page.getByTestId('known-row-v-marta').getByTestId('not-on-server')).toHaveCount(0)
    await expect(page.getByTestId('known-row-v-other').getByTestId('not-in-browser')).toBeVisible()
    await expect(page.getByTestId('registry-note')).toContainText('The last search checked it')
  })

  test('the server returning nobody is never read as proof when the search was cut short', async ({ page }) => {
    // 25 asked, 25 returned: the search may have missed people, so nothing is marked.
    const many = Array.from({ length: 25 }, (_, i) => match(`x-${i}`, `Person ${i}`, 0.3 + i / 100))
    await mockIdentity(page, { stored: [...people, priya], identify: { matches: many } })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toBeVisible()
    await expect(page.getByTestId('not-on-server')).toHaveCount(0)
  })

  test('an empty answer after a restart says the voices are gone', async ({ page }) => {
    await mockIdentity(page, { stored: people, identify: { matches: [] } })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toContainText('The server returned no speakers')
    await expect(page.getByTestId('verdict')).toContainText('enrol them again')
    await expect(page.getByTestId('not-on-server')).toHaveCount(3)
  })

  test('what is stored, and where, is stated', async ({ page }) => {
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await page.getByTestId('stored-note').getByText('What is stored, and where').click()
    const note = page.getByTestId('stored-note')
    await expect(note).toContainText('lost when it restarts')
    await expect(note).toContainText('The recording is not kept')
    await expect(note).toContainText('only if you tick Keep a copy')
  })
})

test.describe('Voices: enrol sheet', () => {
  test('lists what is still needed, then registers and saves only the metadata', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: [] })
    await page.goto('/app/voice')
    await page.getByTestId('enrol-open').click()
    const sheet = page.getByTestId('enrol-sheet')
    await expect(sheet).toBeVisible()
    const submit = sheet.getByRole('button', { name: /^Enrol/ })
    await expect(submit).toBeDisabled()
    await expect(page.getByTestId('enrol-needs')).toContainText('a recording, a name, permission')
    await page.locator('#voice-enrol-audio-file').setInputFiles(clipFile('x.wav', 5))
    await page.locator('#voice-enrol-name').fill('Marta Rossi')
    await page.locator('#voice-enrol-labels').fill('team: platform')
    await expect(page.getByTestId('enrol-needs')).toContainText('permission')
    await sheet.getByText('This person agreed').click()
    await expect(page.getByTestId('enrol-needs')).toHaveText('Ready to enrol.')
    await submit.click()
    await expect(sheet).toHaveCount(0)
    expect(calls.register[0]).toMatchObject({ model: 'wespeaker-resnet34', name: 'Marta Rossi', labels: { team: 'platform' } })
    expect(calls.register[0].audio).toMatch(/^data:audio\//)
    await expect(page.getByTestId('known-list')).toContainText('Marta Rossi')
    const stored = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), VOICE_KEY)
    expect(stored).toHaveLength(1)
    expect(stored[0]).toMatchObject({ name: 'Marta Rossi', labels: { team: 'platform' } })
    // The copy of the recording is opt-in; it was not ticked.
    expect(stored[0]).not.toHaveProperty('sampleUrl')
  })

  test('keeping a copy is opt-in and stays in the browser', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: [] })
    await page.goto('/app/voice')
    await page.getByTestId('enrol-open').click()
    await page.locator('#voice-enrol-audio-file').setInputFiles(clipFile('x.wav', 2))
    await page.locator('#voice-enrol-name').fill('Ada')
    const sheet = page.getByTestId('enrol-sheet')
    await sheet.getByText('This person agreed').click()
    await sheet.getByText('Keep a copy in this browser').click()
    await sheet.getByRole('button', { name: /^Enrol/ }).click()
    await expect(sheet).toHaveCount(0)
    const stored = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), VOICE_KEY)
    expect(stored[0].sampleUrl).toMatch(/^data:audio\//)
    expect(JSON.stringify(calls.register[0])).not.toContain('sampleUrl')
  })

  test('can also keep the recording as a speech voice (admin)', async ({ page }) => {
    const calls = await mockIdentity(page, { stored: [] })
    await page.goto('/app/voice')
    await page.getByTestId('enrol-open').click()
    const sheet = page.getByTestId('enrol-sheet')
    await page.locator('#voice-enrol-audio-file').setInputFiles(clipFile('x.wav', 3))
    await page.locator('#voice-enrol-name').fill('Marta Rossi')
    await sheet.getByText('Also keep it as a speech voice').click()
    await expect(page.getByTestId('enrol-needs')).toContainText('a transcript')
    await page.locator('#voice-enrol-transcript').fill('The words spoken.')
    await sheet.getByText('This person agreed').click()
    await sheet.getByRole('button', { name: /^Enrol/ }).click()
    await expect(sheet).toHaveCount(0)
    expect(calls.register).toHaveLength(1)
    expect(calls.profile).toHaveLength(1)
  })

  test('a failed enrol keeps the sheet and says why', async ({ page }) => {
    await mockIdentity(page, { stored: [] })
    await page.route('**/v1/voice/register', route => route.fulfill({ status: 500, json: { error: { message: 'store unavailable' } } }))
    await page.goto('/app/voice')
    await page.getByTestId('enrol-open').click()
    await page.locator('#voice-enrol-audio-file').setInputFiles(clipFile('x.wav', 2))
    await page.locator('#voice-enrol-name').fill('Ada')
    const sheet = page.getByTestId('enrol-sheet')
    await sheet.getByText('This person agreed').click()
    await sheet.getByRole('button', { name: /^Enrol/ }).click()
    await expect(sheet.getByTestId('identity-error')).toContainText('store unavailable')
    await expect(page.locator('#voice-enrol-name')).toHaveValue('Ada')
  })

  test('Escape closes the sheet', async ({ page }) => {
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await page.getByTestId('enrol-open').click()
    await expect(page.getByTestId('enrol-sheet')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('enrol-sheet')).toHaveCount(0)
  })
})

test.describe('Voices: delete with undo', () => {
  test('nothing is sent before the window ends, and Undo sends nothing', async ({ page }) => {
    await page.clock.install()
    const calls = await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await page.getByRole('button', { name: 'Remove Jonas Weber' }).click()
    await expect(page.getByTestId('forget-undo-toast')).toContainText('Jonas Weber will be removed')
    await expect(page.getByTestId('known-row-v-jonas')).toContainText('removing')
    await page.clock.runFor(9000)
    expect(calls.forget).toHaveLength(0)
    await page.getByTestId('forget-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await page.clock.runFor(20000)
    expect(calls.forget).toHaveLength(0)
    await expect(page.getByTestId('known-row-v-jonas')).toBeVisible()
    await expect(page.getByTestId('known-row-v-jonas')).not.toContainText('removing')
  })

  test('after ten seconds the forget call goes out and the row is gone', async ({ page }) => {
    await page.clock.install()
    const calls = await mockIdentity(page, { stored: people })
    await page.goto('/app/voice')
    await page.getByRole('button', { name: 'Remove Jonas Weber' }).click()
    await page.clock.runFor(9500)
    expect(calls.forget).toHaveLength(0)
    await page.clock.runFor(1500)
    await expect.poll(() => calls.forget.length).toBe(1)
    expect(calls.forget[0]).toEqual({ id: 'v-jonas' })
    await expect(page.getByTestId('known-row-v-jonas')).toHaveCount(0)
    const stored = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)).map(x => x.id), VOICE_KEY)
    expect(stored).toEqual(['v-marta', 'v-ilse'])
  })

  test('a 404 from the server still removes the row', async ({ page }) => {
    await page.clock.install()
    const calls = await mockIdentity(page, { stored: people, forgetStatus: 404 })
    await page.goto('/app/voice')
    await page.getByRole('button', { name: 'Remove Ilse Janssen' }).click()
    await page.clock.runFor(10500)
    await expect.poll(() => calls.forget.length).toBe(1)
    await expect(page.getByTestId('known-row-v-ilse')).toHaveCount(0)
  })
})

test.describe('Voices: states of the page', () => {
  test('disabled: no speaker model says what turns it on and offers installs', async ({ page }) => {
    await mockIdentity(page, {
      voiceModels: [],
      stored: people,
      gallery: [{ id: 'localai@wespeaker-resnet34', name: 'wespeaker-resnet34', backend: 'wespeaker', installed: false }],
    })
    let installed = null
    await page.route('**/api/models/install/**', route => { installed = decodeURIComponent(route.request().url().split('/').pop()); return route.fulfill({ json: { jobID: 'j' } }) })
    await page.goto('/app/voice')
    const needed = page.getByTestId('model-needed')
    await expect(needed).toContainText('Voices need a speaker model')
    await expect(needed).toContainText('Not enabled on this server')
    await expect(needed.getByRole('button', { name: 'Install' })).toBeVisible()
    await needed.getByText('What turns this on').click()
    await expect(needed).toContainText('Voice recognition permission')
    await needed.getByRole('button', { name: 'Install' }).click()
    await expect.poll(() => installed).toBe('localai@wespeaker-resnet34')
    // The remembered people stay manageable without a model.
    await expect(page.getByTestId('known-list')).toContainText('Marta Rossi')
    await expect(page.getByTestId('enrol-open')).toBeDisabled()
  })

  test('a user without the permission sees what is off, not a redirect', async ({ page }) => {
    await mockIdentity(page)
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, user: { id: 'u', role: 'user', permissions: {} } } }))
    await page.goto('/app/voice')
    await expect(page.getByTestId('feature-off')).toContainText('Voice recognition is off for your account')
    await expect(page.getByTestId('feature-off')).toContainText('Voice recognition permission')
    await page.goto('/app/face')
    await expect(page.getByTestId('feature-off')).toContainText('Face recognition is off for your account')
  })

  test('From a recording links to the diarization workspace', async ({ page }) => {
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice?tab=recording')
    await expect(page.getByTestId('recording-tab')).toContainText('Name the speakers in a recording')
    await page.getByRole('link', { name: /Open the diarization workspace/ }).click()
    await expect(page).toHaveURL(/\/app\/studio\/diarization/)
  })

  test('the route /app/voice/:model keeps working', async ({ page }) => {
    await mockIdentity(page, { stored: people })
    await page.goto('/app/voice/wespeaker-resnet34')
    await expect(page.getByRole('heading', { name: 'Voices', level: 1 })).toBeVisible()
    await expect(page.getByTestId('known-list')).toBeVisible()
  })

  test('phone: no sideways scroll with a result open, and the table stays inside the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockIdentity(page, { stored: [...people, { id: 'v-priya', name: 'Priya Nair', labels: {}, registeredAt: new Date().toISOString() }], identify: closeSet })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const box = await page.getByTestId('distance-scale').boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    await page.getByTestId('enrol-open').click()
    const sheet = await page.getByTestId('enrol-sheet').boundingBox()
    expect(sheet.width).toBeLessThanOrEqual(390)
    expect(sheet.x).toBeGreaterThanOrEqual(0)
  })

  test('reduced motion: the working bar and recording pulse do not animate', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockIdentity(page, { stored: people, identify: closeSet, delay: 2500 })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    const bar = page.getByTestId('identify-working').locator('.dk-progress-bar')
    await expect(bar).toBeVisible()
    const name = await bar.evaluate(el => getComputedStyle(el).animationName)
    expect(name).toBe('none')
  })

  test('no left rail marks on rows, cards or the verdict', async ({ page }) => {
    await mockIdentity(page, { stored: people, identify: closeSet })
    await page.goto('/app/voice')
    await addProbe(page)
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toBeVisible()
    const widths = await page.evaluate(() => [...document.querySelectorAll('.idn-page *')]
      .map(el => getComputedStyle(el))
      .filter(cs => parseFloat(cs.borderLeftWidth) >= 3 && cs.borderLeftStyle !== 'none' && parseFloat(cs.borderTopWidth) === 0)
      .length)
    expect(widths).toBe(0)
  })
})

test.describe('Faces', () => {
  const anna = match('f-anna', 'Anna Kowalski', 0.21)
  const tomas = match('f-tomas', 'Tomas Ribeiro', 0.52)

  test('who is this: a match, with the faceprint worded as such', async ({ page }) => {
    const calls = await mockIdentity(page, { storedFaces: faces, faceIdentify: { matches: [anna, tomas] } })
    await page.goto('/app/face')
    await expect(page.getByRole('heading', { name: 'Faces', level: 1 })).toBeVisible()
    await page.locator('#face-probe-image-file').setInputFiles(photoFile())
    await page.getByTestId('identify-go').click()
    const verdict = page.getByTestId('verdict')
    await expect(verdict).toContainText('Probably Anna Kowalski')
    await expect(verdict).toContainText('faceprint')
    await expect(verdict).toContainText('under 0.35')
    expect(calls.faceIdentify[0]).toMatchObject({ model: 'insightface-buffalo-l', threshold: 0.35 })
    expect(calls.faceIdentify[0].img).toMatch(/^data:image\//)
    await expect(page.getByTestId('known-row-f-anna')).toContainText('best match')
  })

  test('no face found: says what to try', async ({ page }) => {
    await mockIdentity(page, { storedFaces: faces, faceIdentify: { status: 400, body: { error: { message: 'no face detected in the image' } } } })
    await page.goto('/app/face')
    await page.locator('#face-probe-image-file').setInputFiles(photoFile())
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('identity-error')).toContainText('No face found in this photo')
    await expect(page.getByTestId('identity-error')).toContainText('front-facing')
  })

  test('same person: sends both photos, optional anti-spoofing, draws the faces it found', async ({ page }) => {
    const calls = await mockIdentity(page, { storedFaces: faces })
    await page.goto('/app/face')
    await page.getByRole('tab', { name: 'Same person?' }).click()
    await page.locator('#face-a-image-file').setInputFiles(photoFile('a.png'))
    await page.locator('#face-b-image-file').setInputFiles(photoFile('b.png'))
    await page.getByText('Check for photos of photos').click()
    await page.getByTestId('compare-go').click()
    await expect(page.getByTestId('verdict')).toContainText('Probably the same person')
    expect(calls.faceVerify[0]).toMatchObject({ anti_spoofing: true })
    expect(calls.faceVerify[0].img1).toMatch(/^data:image\//)
    await expect(page.locator('.biometrics-bbox')).toHaveCount(2)
    await expect(page.getByTestId('cutoff-value')).toHaveText('0.35')
  })

  test('enrol a person: a photo copy is opt-in', async ({ page }) => {
    const calls = await mockIdentity(page, { storedFaces: [] })
    await page.goto('/app/face')
    await expect(page.getByTestId('known-empty')).toContainText('Nobody enrolled in this browser yet')
    await page.getByTestId('enrol-open').click()
    const sheet = page.getByTestId('enrol-sheet')
    await page.locator('#face-enrol-image-file').setInputFiles(photoFile())
    await page.locator('#face-enrol-name').fill('Anna Kowalski')
    await sheet.getByText('This person agreed').click()
    await sheet.getByRole('button', { name: /^Enrol/ }).click()
    await expect(sheet).toHaveCount(0)
    expect(calls.faceRegister[0]).toMatchObject({ model: 'insightface-buffalo-l', name: 'Anna Kowalski' })
    const stored = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), FACE_KEY)
    expect(stored[0]).not.toHaveProperty('thumbnail')
    await expect(page.getByTestId('known-list')).toContainText('Anna Kowalski')
  })

  test('delete uses the face forget call after the window', async ({ page }) => {
    await page.clock.install()
    const calls = await mockIdentity(page, { storedFaces: faces })
    await page.goto('/app/face')
    await page.getByRole('button', { name: 'Remove Anna Kowalski' }).click()
    await page.clock.runFor(9000)
    expect(calls.forget).toHaveLength(0)
    await page.clock.runFor(2000)
    await expect.poll(() => calls.forget.length).toBe(1)
    expect(calls.forget[0]).toMatchObject({ face: true, id: 'f-anna' })
  })

  test('disabled: no face model', async ({ page }) => {
    await mockIdentity(page, { faceModels: [] })
    await page.goto('/app/face')
    await expect(page.getByTestId('model-needed')).toContainText('Faces need a face model')
  })

  test('more tools stay reachable: detect and the raw faceprint', async ({ page }) => {
    await mockIdentity(page, { storedFaces: faces })
    await page.goto('/app/face')
    await page.getByText('More tools').click()
    await expect(page.getByRole('tab', { name: /Detect and analyze/ })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Embedding' })).toBeVisible()
  })
})
