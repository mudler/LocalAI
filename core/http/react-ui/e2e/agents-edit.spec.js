import { test, expect } from './coverage-fixtures.js'
import { installFakeStream, mockAgents, RESEARCH_CONFIG } from './agents-fixtures.js'

test.describe('Create and edit an agent', () => {
  test('sections fold, show ready marks and a one-line summary', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/research-assistant/edit')
    await expect(page.getByRole('heading', { name: 'Edit Agent: research-assistant' })).toBeVisible()
    const basics = page.locator('[data-section="BasicInfo"]')
    await expect(basics.locator('.ag-fold__button')).toHaveAttribute('aria-expanded', 'true')
    const model = page.locator('[data-section="ModelSettings"]')
    await expect(model.locator('.ag-fold__button')).toHaveAttribute('aria-expanded', 'false')
    await expect(model.locator('.ag-fold__sum')).toHaveText('qwen3-14b-instruct')
    await expect(model.locator('.ag-ready')).toHaveAttribute('data-state', 'ready')
    await expect(model.locator('.ag-fold__body')).toHaveCount(0)
    await model.locator('.ag-fold__button').click()
    await expect(model.locator('.ag-fold__body')).toBeVisible()
    await model.locator('.ag-fold__button').click()
    await expect(model.locator('.ag-fold__body')).toHaveCount(0)
  })

  test('a new agent starts with sections that need a name and a model', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/new')
    await expect(page.locator('[data-section="BasicInfo"] .ag-ready')).toHaveAttribute('data-state', 'needs')
    await expect(page.locator('[data-section="ModelSettings"] .ag-fold__sum')).toHaveText('Needs a model')
    await expect(page.locator('[data-section="MemorySettings"] .ag-ready')).toHaveAttribute('data-state', 'empty')
  })

  test('a template fills the form and saves nothing', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await page.goto('/app/agents/new')
    await page.getByRole('button', { name: 'Code reviewer' }).click()
    await expect(page.locator('[data-section="BasicInfo"] .ag-fold__sum')).toContainText('code-reviewer')
    expect(seen.saves).toEqual([])
  })

  test('a template link from the launcher opens the form already filled', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/new?template=digest')
    await expect(page.locator('[data-section="BasicInfo"] .ag-fold__sum')).toContainText('daily-digest')
  })

  test('the preview sheet shows the config and, when editing, what changed', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page, { agents: { 'research-assistant': { active: true, config: { ...RESEARCH_CONFIG, api_key: 'sk-secret-value', mcp_servers: [{ url: 'https://x.example.org', token: 'tok-123' }] } } } })
    await page.goto('/app/agents/research-assistant/edit')
    await page.locator('[data-section="PromptsGoals"] .ag-fold__button').click()
    await page.locator('[data-section="PromptsGoals"] textarea').fill('A new instruction.')
    await expect(page.locator('[data-section="PromptsGoals"] .ag-badge-changed')).toHaveText('1 changed')

    await page.getByTestId('agent-preview-open').click()
    const sheet = page.getByTestId('agent-preview')
    await expect(sheet.getByTestId('agent-preview-config')).toContainText('"system_prompt": "A new instruction."')
    const cfg = await sheet.getByTestId('agent-preview-config').textContent()
    expect(cfg).not.toContain('sk-secret-value')
    expect(cfg).not.toContain('tok-123')

    await sheet.getByRole('tab', { name: /Changes/ }).click()
    const changes = sheet.getByTestId('agent-preview-changes')
    await expect(changes.locator('[data-key]')).toHaveCount(1)
    await expect(changes.locator('[data-key="system_prompt"]')).toContainText('You are a careful research assistant')
    await expect(changes.locator('[data-key="system_prompt"]')).toContainText('A new instruction.')

    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)

    await page.getByTestId('agent-preview-open').click()
    await page.getByTestId('agent-preview').getByRole('button', { name: 'Save Changes' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0]).toMatchObject({ method: 'PUT', name: 'research-assistant' })
    expect(seen.saves[0].body.system_prompt).toBe('A new instruction.')
  })

  test('with no edits the Changes tab says the form matches the saved agent', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/research-assistant/edit')
    await page.getByTestId('agent-preview-open').click()
    await page.getByRole('tab', { name: /Changes/ }).click()
    await expect(page.getByTestId('agent-preview-changes')).toContainText('No changes')
  })

  test('creating an agent sends the form and offers no Dry run it cannot run', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await page.goto('/app/agents/new')
    await page.locator('[data-section="BasicInfo"] input').first().fill('my-agent')
    await page.locator('[data-section="ModelSettings"] .ag-fold__button').click()
    await page.locator('[data-section="ModelSettings"] input').first().fill('qwen3-8b-instruct')
    await page.getByTestId('agent-preview-open').click()
    await expect(page.getByRole('tab', { name: /Dry run/ })).toHaveCount(0)
    await page.getByRole('tab', { name: /Changes/ }).click()
    await expect(page.getByTestId('agent-preview-changes')).toContainText('qwen3-8b-instruct')
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].body).toMatchObject({ name: 'my-agent', model: 'qwen3-8b-instruct' })
  })

  test('draft from a sentence needs a model first and is labelled optional', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/new')
    await expect(page.getByTestId('agent-start')).toContainText('Optional')
    await page.getByLabel('Describe the agent in one sentence').fill('reads release notes')
    await page.getByRole('button', { name: 'Draft' }).click()
    await expect(page.getByText(/Pick a model in the Model section first/)).toBeVisible()
  })

  test('draft from a sentence fills name, description and instructions from the chosen model', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.route('**/v1/chat/completions', route => route.fulfill({ json: { choices: [{ message: { content: '```json\n{"name":"release-reader","description":"Reads release notes.","system_prompt":"List breaking changes."}\n```' } }] } }))
    await page.goto('/app/agents/new')
    await page.locator('[data-section="ModelSettings"] .ag-fold__button').click()
    await page.locator('[data-section="ModelSettings"] input').first().fill('qwen3-8b-instruct')
    await page.getByLabel('Describe the agent in one sentence').fill('reads release notes')
    await page.getByRole('button', { name: 'Draft' }).click()
    await expect(page.locator('[data-section="BasicInfo"] .ag-fold__sum')).toContainText('release-reader')
  })
})
