import { readFileSync } from 'node:fs'
import { parse as parseYaml } from 'yaml'
import { test, expect } from './coverage-fixtures.js'
import { mockStudio, silentWav } from './studio-fixtures.js'
const preference = 'localai_sound_expansion_model'
const chatPreference = 'localai_last_model:FLAG_CHAT'
const audio = { id: 'music', capabilities: ['FLAG_SOUND_GENERATION'] }
const chat = id => ({ id, capabilities: ['FLAG_CHAT'] })
const picker = page => page.getByLabel('Prompt expansion model', { exact: true })
const submit = page => page.locator('[data-testid="ws-compose"] button[type="submit"]')
async function setup(page, models = [audio, chat('writer'), chat('poet')], saved) {
  await page.route('**/api/**', route => route.fulfill({ json: {} }))
  await mockStudio(page, { capabilityModels: models })
  await page.addInitScript(({ preference, chatPreference, saved }) => {
    if (!sessionStorage.getItem('sound-test-seeded')) {
      localStorage.setItem(chatPreference, 'general-chat')
      if (saved !== undefined) localStorage.setItem(preference, saved)
      sessionStorage.setItem('sound-test-seeded', '1')
    }
  }, { preference, chatPreference, saved })
  const requests = []
  await page.route('**/v1/sound-generation', route => {
    requests.push(route.request().postDataJSON())
    return route.fulfill({ contentType: 'audio/wav', body: silentWav() })
  })
  await page.goto('/app/studio/sound')
  await expect(page.getByRole('button', { name: 'music', exact: true })).toBeVisible()
  return requests
}
async function prompt(page) { await page.locator('[data-testid="ws-compose"] textarea').first().fill('A quiet piano instrumental') }

test('requires a choice, filters installed chat models and remembers only the Sound preference', async ({ page }) => {
  const requests = await setup(page, [audio, chat('writer'), chat('poet'), { id: 'embedding', capabilities: ['FLAG_EMBEDDINGS'] }, { ...chat('disabled'), disabled: true }])
  await prompt(page)
  await expect(submit(page)).toBeDisabled()
  await expect(picker(page).locator('option')).toHaveText(['Choose a model or backend default', 'Backend default', 'writer', 'poet'])
  await picker(page).selectOption('poet')
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).toMatchObject({ model_id: 'music', text: 'A quiet piano instrumental', instrumental: false, expansion_model: 'poet' })
  await page.reload()
  await expect(picker(page)).toHaveValue('poet')
  expect(await page.evaluate(key => localStorage.getItem(key), chatPreference)).toBe('general-chat')
  expect(await page.evaluate(key => localStorage.getItem(key), preference)).toBe('poet')
})
for (const stale of ['removed', 'embedding']) {
  test(`clears unavailable remembered model ${stale} and asks for a choice`, async ({ page }) => {
    await setup(page, [audio, chat('writer'), { id: 'embedding', capabilities: ['FLAG_EMBEDDINGS'] }], stale)
    await prompt(page)
    await expect(picker(page)).toHaveValue('')
    await expect(submit(page)).toBeDisabled()
    await expect(page.getByText('Choose a chat model or Backend default to continue.', { exact: true })).toBeVisible()
    expect(await page.evaluate(key => localStorage.getItem(key), preference)).toBeNull()
  })
}
test('revalidates a selected model when the installed list changes', async ({ page }) => {
  await setup(page, [audio, chat('writer'), chat('poet')], 'writer')
  await expect(picker(page)).toHaveValue('writer')
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [audio, chat('poet')] } }))
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(picker(page)).toHaveValue('')
  await prompt(page)
  await expect(submit(page)).toBeDisabled()
  expect(await page.evaluate(key => localStorage.getItem(key), preference)).toBeNull()
})
test('Backend default works without chat models, omits expansion_model and survives reload', async ({ page }) => {
  const requests = await setup(page, [audio])
  await prompt(page)
  await expect(submit(page)).toBeDisabled()
  await expect(page.getByText('No installed chat models. Choose Backend default to use the audio backend.', { exact: true })).toBeVisible()
  await picker(page).selectOption('__backend_default__')
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).not.toHaveProperty('expansion_model')
  await page.reload()
  await expect(picker(page)).toHaveValue('__backend_default__')
  expect(await page.evaluate(key => localStorage.getItem(key), chatPreference)).toBe('general-chat')
})
test('Advanced preserves all fields and never sends the remembered expansion model', async ({ page }) => {
  const requests = await setup(page, undefined, 'writer')
  await expect(picker(page)).toHaveValue('writer')
  await page.getByRole('tab', { name: 'Advanced', exact: true }).click()
  await expect(picker(page)).toHaveCount(0)
  await page.locator('#sound-caption').fill('Warm acoustic folk')
  await page.locator('#sound-lyrics').fill('Hello morning')
  await page.getByTestId('ws-duration').fill('42.5')
  await page.getByRole('button', { name: /More options/ }).click()
  await page.locator('#sound-bpm').fill('72')
  await page.locator('#sound-key').fill('C minor')
  await page.locator('#sound-language').fill('en')
  await page.locator('#sound-timesig').fill('4/4')
  await page.locator('.ws-check input').check()
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).toEqual({ model_id: 'music', caption: 'Warm acoustic folk', lyrics: 'Hello morning', think: true, bpm: 72, duration_seconds: 42.5, keyscale: 'C minor', language: 'en', timesignature: '4/4' })
})

