import { test, expect } from './coverage-fixtures.js'
import { mockRuntime } from './engine-runtime-fixtures.js'

// The Runtime tuning section for a second engine, mocked: a table of its own
// and no capability. The section must draw the fields from the schema alone,
// name the engine in its badge, and leave out every part of a capability the
// engine does not list: no presets, no placement block, no command preview,
// no paste box.

const field = (page, id) => page.getByTestId(`rt-${id}`)
const lastPatch = state => state.patches[state.patches.length - 1]?.patch

async function open(page) {
  const state = await mockRuntime(page)
  await page.goto('/app/model-editor/toy-model')
  await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 15_000 })
  await expect(field(page, 'context_size')).toBeVisible({ timeout: 10_000 })
  return state
}

test.describe('Runtime tuning: another engine', () => {
  test('fields render from its schema, with no llama.cpp part', async ({ page }) => {
    const state = await open(page)
    await expect(page.getByTestId('rail-runtime')).toBeVisible()
    await expect(page.getByTestId('runtime-engine')).toHaveText('Toy Engine')
    await expect(page.getByTestId('runtime')).toContainText('How Toy Engine loads and runs this model.')

    // Its own groups, in its own order, and its own settings.
    await expect(page.getByTestId('rt-group-limits')).toBeVisible()
    await expect(page.getByTestId('rt-group-runtime')).toBeVisible()
    await expect(page.getByTestId('rt-group-limits')).toContainText('Longest sequence in tokens.')
    await expect(field(page, 'options-gpu_memory_utilization')).toBeVisible()
    await expect(field(page, 'options-enforce_eager-true')).toHaveAttribute('aria-checked', 'true')

    // Nothing of a capability it does not have, and nothing of llama.cpp.
    for (const id of ['rt-presets', 'rt-group-placement', 'rt-ot', 'rt-preview', 'rt-paste', 'rt-load-mode', 'rt-group-memory']) {
      await expect(page.getByTestId(id), id).toHaveCount(0)
    }
    await expect(page.getByTestId('runtime')).not.toContainText('llama')

    // A raw option stays a raw option.
    await expect(page.getByTestId('rt-raw-0')).toHaveValue('seed:3')
    expect(state.toyCalls).toEqual([])
  })

  test('edits write its keys and options on save', async ({ page }) => {
    const state = await open(page)
    await field(page, 'context_size').fill('8192')
    await field(page, 'context_size').blur()
    await field(page, 'options-enforce_eager-').click()
    await page.getByTestId('editor-save').click()
    await expect.poll(() => state.patches.length).toBeGreaterThan(0)
    const patch = lastPatch(state)
    expect(patch.context_size).toBe(8192)
    expect(patch.options).toEqual(['seed:3'])
    expect(state.toyCalls).toEqual([])
  })
})
