import { test, expect } from './coverage-fixtures.js'
import { mockAccess, statusFixture } from './access-fixtures.js'
import { mockHome } from './home-fixtures.js'

// The guide under Account > API keys, and the two first-run steps on Home that
// lead to it and to the add-ons.

const chatModels = (page, ids) => page.route('**/api/models/capabilities', route =>
  route.fulfill({ json: { data: ids.map(id => ({ id, capabilities: ['FLAG_CHAT'] })) } }))

test.describe('Connect a coding agent', () => {
  test('lists the six agents and fills the base URL, model and the key placeholder', async ({ page }) => {
    await mockAccess(page)
    await chatModels(page, ['qwen3-8b-instruct', 'gemma-3-12b-it'])
    await page.goto('/app/account?tab=keys')
    const guide = page.getByTestId('agent-connect')
    await expect(guide).toBeVisible()
    await expect(page.getByTestId('agent-connect-model-note')).toContainText('tool calling')
    await expect(page.getByTestId('agent-connect-pick').getByRole('button')).toHaveText(['Codex CLI', 'Claude Code', 'opencode', 'Hermes Agent', 'nib', 'pi'])
    // Codex is first: Responses API, /v1 base, the first chat model.
    await expect(page.getByTestId('agent-connect-endpoint')).toContainText('/v1/responses')
    await expect(page.getByTestId('agent-connect-file')).toContainText('~/.codex/config.toml')
    const config = page.getByTestId('agent-connect-snippet-config')
    await expect(config).toContainText('wire_api = "responses"')
    await expect(config).toContainText('model = "qwen3-8b-instruct"')
    await expect(config).toContainText(new RegExp(`base_url = "${new URL(page.url()).origin}/v1"`))
    await expect(page.getByTestId('agent-connect-snippet-shell')).toContainText('LOCALAI_API_KEY=YOUR_API_KEY')
    await expect(page.getByTestId('agent-connect-auth')).toContainText('shown only once')
    await expect(page.getByTestId('agent-connect-docs')).toHaveAttribute('href', /\/features\/coding-agents\/$/)
  })

  test('the pressed tab is always the agent whose snippet is shown', async ({ page }) => {
    await mockAccess(page)
    await chatModels(page, ['qwen3-8b-instruct'])
    await page.goto('/app/account?tab=keys')
    const ids = ['codex', 'claude-code', 'opencode', 'hermes', 'nib', 'pi', 'codex']
    const file = { codex: '.codex/config.toml', 'claude-code': '.claude/settings.json', opencode: 'opencode.json', hermes: '.hermes/config.yaml', nib: 'nib/config.yaml', pi: 'models.json' }
    for (const id of ids) {
      await page.getByTestId(`agent-connect-${id}`).click()
      const pressed = page.getByTestId('agent-connect-pick').locator('[aria-pressed="true"]')
      await expect(pressed).toHaveCount(1)
      await expect(pressed).toHaveAttribute('data-filter', id)
      await expect(page.getByTestId('agent-connect-file')).toContainText(file[id])
    }
  })

  test('Claude Code gets the base URL without /v1 and follows the chosen model', async ({ page }) => {
    await mockAccess(page)
    await chatModels(page, ['qwen3-8b-instruct', 'gemma-3-12b-it'])
    await page.goto('/app/account?tab=keys')
    await page.getByTestId('agent-connect-claude-code').click()
    await page.getByLabel('Model', { exact: true }).selectOption('gemma-3-12b-it')
    const shell = page.getByTestId('agent-connect-snippet-shell')
    await expect(shell).toContainText('ANTHROPIC_MODEL=gemma-3-12b-it')
    await expect(shell).toContainText('ANTHROPIC_DEFAULT_HAIKU_MODEL=gemma-3-12b-it')
    const text = await shell.innerText()
    expect(text).toMatch(/ANTHROPIC_BASE_URL=http:\/\/[^\s/]+(:\d+)?\n/)
    expect(text).not.toMatch(/ANTHROPIC_BASE_URL=\S*\/v1/)
    await expect(page.getByTestId('agent-connect-endpoint')).toContainText('/v1/messages')
  })

  test('each agent shows its file, endpoint and one caveat', async ({ page }) => {
    await mockAccess(page)
    await chatModels(page, [])
    await page.goto('/app/account?tab=keys')
    const files = { opencode: '~/.config/opencode/opencode.json', hermes: '~/.hermes/config.yaml', nib: '~/.config/nib/config.yaml', pi: '~/.pi/agent/models.json' }
    for (const [id, file] of Object.entries(files)) {
      await page.getByTestId(`agent-connect-${id}`).click()
      await expect(page.getByTestId('agent-connect-file')).toContainText(file)
      await expect(page.getByTestId('agent-connect-endpoint')).toContainText('/v1/chat/completions')
      await expect(page.getByTestId('agent-connect-caveat')).not.toBeEmpty()
      // No chat model: the placeholder model.
      await expect(page.getByTestId('agent-connect-snippet-config')).toContainText('my-model')
    }
  })

  test('a just-created key is put in the snippet', async ({ page }) => {
    await mockAccess(page)
    await chatModels(page, ['qwen3-8b-instruct'])
    await page.goto('/app/account?tab=keys')
    await page.getByLabel('New API key', { exact: true }).fill('agent key')
    await page.getByRole('button', { name: 'New API key' }).click()
    await expect(page.getByTestId('api-key-reveal')).toBeVisible()
    await page.getByTestId('agent-connect-hermes').click()
    await expect(page.getByTestId('agent-connect-snippet-config')).toContainText('api_key: "lai-9d41f0c27be84a0c92aa5d6b"')
    await expect(page.getByTestId('agent-connect-auth')).toContainText('just created')
  })

  test('the admin Users page does not show the guide', async ({ page }) => {
    await mockAccess(page)
    await chatModels(page, [])
    await page.goto('/app/users?tab=keys')
    await expect(page.getByTestId('api-keys-panel')).toBeVisible()
    await expect(page.getByTestId('agent-connect')).toHaveCount(0)
  })
})

