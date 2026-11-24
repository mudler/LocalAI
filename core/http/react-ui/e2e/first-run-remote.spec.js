import { test, expect } from './coverage-fixtures.js'
import { mockHome, mockUser } from './home-fixtures.js'
import { mockRecommended, recommendedAnswer } from './recommended-fixtures.js'
import { mockRemotes, remote, TEST_OK, TEST_FAIL, LOCALAI_MODELS, CLOUD_MODELS, CLOUD_BARE } from './remotes-fixtures.js'

// First run, with the choice "where do your models run". On this machine keeps
// the steps of today. On another server or service swaps steps 1 and 2 for
// "connect a remote" and "choose models".

async function firstRun(page, options = {}) {
  await mockHome(page, { loaded: [], models: [], chatModels: [] })
  await mockRecommended(page, recommendedAnswer())
  const state = await mockRemotes(page, options)
  await page.goto('/app')
  await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 15_000 })
  return state
}

const where = (page, id) => page.getByTestId(`home-where-${id}`)
const kind = (page, id) => page.getByTestId(`remote-kind-${id}`)

async function goRemote(page) {
  await where(page, 'remote').click()
  await expect(page.getByTestId('home-step-connect')).toBeVisible()
}

test.describe('First run: where models run', () => {
  test('on this machine is the default and the steps are today\'s', async ({ page }) => {
    await firstRun(page)
    await expect(where(page, 'local')).toHaveAttribute('aria-checked', 'true')
    await expect(where(page, 'remote')).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('home-first-run')).toContainText('Browse the Model Gallery')
    await expect(page.getByTestId('home-first-run')).toContainText('Install a Model')
    await expect(page.getByTestId('starter-models')).toBeVisible()
    await expect(page.getByTestId('home-step-connect')).toHaveCount(0)
    await expect(page.getByTestId('home-remote-intro')).toHaveCount(0)
  })

  test('another server swaps steps 1 and 2 and keeps the rest', async ({ page }) => {
    await firstRun(page)
    await goRemote(page)
    const guide = page.getByTestId('home-first-run')
    await expect(guide).toContainText('Connect a remote')
    await expect(guide).toContainText('Choose models')
    await expect(guide).not.toContainText('Browse the Model Gallery')
    await expect(page.getByTestId('starter-models')).toHaveCount(0)
    await expect(guide).toContainText('Start Chatting')
    await expect(page.getByTestId('home-remote-intro')).toHaveText('Connect a model that already runs somewhere else. You can install local models later.')
    await expect(guide).toContainText('recommended-model and context-size checks are skipped')
    // Back to this machine restores the original steps.
    await where(page, 'local').click()
    await expect(guide).toContainText('Browse the Model Gallery')
    await expect(page.getByTestId('starter-models')).toBeVisible()
  })

  test('the choice is a radio group the keyboard can operate', async ({ page }) => {
    await firstRun(page)
    const group = page.getByRole('radiogroup', { name: 'Where do your models run?' })
    await expect(group).toBeVisible()
    await where(page, 'local').focus()
    await page.keyboard.press('ArrowRight')
    await expect(where(page, 'remote')).toBeFocused()
    await expect(where(page, 'remote')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('home-step-connect')).toBeVisible()
    await page.keyboard.press('ArrowLeft')
    await expect(where(page, 'local')).toHaveAttribute('aria-checked', 'true')
  })

  test('a LocalAI remote: address, optional key, test, every model ticked, connect', async ({ page }) => {
    const state = await firstRun(page, { test: TEST_OK(LOCALAI_MODELS) })
    await goRemote(page)
    await expect(kind(page, 'localai')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('remote-url')).toBeVisible()
    await expect(page.getByTestId('remote-pii')).toHaveCount(0)
    await expect(page.getByText('(optional)')).toBeVisible()
    await expect(page.getByTestId('remote-test')).toBeDisabled()
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-ok')).toContainText('Connected. Found 5 models.')
    // The answer takes focus so a keyboard user hears it.
    await expect(page.getByTestId('remote-result')).toBeFocused()
    const boxes = page.getByTestId('remote-models').getByRole('checkbox')
    await expect(boxes).toHaveCount(5)
    for (let i = 0; i < 5; i++) await expect(boxes.nth(i)).toBeChecked()
    await expect(page.getByTestId('remote-count')).toHaveText('5 of 5 selected')
    await expect(page.getByTestId('remote-models')).toContainText('Embeddings')
    await expect(page.getByTestId('remote-models')).toContainText('Transcription')
    await boxes.nth(4).uncheck()
    await expect(page.getByTestId('remote-count')).toHaveText('4 of 5 selected')
    await expect(page.getByTestId('remote-name')).toHaveAttribute('placeholder', 'office.example.com')
    // The server lists the new entries from now on.
    await page.route('**/v1/models', route => route.fulfill({ json: { data: LOCALAI_MODELS.slice(0, 4).map(m => ({ id: m.id })) } }))
    await page.getByTestId('remote-connect').click()
    const create = state.calls.find(c => c.method === 'POST' && c.path === '/')
    expect(create.body).toEqual({
      kind: 'localai', name: 'office.example.com', url: 'https://office.example.com', api_key: '',
      models: LOCALAI_MODELS.slice(0, 4),
    })
    // The models exist now, so the guide gives way to the page.
    await expect(page.getByTestId('home-first-run')).toHaveCount(0, { timeout: 15_000 })
    await expect(page.getByText('Connected office.example.com. Added 4 models.')).toBeVisible()
  })

  test('an OpenAI-compatible remote: presets fill the address, no model is ticked, the filter line shows', async ({ page }) => {
    const state = await firstRun(page, { test: TEST_OK(CLOUD_MODELS) })
    await goRemote(page)
    await kind(page, 'openai').click()
    await expect(page.getByTestId('remote-preset')).toBeVisible()
    await expect(page.getByTestId('remote-preset-openai')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('remote-url')).toHaveValue('https://api.openai.com/v1')
    await page.getByTestId('remote-preset-openrouter').click()
    await expect(page.getByTestId('remote-url')).toHaveValue('https://openrouter.ai/api/v1')
    await page.getByTestId('remote-preset-groq').click()
    await expect(page.getByTestId('remote-url')).toHaveValue('https://api.groq.com/openai/v1')
    await page.getByTestId('remote-preset-custom').click()
    await expect(page.getByTestId('remote-url')).toHaveValue('')
    await expect(page.getByTestId('remote-pii')).toContainText('PII filter')
    await expect(page.getByTestId('remote-key')).toHaveAttribute('type', 'password')
    await expect(page.getByTestId('remote-key')).toHaveValue('')
    await expect(page.getByText('(optional)')).toHaveCount(0)
    await page.getByTestId('remote-preset-openai').click()
    await expect(page.getByTestId('remote-test')).toBeDisabled()
    await page.getByTestId('remote-key').fill('sk-test-123')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-ok')).toBeVisible()
    expect(state.calls.find(c => c.path === '/test').body).toEqual({ kind: 'openai', url: 'https://api.openai.com/v1', api_key: 'sk-test-123' })
    const boxes = page.getByTestId('remote-models').getByRole('checkbox')
    await expect(boxes).toHaveCount(3)
    for (let i = 0; i < 3; i++) await expect(boxes.nth(i)).not.toBeChecked()
    await expect(page.getByTestId('remote-connect')).toBeDisabled()
    await boxes.nth(1).check()
    await expect(page.getByTestId('remote-connect')).toBeEnabled()
    await page.getByTestId('remote-connect').click()
    const create = state.calls.find(c => c.method === 'POST' && c.path === '/')
    expect(create.body.name).toBe('OpenAI')
    expect(create.body.models).toEqual([CLOUD_MODELS[1]])
  })

  test('an Anthropic remote needs only a key and hides the address', async ({ page }) => {
    const state = await firstRun(page, { test: TEST_OK(CLOUD_MODELS) })
    await goRemote(page)
    await kind(page, 'anthropic').click()
    await expect(page.getByTestId('remote-url')).toHaveCount(0)
    await expect(page.getByTestId('remote-preset')).toHaveCount(0)
    await expect(page.getByTestId('remote-pii')).toBeVisible()
    await page.getByTestId('remote-key').fill('key-abc')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-ok')).toBeVisible()
    const sent = state.calls.find(c => c.path === '/test').body
    expect(sent.kind).toBe('anthropic')
    // The server fills in the Anthropic address when none is sent.
    expect(sent).not.toHaveProperty('url')
  })

  for (const [step, words] of [
    ['invalid_url', 'That address is not valid'],
    ['unreachable', 'check the address and that the other server is running and reachable from this server'],
    ['auth', 'The other LocalAI server rejected the key'],
    ['no_models', 'lists no models to add'],
  ]) {
    test(`a failed test (${step}) says what failed in plain words`, async ({ page }) => {
      await firstRun(page, { test: TEST_FAIL(step) })
      await goRemote(page)
      await page.getByTestId('remote-url').fill('https://office.example.com')
      await page.getByTestId('remote-test').click()
      const fail = page.getByTestId('remote-fail')
      await expect(fail).toBeVisible()
      await expect(fail).toContainText(new RegExp(words, 'i'))
      await expect(fail).toHaveAttribute('role', 'alert')
      await expect(page.getByTestId('remote-result')).toBeFocused()
      await expect(page.getByTestId('remote-choose-empty')).toBeVisible()
    })
  }

  test('a cloud service that rejects the key says "provider", a LocalAI server says so in its own words', async ({ page }) => {
    await firstRun(page, { test: TEST_FAIL('auth') })
    await goRemote(page)
    await kind(page, 'openai').click()
    await page.getByTestId('remote-key').fill('sk-bad')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-fail')).toContainText('The provider rejected the key')
    await expect(page.getByTestId('remote-fail')).not.toContainText('LocalAI server')
  })

  test('changing the address drops the test result and the models', async ({ page }) => {
    await firstRun(page)
    await goRemote(page)
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-models')).toBeVisible()
    await page.getByTestId('remote-url').fill('https://other.example.com')
    await expect(page.getByTestId('remote-models')).toHaveCount(0)
    await expect(page.getByTestId('remote-ok')).toHaveCount(0)
  })

  test('a name that is taken is reported next to the button', async ({ page }) => {
    await firstRun(page, { status: { 'POST /': { status: 409, message: 'name taken' } } })
    await goRemote(page)
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await page.getByTestId('remote-connect').click()
    await expect(page.getByTestId('remote-create-error')).toContainText('already exists')
    await expect(page.getByTestId('remote-connect')).toBeEnabled()
  })

  test('the kind radios work with the keyboard', async ({ page }) => {
    await firstRun(page)
    await goRemote(page)
    await kind(page, 'localai').focus()
    await page.keyboard.press('ArrowRight')
    await expect(kind(page, 'openai')).toBeFocused()
    await expect(kind(page, 'openai')).toHaveAttribute('aria-checked', 'true')
    await page.keyboard.press('End')
    await expect(kind(page, 'anthropic')).toHaveAttribute('aria-checked', 'true')
  })

  test('the choose list is a keyboard-operable checkbox group', async ({ page }) => {
    await firstRun(page, { test: TEST_OK(CLOUD_MODELS) })
    await goRemote(page)
    await kind(page, 'anthropic').click()
    await page.getByTestId('remote-key').fill('k')
    await page.getByTestId('remote-test').click()
    await expect(page.getByRole('group', { name: 'Models to add' })).toBeVisible()
    const first = page.getByTestId('remote-models').getByRole('checkbox').first()
    await first.focus()
    await page.keyboard.press('Space')
    await expect(first).toBeChecked()
    await expect(page.getByTestId('remote-count')).toHaveText('1 of 3 selected')
  })

  test('a cloud model with no listed uses shows a Chat tag and is sent as chat', async ({ page }) => {
    const state = await firstRun(page, { test: TEST_OK(CLOUD_BARE) })
    await goRemote(page)
    await kind(page, 'anthropic').click()
    await page.getByTestId('remote-key').fill('key-abc')
    await page.getByTestId('remote-test').click()
    const rows = page.getByTestId('remote-models').locator('li')
    await expect(rows).toHaveCount(2)
    for (let i = 0; i < 2; i++) await expect(rows.nth(i)).toContainText('Chat')
    await page.getByTestId('remote-models').getByRole('checkbox').first().check()
    await page.getByTestId('remote-connect').click()
    const create = state.calls.find(c => c.method === 'POST' && c.path === '/')
    expect(create.body.models).toEqual([{ id: 'cloud-model-a', usecases: ['chat'] }])
  })

  test('a test that was capped shows its notice next to the result', async ({ page }) => {
    await firstRun(page, { test: { ok: true, models: LOCALAI_MODELS, message: 'Only the first 2000 are shown.' } })
    await goRemote(page)
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-ok')).toBeVisible()
    await expect(page.getByTestId('remote-notice')).toContainText('Only the first 2000 are shown.')
  })

  test('more than 500 ticked models cannot be sent in one call', async ({ page }) => {
    const many = Array.from({ length: 501 }, (_, i) => ({ id: `m-${i}`, usecases: ['chat'] }))
    await firstRun(page, { test: TEST_OK(many) })
    await goRemote(page)
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-too-many')).toContainText('at most 500')
    await expect(page.getByTestId('remote-connect')).toBeDisabled()
    await page.getByTestId('remote-models').getByRole('checkbox').first().uncheck()
    await expect(page.getByTestId('remote-connect')).toBeEnabled()
  })

  test('a member sees no switch and the same page as before', async ({ page }) => {
    await mockHome(page, { loaded: [], models: [], chatModels: [] })
    await mockRecommended(page, recommendedAnswer())
    await mockUser(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-where')).toHaveCount(0)
  })
})

