import { test, expect } from './coverage-fixtures.js'
import { mockHome, mockUser } from './home-fixtures.js'
import { mockRecommended, recommendedAnswer } from './recommended-fixtures.js'
import { mockConnections, TEST_OK, TEST_FAIL, LOCALAI_MODELS, CLOUD_MODELS } from './connections-fixtures.js'

// First run, with the choice "where do your models run". On this machine keeps
// the steps of today. On another server or service swaps steps 1 and 2 for
// "connect a remote" and "choose models".

async function firstRun(page, options = {}) {
  await mockHome(page, { loaded: [], models: [], chatModels: [] })
  await mockRecommended(page, recommendedAnswer())
  const state = await mockConnections(page, options)
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
    expect(state.calls.find(c => c.path === '/test').body.kind).toBe('anthropic')
  })

  for (const [step, words] of [
    ['invalid_url', 'That address is not valid'],
    ['unreachable', 'check the address and that the other server is running and reachable from this server'],
    ['auth', 'The provider rejected the key'],
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

  test('a member sees no switch and the same page as before', async ({ page }) => {
    await mockHome(page, { loaded: [], models: [], chatModels: [] })
    await mockRecommended(page, recommendedAnswer())
    await mockUser(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-where')).toHaveCount(0)
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
