import { test, expect } from './coverage-fixtures.js'
import { mockLibrary } from './library-fixtures.js'

// The agent form's skill and memory pickers say what an addition costs and
// who else uses it, from the same facts the Library pages read.

test.describe('Agent form pickers', () => {
  test('each skill says who else uses it and what it costs, with a total', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/agents/handbook/edit')
    await page.locator('[data-section="AdvancedSettings"] .ag-fold__button').click()
    await expect(page.getByTestId('agent-skill-hint-summarise-pdf')).toHaveText('Used by research-assistant · +114 tokens per message (estimate)')
    await expect(page.getByTestId('agent-skill-hint-translate-document')).toHaveText('Not used yet · +90 tokens per message (estimate)')
    await expect(page.getByTestId('agent-skills-budget')).toContainText('about 114 tokens')
  })

  test('the memory section names the collection the agent reads', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/agents/handbook/edit')
    await page.locator('[data-section="MemorySettings"] .ag-fold__button').click()
    await expect(page.getByTestId('agent-kb-note')).toContainText('collection named handbook')
    await expect(page.getByTestId('agent-kb-note')).toContainText('fill and search it in Memory')
  })
})