const SKIPS = [
  { id: 'chat-small', reason: 'listed twice' },
  { id: 'text-to-speech', reason: 'a model with that name exists' },
]
const created = (body, over = {}) => remote({ id: 'remotes-new', name: body.name, entries: ['chat-large'], ...over })

test.describe('First run: a result with skipped models or a warning', () => {
  async function connect(page, options) {
    const state = await firstRun(page, { test: TEST_OK(LOCALAI_MODELS), ...options })
    // The server lists the new entries from now on, so the guide gives way.
    await page.route('**/v1/models', route => route.fulfill({ json: { data: LOCALAI_MODELS.map(m => ({ id: m.id })) } }))
    await goRemote(page)
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await page.getByTestId('remote-connect').click()
    return state
  }

  test('skipped models stay on screen with their reasons until the person continues', async ({ page }) => {
    await connect(page, {
      create: ({ body }) => ({ json: { remote: created(body), created: ['chat-large', 'embed-base', 'speech-to-text'], skipped: SKIPS } }),
    })
    const outcome = page.getByTestId('remote-outcome')
    await expect(outcome).toBeVisible()
    // The guide has not gone away: the list is the point.
    await expect(page.getByTestId('home-first-run')).toBeVisible()
    await expect(outcome).toContainText('Some models were not added.')
    await expect(outcome).toContainText('Added 3 models.')
    // A count first, the list on demand.
    const skipped = page.getByTestId('remote-skipped')
    await expect(skipped).toContainText('2 models not added')
    await expect(page.getByTestId('remote-skipped-item').first()).toBeHidden()
    await skipped.getByText('2 models not added').click()
    await expect(page.getByTestId('remote-skipped-item')).toHaveCount(2)
    await expect(skipped).toContainText('chat-small')
    await expect(skipped).toContainText('Listed twice')
    await expect(skipped).toContainText('A model with that name exists')
    // The two skipped models are still missing, so Adopt is offered.
    await expect(page.getByTestId('remote-adopt')).toBeVisible()
    await page.getByTestId('remote-continue').click()
    await expect(page.getByTestId('home-first-run')).toHaveCount(0, { timeout: 15_000 })
    await expect(page.getByText('Connected office.example.com. Added 3 models.')).toBeVisible()
    await expect(page.getByText(/2 models were not added: chat-small, text-to-speech/)).toBeVisible()
  })

  test('a warning offers Adopt models and never creates the remote twice', async ({ page }) => {
    const state = await connect(page, {
      create: ({ body }) => ({
        json: {
          remote: created(body),
          created: ['chat-large'],
          skipped: [{ id: 'chat-small', reason: 'could not write the entry' }],
          warning: 'The remote was saved, but some models could not be added. Check the list of models and the server log, then add the missing models again.',
        },
      }),
      adopt: ({ body }) => ({ json: { remote: created({ name: 'office.example.com' }, { entries: ['chat-large', ...body.models.map(m => m.id)] }), created: body.models.map(m => m.id), skipped: [] } }),
    })
    const warning = page.getByTestId('remote-warning')
    await expect(warning).toContainText('The remote is saved, but some models were not added.')
    await expect(warning).toContainText('Adopt the missing models')
    await expect(warning).not.toContainText('server log')
    await expect(warning).toHaveAttribute('role', 'alert')
    await expect(page.getByTestId('remote-skipped')).toContainText('1 model not added')
    // The form is locked and the create button is gone: only Adopt remains.
    await expect(page.getByTestId('remote-connect')).toHaveCount(0)
    await expect(page.getByTestId('remote-url')).toBeDisabled()
    await page.getByTestId('remote-adopt').click()
    await expect(page.getByTestId('home-first-run')).toHaveCount(0, { timeout: 15_000 })
    const posts = state.calls.filter(c => c.method === 'POST')
    expect(posts.filter(c => c.path === '/')).toHaveLength(1)
    const adopt = posts.find(c => c.path === '/remotes-new/adopt')
    // Only the models the remote still lacks.
    expect(adopt.body.models.map(m => m.id)).toEqual(['chat-small', 'embed-base', 'speech-to-text', 'text-to-speech'])
  })

  test('an adopt that is partial too keeps the result and the offer', async ({ page }) => {
    await connect(page, {
      create: ({ body }) => ({ json: { remote: created(body), created: ['chat-large'], skipped: [{ id: 'chat-small', reason: 'could not write the entry' }], warning: 'partial' } }),
      adopt: ({ body }) => ({ json: { remote: created({ name: 'x' }), created: body.models.slice(0, 1).map(m => m.id), skipped: [{ id: 'text-to-speech', reason: 'could not write the entry' }], warning: 'still partial' } }),
    })
    await page.getByTestId('remote-adopt').click()
    await expect(page.getByTestId('remote-warning')).toContainText('The remote is saved, but some models were not added.')
    await expect(page.getByTestId('remote-skipped')).toContainText('text-to-speech')
    await expect(page.getByTestId('remote-skipped')).toContainText('1 model not added')
    await expect(page.getByTestId('home-first-run')).toBeVisible()
    await expect(page.getByTestId('remote-adopt')).toBeVisible()
  })
})

