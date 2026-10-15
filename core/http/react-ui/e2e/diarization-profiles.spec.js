// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'

const profiles = { version: 1, encoder: { identity: `sha256:${'a'.repeat(64)}`, dimension: 2 }, speakers: [
  { speaker: 9, clean_duration: 0, intervals: [], unavailable_reason: 'insufficient_clean_speech' },
  { speaker: 0, clean_duration: 3, intervals: [{ start: 1, end: 2 }, { start: 4, end: 6 }], unavailable_reason: null, embedding: [1, 0] },
  { speaker: 7, clean_duration: 2, intervals: [{ start: 2, end: 4 }], unavailable_reason: null, embedding: [0, 1] },
  { speaker: 3, clean_duration: 2, intervals: [{ start: 6, end: 8 }], unavailable_reason: null, embedding: [0.6, 0.8] },
] }
const result = { speakers: [
  { id: 'SPEAKER_00', label: '7', name: 'Known' },
  { id: 'SPEAKER_01', label: '3' },
  { id: 'SPEAKER_02', label: '9' },
  { id: 'SPEAKER_03', label: '0' },
], segments: [
  { id: 0, speaker: 'SPEAKER_03', label: '0', start: 1, end: 2, text: 'First' },
  { id: 1, speaker: 'SPEAKER_01', label: '3', start: 6, end: 8, text: 'Second' },
  { id: 2, speaker: 'SPEAKER_03', label: '0', start: 4, end: 6, text: 'Third' },
], speaker_profiles: profiles }
const file = { name: 'meeting.wav', mimeType: 'audio/wav', buffer: Buffer.alloc(64) }
async function setup(page, permission = true, diarizationPermission = true) {
  await page.route('**/api/**', route => {
    const url = route.request().url()
    const data = url.endsWith('/auth/status') ? { authEnabled: true, user: { role: 'user', permissions: { audio_diarization: diarizationPermission, voice_recognition: permission } } }
      : url.endsWith('/models/capabilities') ? { data: ['diarizer', 'other'].map(id => ({ id, capabilities: ['FLAG_DIARIZATION'] })) } : {}
    return route.fulfill({ json: data })
  })
  await page.route('**/v1/audio/diarization', route => route.fulfill({ json: result }))
  await page.goto('/app/diarization')
  if (!diarizationPermission) return
  await expect(page.getByRole('button', { name: 'diarizer', exact: true })).toBeVisible()
  await page.getByLabel('Recording', { exact: true }).setInputFiles(file)
}
async function run(page) {
  await page.getByLabel('Prepare speakers to remember').check()
  await page.getByRole('button', { name: 'Diarize', exact: true }).click()
  await expect(page.getByTestId('speaker-0')).toBeVisible()
}
const speaker = (page, slot) => page.getByTestId(`speaker-${slot}`)