// All published schema surfaces must describe the opt-in field consistently.
test('published Sound schemas expose expansion_model as an optional string', () => {
  const read = name => readFileSync(new URL(`../../../../swagger/${name}`, import.meta.url), 'utf8')
  const generated = read('docs.go').split('"schema.ElevenLabsSoundGenerationRequest":')[1].split('"schema.ErrorResponse":')[0].trim().replace(/,$/, '')
  const schemas = [
    JSON.parse(read('swagger.json')),
    parseYaml(read('swagger.yaml')),
    { definitions: { 'schema.ElevenLabsSoundGenerationRequest': JSON.parse(generated) } },
  ]
  for (const schema of schemas) {
    const sound = schema.definitions['schema.ElevenLabsSoundGenerationRequest']
    expect(sound.properties.expansion_model).toEqual({ type: 'string' })
    expect(sound.required || []).not.toContain('expansion_model')
  }
})

test('Re-run with edits restores the original expansion choice and tracks edits', async ({ page }) => {
  await setup(page)
  await page.route('**/v1/sound-generation', route => route.fulfill({
    contentType: 'audio/wav', headers: { 'Content-Disposition': 'attachment; filename="piano.wav"' }, body: silentWav(),
  }))
  await prompt(page)
  await picker(page).selectOption('writer')
  await submit(page).click()
  const tile = page.getByTestId('media-history-item').first()
  await expect(tile).toBeVisible()
  await picker(page).selectOption('poet')
  await tile.locator('.ws-tile__open').click()
  await page.getByTestId('ws-rerun').click()
  await expect(picker(page)).toHaveValue('writer')
  await picker(page).selectOption('poet')
  await expect(page.getByTestId('ws-changes')).toContainText('Prompt expansion model writer to poet')
})

for (const historical of [undefined, 'removed']) {
  test(`Re-run keeps ${historical ? 'an unavailable model unresolved' : 'legacy history on Backend default'}`, async ({ page }) => {
    await setup(page, undefined, 'poet')
    await page.evaluate(expansionModel => localStorage.setItem('localai_sound_history', JSON.stringify([{
      id: 'old-sound', createdAt: Date.now(), model: 'music', prompt: 'An old piano take',
      params: { mode: 'simple', expansionModel }, results: [{ url: '/generated-audio/old.wav' }],
    }])), historical)
    await page.reload()
    await page.getByTestId('media-history-item').first().locator('.ws-tile__open').click()
    await page.getByTestId('ws-rerun').click()
    await expect(picker(page)).toHaveValue(historical ? '' : '__backend_default__')
    if (historical) await expect(submit(page)).toBeDisabled()
    else await expect(submit(page)).toBeEnabled()
  })
}