test.describe('First run: a server without the remote routes', () => {
  test('the choice is not offered', async ({ page }) => {
    await firstRun(page, { status: { 'GET /': { status: 404, message: 'Not Found' } } })
    await expect(page.getByTestId('home-first-run')).toContainText('Browse the Model Gallery')
    await expect(page.getByTestId('home-where')).toHaveCount(0)
  })

  test('a test that meets a missing route says so in plain words', async ({ page }) => {
    await firstRun(page, { status: { 'POST /test': { status: 404, message: 'Not Found' } } })
    await goRemote(page)
    await page.getByTestId('remote-url').fill('https://office.example.com')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-fail')).toContainText('This server cannot connect remotes.')
    await expect(page.getByTestId('remote-fail')).not.toContainText('Not Found')
  })
})

test.describe('First run: remote on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('the steps fit the screen with no sideways scroll', async ({ page }) => {
    await firstRun(page, { test: TEST_OK(LOCALAI_MODELS) })
    await goRemote(page)
    await kind(page, 'openai').click()
    await page.getByTestId('remote-key').fill('k')
    await page.getByTestId('remote-test').click()
    await expect(page.getByTestId('remote-ok')).toBeVisible()
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(over).toBeLessThanOrEqual(0)
  })
})

test.describe('First run: gallery endpoints off', () => {
  test('the choice to use another server still shows', async ({ page }) => {
    for (const pattern of ['**/models/available*', '**/models/galleries']) {
      await page.route(pattern, route => route.fulfill({ status: 404, json: { error: 'not found' } }))
    }
    await firstRun(page)
    await expect(where(page, 'remote')).toBeVisible()
    await goRemote(page)
  })
})
