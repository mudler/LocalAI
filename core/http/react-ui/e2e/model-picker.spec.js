import { test, expect } from './coverage-fixtures.js'

// The shared model picker, reached through the settings and scheduling pages
// with the model list stubbed: aliases, broken aliases and disabled models.

const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
  { id: 'fast', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp', alias_of: 'qwen3-8b' },
  { id: 'lost', capabilities: [], alias_of: 'gone', alias_error: 'alias "lost" points to unknown model "gone"' },
  { id: 'old', capabilities: ['FLAG_CHAT'], disabled: true },
  { id: 'pinned-one', capabilities: ['FLAG_CHAT'], pinned: true },
]

async function stub(page) {
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: MODELS } }))
  await page.route('**/api/nodes/scheduling', route => route.fulfill({ json: [] }))
  await page.route('**/api/nodes', route => route.fulfill({ json: [] }))
}

test.describe('Model picker', () => {
  test('lists an alias with its target and a broken alias as unusable', async ({ page }) => {
    await stub(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'New rule' }).click()
    await page.locator('#sched-model').click()
    const list = page.getByRole('listbox')
    await expect(list.getByRole('option', { name: /fast.*alias of qwen3-8b/ })).toBeVisible()
    await expect(list.getByRole('option', { name: /lost.*broken alias/ })).toHaveAttribute('aria-disabled', 'true')
    await expect(list.getByRole('option', { name: /old.*disabled/ })).toHaveAttribute('aria-disabled', 'true')
    await expect(list.getByRole('option', { name: /pinned-one.*pinned/ })).toBeVisible()
    if (process.env.PICKER_SHOTS) await page.waitForTimeout(600)
    if (process.env.PICKER_SHOTS) await page.screenshot({ path: `${process.env.PICKER_SHOTS}/scheduling-picker.png` })
    await list.getByRole('option', { name: /fast/ }).click()
    await expect(page.locator('#sched-model')).toContainText('fast')
  })

  test('a typed name that is not listed can be used as it is', async ({ page }) => {
    await stub(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'New rule' }).click()
    await page.locator('#sched-model').click()
    await page.getByRole('combobox', { name: 'Filter models' }).fill('later-model')
    await page.getByRole('option', { name: /Use "later-model"/ }).click()
    await expect(page.locator('#sched-model')).toContainText('later-model')
  })
})
