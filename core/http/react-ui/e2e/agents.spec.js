import { test, expect } from './coverage-fixtures.js'

// Agents feature page (src/pages/Agents.jsx).
test.describe('Agents page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/agents')
  })

  test('renders the agents list and empty state', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/agents$/)
    await expect(page.getByRole('heading', { name: 'Agents', exact: true })).toBeVisible()
    await expect(page.getByText(/No agents configured/i)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Create Agent' }).first()).toBeVisible()
  })

  test('Create Agent navigates to the agent creation form', async ({ page }) => {
    const create = page.getByRole('button', { name: 'Create Agent' }).last()
    await create.scrollIntoViewIfNeeded()
    await Promise.all([
      page.waitForURL(/\/app\/agents\/new$/),
      create.click(),
    ])
    // Wait for AgentCreate.jsx to actually render, not just for the URL to
    // change. Ending the test the instant the route matched let the component
    // mount race the coverage teardown — its ~400 lines were collected only
    // when the render won, swinging total UI coverage ~1pp run-to-run.
    await expect(page.getByRole('heading', { name: 'Create Agent' })).toBeVisible()
  })
})