test('sparse raw slots, known/unavailable, explicit zero and duplicate names', async ({ page }) => {
  await setup(page)
  const requests = []
  await page.route('**/v1/voice/register', route => { requests.push(route.request().postDataJSON()); return route.fulfill({ json: { id: `id-${requests.length}`, name: 'Known', registered_at: '2026-01-01' } }) })
  const request = page.waitForRequest('**/v1/audio/diarization')
  await run(page)
  const body = (await request).postData()
  for (const value of ['include_speaker_profiles', 'include_text', 'verbose_json']) expect(body).toContain(value)
  await expect(speaker(page, 7)).toContainText('Known')
  await expect(speaker(page, 7).getByRole('button', { name: 'Name and remember' })).toHaveCount(0)
  await expect(speaker(page, 9)).toContainText('Not enough clean speech')
  await expect(speaker(page, 9).getByRole('button', { name: 'Name and remember' })).toBeDisabled()
  for (const slot of [0, 3]) {
    await speaker(page, slot).getByRole('button', { name: 'Name and remember' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Known')
    await page.getByRole('button', { name: 'Remember', exact: true }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(requests.at(-1)).toEqual({ model: 'diarizer', name: 'Known', speaker_slot: slot, speaker_profiles: profiles })
  }
  await expect(page.getByTestId('segments').getByText('Known', { exact: true })).toHaveCount(3)
  const stored = await page.evaluate(() => JSON.parse(localStorage.getItem('localai_voice_enrollments')))
  expect(stored.map(x => x.id)).toEqual(['id-2', 'id-1'])
  expect(JSON.stringify(stored)).not.toMatch(/embedding|speaker_profiles|sampleUrl/)
  await page.getByRole('link', { name: 'Manage remembered voices' }).click()
  await expect(page.getByRole('heading', { name: 'Known speakers' })).toBeVisible()
  await expect(page.getByTestId('known-list').locator('.dk-table-name', { hasText: 'Known' })).toHaveCount(2)
})

test('save failure preserves input, no premature relabel, duplicate submission disabled', async ({ page }) => {
  await setup(page); await run(page)
  let release
  await page.route('**/v1/voice/register', async route => { await new Promise(r => { release = r }); await route.fulfill({ status: 500, json: { error: 'Try again' } }) })
  await speaker(page, 0).getByRole('button', { name: 'Name and remember' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Ada')
  await page.getByRole('button', { name: 'Remember', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Saving…' })).toBeDisabled()
  await expect(speaker(page, 0)).not.toContainText('Ada')
  release()
  await expect(page.getByRole('dialog')).toContainText('Try again')
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Ada')
  await page.route('**/v1/voice/register', route => route.fulfill({ json: { id: 'ada', name: 'Ada' } }))
  await page.getByRole('button', { name: 'Remember', exact: true }).click()
  await expect(speaker(page, 0)).toContainText('Ada')
})

test('recognition permission does not block normal diarization', async ({ page }) => {
  await setup(page, false)
  await expect(page.getByLabel('Prepare speakers to remember')).toHaveCount(0)
  const req = page.waitForRequest('**/v1/audio/diarization')
  await page.getByRole('button', { name: 'Diarize', exact: true }).click()
  expect((await req).postData()).not.toContain('include_speaker_profiles')
  await expect(page.getByTestId('segments')).toContainText('First')
  await expect(page.getByRole('button', { name: 'Name and remember' })).toHaveCount(0)
})

test('preview uses clean intervals and revokes original object URL on replacement', async ({ page }) => {
  await page.addInitScript(() => {
    window.revoked = []
    const revoke = URL.revokeObjectURL.bind(URL)
    URL.revokeObjectURL = url => { window.revoked.push(url); revoke(url) }
    HTMLMediaElement.prototype.play = function () { window.playedAt = this.currentTime; return Promise.resolve() }
    HTMLMediaElement.prototype.pause = function () { window.paused = true }
  })
  await setup(page); await run(page)
  await speaker(page, 0).getByRole('button', { name: 'Preview 1' }).click()
  expect(await page.evaluate(() => window.playedAt)).toBe(1)
  const audio = page.locator('audio')
  const url = await audio.getAttribute('src')
  await audio.evaluate(el => { el.currentTime = 2.1; el.dispatchEvent(new Event('timeupdate')) })
  expect(await page.evaluate(() => window.paused)).toBe(true)
  await speaker(page, 0).getByRole('button', { name: 'Preview 2' }).click()
  expect(await page.evaluate(() => window.playedAt)).toBe(4)
  await page.getByLabel('Recording', { exact: true }).setInputFiles({ ...file, name: 'new.wav' })
  await expect(speaker(page, 0)).toHaveCount(0)
  await expect.poll(() => page.evaluate(url => window.revoked.includes(url), url)).toBe(true)
})

for (const change of ['recording', 'model']) test(`late inference and save cannot relabel changed ${change}`, async ({ page }) => {
  await setup(page)
  let release
  await page.route('**/v1/audio/diarization', async route => { await new Promise(r => { release = r }); await route.fulfill({ json: result }) })
  await page.getByLabel('Prepare speakers to remember').check()
  const req = page.waitForRequest('**/v1/audio/diarization')
  await page.getByRole('button', { name: 'Diarize', exact: true }).click(); await req
  const replace = async () => {
    if (change === 'recording') await page.getByLabel('Recording', { exact: true }).setInputFiles({ ...file, name: 'new.wav' })
    else { await page.getByRole('button', { name: 'diarizer', exact: true }).click(); await page.getByRole('option', { name: 'other', exact: true }).click() }
  }
  const inferenceResponse = page.waitForResponse('**/v1/audio/diarization')
  await replace(); release(); await inferenceResponse
  await expect(speaker(page, 0)).toHaveCount(0)
  await page.route('**/v1/audio/diarization', route => route.fulfill({ json: result }))
  await run(page)
  await page.route('**/v1/voice/register', async route => { await new Promise(r => { release = r }); await route.fulfill({ json: { id: 'late', name: 'Late' } }) })
  await speaker(page, 0).getByRole('button', { name: 'Name and remember' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Late')
  const save = page.waitForRequest('**/v1/voice/register')
  await page.getByRole('button', { name: 'Remember', exact: true }).click(); await save
  // Input state may change while a save is pending; simulate without dismissing it.
  if (change === 'recording') await page.getByLabel('Recording', { exact: true }).setInputFiles({ ...file, name: 'third.wav' })
  else {
    await page.getByRole('button', { name: 'other', exact: true }).evaluate(el => el.click())
    await page.getByRole('option', { name: /^diarizer/ }).evaluate(el => el.click())
  }
  const saveResponse = page.waitForResponse('**/v1/voice/register')
  release(); await saveResponse
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(speaker(page, 0)).toHaveCount(0)
})

test('unsupported export gives actionable error, never silently falls back', async ({ page }) => {
  await setup(page)
  await page.route('**/v1/audio/diarization', route => route.fulfill({ status: 501, json: { error: 'unsupported backend' } }))
  await page.getByLabel('Prepare speakers to remember').check()
  await page.getByRole('button', { name: 'Diarize', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Choose a profile-capable model')
  await expect(page.getByLabel('Prepare speakers to remember')).toBeChecked()
})


test('missing requested profiles is an error and normal defaults remain opt-in', async ({ page }) => {
  await setup(page)
  await expect(page.getByLabel('Prepare speakers to remember')).not.toBeChecked()
  await page.route('**/v1/audio/diarization', route => route.fulfill({ json: { ...result, speaker_profiles: undefined } }))
  await page.getByLabel('Prepare speakers to remember').check()
  await page.getByRole('button', { name: 'Diarize', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('did not return')
  await expect(page.getByTestId('segments')).toHaveCount(0)
})

test('diarization permission gates direct page and Studio tab', async ({ page }) => {
  await setup(page, true, false)
  await expect(page).toHaveURL(/\/app$/)
  await page.goto('/app/studio')
  await expect(page.locator('[data-tab="diarization"]')).toHaveCount(0)
})

test('Studio exposes diarization and navigation revokes recording URL', async ({ page }) => {
  await page.addInitScript(() => {
    window.revoked = []
    const revoke = URL.revokeObjectURL.bind(URL)
    URL.revokeObjectURL = url => { window.revoked.push(url); revoke(url) }
  })
  await setup(page)
  await page.goto('/app/studio/diarization')
  await expect(page.getByRole('heading', { name: 'Speaker diarization' })).toBeVisible()
  await page.getByLabel('Recording', { exact: true }).setInputFiles(file)
  await run(page)
  const url = await page.locator('audio').getAttribute('src')
  await page.screenshot({ path: 'test-results/diarization-profiles.png', fullPage: true })
  await page.getByRole('link', { name: 'Manage remembered voices' }).click()
  await expect.poll(() => page.evaluate(url => window.revoked.includes(url), url)).toBe(true)
})
