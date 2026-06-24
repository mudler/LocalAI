import { test, expect } from './coverage-fixtures.js'

const MODEL_NAME = 'rate%model'
const SKILL_NAME = 'cache%skill'
const encodedModel = encodeURIComponent(MODEL_NAME)
const encodedSkill = encodeURIComponent(SKILL_NAME)

test.describe('Encoded route parameters', () => {
  test('model editor preserves a literal percent in the model name', async ({ page }) => {
    let requestedPath = ''
    await page.route('**/api/models/config-metadata?section=all', (route) => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ sections: [], fields: [] }),
    }))
    await page.route(`**/api/models/edit/${encodedModel}`, (route) => {
      requestedPath = new URL(route.request().url()).pathname
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          name: MODEL_NAME,
          config: `name: ${MODEL_NAME}\nbackend: mock-backend\n`,
        }),
      })
    })

    await page.goto(`/app/model-editor/${encodedModel}`)

    await expect(page.locator('.page-subtitle')).toHaveText(MODEL_NAME)
    await expect.poll(() => requestedPath).toBe(`/api/models/edit/${encodedModel}`)
  })

  test('backend logs preserve a literal percent in the model name', async ({ page }) => {
    await page.goto(`/app/backend-logs/${encodedModel}`)

    await expect(page.locator('.page-title')).toContainText(MODEL_NAME)
  })

  test('node backend logs preserve a literal percent in the model name', async ({ page }) => {
    await page.route('**/api/nodes/test-node', (route) => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ id: 'test-node', name: 'Test node' }),
    }))
    await page.route('**/api/nodes/test-node/models', (route) => route.fulfill({
      contentType: 'application/json',
      body: '[]',
    }))

    await page.goto(`/app/node-backend-logs/test-node/${encodedModel}`)

    await expect(page.locator('.page-title')).toContainText(MODEL_NAME)
  })

  test('skill editor does not decode an already decoded route parameter', async ({ page }) => {
    await page.route(`**/api/agents/skills/${encodedSkill}`, (route) => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        name: SKILL_NAME,
        description: 'A route parameter regression fixture',
        content: '# Fixture',
      }),
    }))

    await page.goto(`/app/skills/edit/${encodedSkill}`)

    await expect(page.locator('.page-title')).toContainText(`Edit: ${SKILL_NAME}`)
  })
})