async function firstRun(page, { features = { addons: true }, auth = true } = {}) {
  await mockHome(page, { loaded: [], models: [], chatModels: [], features })
  await page.route('**/api/auth/status', route => route.fulfill({ json: auth ? statusFixture('admin') : { authEnabled: false, staticApiKeyRequired: false, providers: [], user: null } }))
  await page.goto('/app')
  await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 15_000 })
}

test.describe('First run steps for agents and add-ons', () => {
  test('shows the agents step with a link to the key guide', async ({ page }) => {
    await firstRun(page)
    const step = page.getByTestId('home-step-agents')
    await expect(step).toContainText('Onboard your agents')
    await expect(step).toContainText('Codex, Claude Code, opencode, Hermes Agent, nib, pi')
    await expect(page.getByTestId('home-step-agents-link')).toHaveAttribute('href', '/app/account?tab=keys')
  })

  test('shows the add-on step with examples and a link to the add-ons page', async ({ page }) => {
    await firstRun(page)
    const step = page.getByTestId('home-step-addons')
    await expect(step).toContainText('Extend LocalAI with an add-on')
    await expect(step).toContainText('Sandbox, Telegram')
    await expect(page.getByTestId('home-step-addons-link')).toHaveAttribute('href', '/app/addons')
  })

  test('hides the add-on step when the add-ons feature is off, and keeps the agents step', async ({ page }) => {
    await firstRun(page, { features: { addons: false } })
    await expect(page.getByTestId('home-step-addons')).toHaveCount(0)
    await expect(page.getByTestId('home-step-agents')).toBeVisible()
  })

  test('hides the agents step when accounts are off, since the guide lives under API keys', async ({ page }) => {
    await firstRun(page, { auth: false })
    await expect(page.getByTestId('home-step-agents')).toHaveCount(0)
  })
})
