import { test, expect } from './coverage-fixtures.js'

test('agent knowledge-base selectors filter models and save their selections', async ({ page }) => {
  await page.route('**/api/agents/config/metadata', route => route.fulfill({
    json: { Fields: [
      { name: 'name', label: 'Name', type: 'text', required: true, tags: { section: 'BasicInfo' } },
      { name: 'model', label: 'Reasoning Model', type: 'text', defaultValue: 'chat-model', tags: { section: 'ModelSettings' } },
      { name: 'embedding_model', label: 'Embedding Model', type: 'text', tags: { section: 'ModelSettings' } },
      { name: 'reranker_model', label: 'Reranker Model', type: 'text', tags: { section: 'ModelSettings' } },
    ] },
  }))
  await page.route('**/api/models/capabilities', route => route.fulfill({
    json: { data: [
      { id: 'chat-model', capabilities: ['FLAG_CHAT'] },
      { id: 'embed-model', capabilities: ['FLAG_EMBEDDINGS'] },
      { id: 'rank-model', capabilities: ['FLAG_RERANK'] },
    ] },
  }))
  await page.route('**/api/agents', route => route.fulfill({
    json: route.request().method() === 'POST' ? {} : { agents: [], statuses: {} },
  }))

  await page.goto('/app/agents/new')
  await page.locator('#field-name').fill('research')
  await page.locator('.agent-wizard-nav-item').filter({ hasText: 'Model Settings' }).click()

  const embedding = page.locator('.form-row').filter({ has: page.getByText('Embedding Model', { exact: true }) })
  const reranker = page.locator('.form-row').filter({ has: page.getByText('Reranker Model', { exact: true }) })
  await expect(embedding.locator('input')).toHaveValue('')
  await expect(reranker.locator('input')).toHaveValue('')

  await embedding.locator('input').click()
  await expect(embedding.getByRole('option')).toHaveCount(1)
  await embedding.getByRole('option', { name: /embed-model/ }).click()
  await reranker.locator('input').click()
  await expect(reranker.getByRole('option')).toHaveCount(1)
  await reranker.getByRole('option', { name: /rank-model/ }).click()

  const saved = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/agents')
  await page.getByRole('button', { name: 'Create Agent', exact: true }).click()
  expect((await saved).postDataJSON()).toMatchObject({
    name: 'research', model: 'chat-model', embedding_model: 'embed-model', reranker_model: 'rank-model',
  })
  await expect(page).toHaveURL(/\/app\/agents$/)
})
