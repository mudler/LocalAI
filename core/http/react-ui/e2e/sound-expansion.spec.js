import { readFileSync } from 'node:fs'
import { parse as parseYaml } from 'yaml'
import { test, expect } from './coverage-fixtures.js'
import { mockStudio, silentWav } from './studio-fixtures.js'
const preference = 'localai_sound_expansion_model'
const chatPreference = 'localai_last_model:FLAG_CHAT'
const audio = { id: 'music', capabilities: ['FLAG_SOUND_GENERATION'] }
const chat = id => ({ id, capabilities: ['FLAG_CHAT'] })
const picker = page => page.getByRole('button', { name: 'Prompt expansion model', exact: true })
const choiceLabel = value => value === '__backend_default__' ? 'Backend default' : value || 'Choose a model or backend default'
async function choose(page, value) {
  await picker(page).click()
  await page.getByRole('listbox', { name: 'Prompt expansion model' }).getByRole('option').filter({ has: page.locator('code').getByText(choiceLabel(value), { exact: true }) }).click()
}
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
  await picker(page).click()
  await expect(page.getByRole('listbox', { name: 'Prompt expansion model' }).getByRole('option').locator('code')).toHaveText(['Backend default', 'writer', 'poet'])
  await page.keyboard.press('Escape')
  await choose(page, 'poet')
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).toMatchObject({ model_id: 'music', text: 'A quiet piano instrumental', instrumental: false, expansion_model: 'poet' })
  await page.reload()
  await expect(picker(page)).toContainText(choiceLabel('poet'))
  expect(await page.evaluate(key => localStorage.getItem(key), chatPreference)).toBe('general-chat')
  expect(await page.evaluate(key => localStorage.getItem(key), preference)).toBe('poet')
})
for (const stale of ['removed', 'embedding']) {
  test(`clears unavailable remembered model ${stale} and asks for a choice`, async ({ page }) => {
    await setup(page, [audio, chat('writer'), { id: 'embedding', capabilities: ['FLAG_EMBEDDINGS'] }], stale)
    await prompt(page)
    await expect(picker(page)).toContainText(choiceLabel(''))
    await expect(submit(page)).toBeDisabled()
    await expect(page.getByText('Choose a chat model or Backend default to continue.', { exact: true })).toBeVisible()
    expect(await page.evaluate(key => localStorage.getItem(key), preference)).toBeNull()
  })
}
test('revalidates a selected model when the installed list changes', async ({ page }) => {
  await setup(page, [audio, chat('writer'), chat('poet')], 'writer')
  await expect(picker(page)).toContainText(choiceLabel('writer'))
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [audio, chat('poet')] } }))
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(picker(page)).toContainText(choiceLabel(''))
  await prompt(page)
  await expect(submit(page)).toBeDisabled()
  expect(await page.evaluate(key => localStorage.getItem(key), preference)).toBeNull()
})
test('Backend default works without chat models, omits expansion_model and survives reload', async ({ page }) => {
  const requests = await setup(page, [audio])
  await prompt(page)
  await expect(submit(page)).toBeDisabled()
  await expect(page.getByText('No installed chat models. Choose Backend default to use the audio backend.', { exact: true })).toBeVisible()
  await choose(page, '__backend_default__')
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).not.toHaveProperty('expansion_model')
  await page.reload()
  await expect(picker(page)).toContainText(choiceLabel('__backend_default__'))
  expect(await page.evaluate(key => localStorage.getItem(key), chatPreference)).toBe('general-chat')
})
test('Advanced preserves all fields and never sends the remembered expansion model', async ({ page }) => {
  const requests = await setup(page, undefined, 'writer')
  await expect(picker(page)).toContainText(choiceLabel('writer'))
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
  await choose(page, 'writer')
  await submit(page).click()
  const tile = page.getByTestId('media-history-item').first()
  await expect(tile).toBeVisible()
  await choose(page, 'poet')
  await tile.locator('.ws-tile__open').click()
  await page.getByTestId('ws-rerun').click()
  await expect(picker(page)).toContainText(choiceLabel('writer'))
  await choose(page, 'poet')
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
    await expect(picker(page)).toContainText(choiceLabel(historical ? '' : '__backend_default__'))
    if (historical) await expect(submit(page)).toBeDisabled()
    else await expect(submit(page)).toBeEnabled()
  })
}

test('shared chip supports keyboard, focus return and dismissal without choosing', async ({ page }) => {
  await setup(page)
  const chip = picker(page)
  await expect(chip).toHaveAttribute('id', 'sound-expansion-model')
  await expect(chip).toHaveAttribute('aria-describedby', /(?:^| )sound-expansion-help(?: |$)/)
  await chip.press('ArrowDown')
  const list = page.getByRole('listbox', { name: 'Prompt expansion model' })
  await expect(list).toBeVisible()
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(chip).toContainText('writer')
  await expect(chip).toBeFocused()
  await chip.click()
  await page.keyboard.press('Escape')
  await expect(list).toHaveCount(0)
  await expect(chip).toBeFocused()
  await chip.click()
  await page.keyboard.press('Tab')
  await expect(list).toHaveCount(0)
  await chip.click()
  await page.locator('[data-testid="ws-compose"] textarea').first().click()
  await expect(list).toHaveCount(0)
  await expect(chip).toContainText('writer')
})

for (const theme of ['dark', 'light']) {
  test(`searches long IDs without clipping on a narrow ${theme} viewport`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await setup(page, [audio, ...Array.from({ length: 8 }, (_, i) => chat(`writer-${i}-${'long-model-name-'.repeat(5)}`))])
    await page.evaluate(theme => document.documentElement.setAttribute('data-theme', theme), theme)
    await picker(page).click()
    const search = page.getByRole('combobox')
    await search.fill('no-such-model')
    await expect(page.getByRole('option')).toHaveCount(0)
    await expect(page.getByText('No model matches.', { exact: false })).toBeVisible()
    await search.fill('writer-7')
    await expect(page.getByRole('option')).toHaveCount(1)
    const menu = page.locator('.home-menu--models')
    const box = await menu.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    await page.keyboard.press('Enter')
    await expect(picker(page)).toContainText('writer-7')
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    if (process.env.SOUND_PICKER_EVIDENCE) {
      await picker(page).click()
      await page.screenshot({ path: `${process.env.SOUND_PICKER_EVIDENCE}/sound-${theme}-narrow.png`, fullPage: true })
    }
  })
}

test('Backend default remains usable after a model list error', async ({ page }) => {
  const requests = await setup(page)
  await page.route('**/api/models/capabilities', route => route.fulfill({ status: 500, json: { error: 'offline' } }))
  await page.route('**/v1/models', route => route.fulfill({ status: 500, json: { error: 'offline' } }))
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(page.locator('#sound-expansion-help')).toHaveText('Cannot load chat models. Choose Backend default or return to this tab to retry.')
  await choose(page, '__backend_default__')
  await prompt(page)
  await expect(submit(page)).toBeEnabled()
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).not.toHaveProperty('expansion_model')
})


test('initial model loading disables the picker until the response resolves', async ({ page }) => {
  const requests = await setup(page, undefined, 'writer')
  let release
  const responseReady = new Promise(resolve => { release = resolve })
  await page.route('**/api/models/capabilities', async route => {
    await responseReady
    await route.fulfill({ json: { data: [audio, chat('writer'), chat('poet')] } })
  })
  await page.reload()
  try {
    await expect(picker(page)).toBeDisabled()
    await expect(page.locator('#sound-expansion-help')).toHaveText('Loading installed chat models…')
    await prompt(page)
    await expect(submit(page)).toBeDisabled()
  } finally {
    release()
  }
  await expect(picker(page)).toBeEnabled()
  await expect(picker(page)).toContainText('writer')
  await choose(page, 'poet')
  await expect(submit(page)).toBeEnabled()
  await submit(page).click()
  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).toHaveProperty('expansion_model', 'poet')
})

test('Sound shows no unverified load status on any expansion model row or the chip', async ({ page }) => {
  await setup(page)
  await picker(page).click()
  const options = page.getByRole('listbox', { name: 'Prompt expansion model' }).getByRole('option')
  await expect(options.locator('code')).toHaveText(['Backend default', 'writer', 'poet'])
  await expect(options.locator('.home-dot')).toHaveCount(0)
  await expect(options.locator('small')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await choose(page, 'writer')
  await expect(picker(page).locator('.home-dot')).toHaveCount(0)
})

test('the stable picker label describes its current choice and retains the help association', async ({ page }) => {
  await setup(page)
  const chip = picker(page)
  const help = 'A chat model expands your description into style and lyrics. Backend default uses the audio backend’s existing prompt behavior.'
  await expect(chip).toHaveAccessibleName('Prompt expansion model')
  await expect(chip).toHaveAccessibleDescription(`${choiceLabel('')} ${help}`)
  await choose(page, 'writer')
  await expect(chip).toHaveAccessibleName('Prompt expansion model')
  await expect(chip).toHaveAccessibleDescription(`writer ${help}`)
  await choose(page, '__backend_default__')
  await expect(chip).toHaveAccessibleName('Prompt expansion model')
  await expect(chip).toHaveAccessibleDescription(`Backend default ${help}`)
  await expect(chip).toHaveAttribute('aria-describedby', /(?:^| )sound-expansion-help(?: |$)/)
})
