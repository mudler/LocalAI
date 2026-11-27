import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'

// The disk prompt cache: the admin sees its state and clears it in Settings >
// Models in memory, and a user clears their own cached prompts in Account.
// The API is stubbed.

const GIB = 1024 ** 3

function statusFixture(state = 'on') {
  return {
    enabled: true,
    shared: false,
    nodes: [{
      node: 'local',
      bytes: 3 * GIB,
      files: 4,
      namespaces: 2,
      models: [{ model: 'qwen', model16: 'aaaaaaaaaaaaaaaa', bytes: 3 * GIB, files: 4 }],
      status: {
        state,
        cache_bytes: 3 * GIB,
        free_bytes: state === 'on' ? 80 * GIB : 4 * GIB,
        total_bytes: 200 * GIB,
        low_watermark_bytes: 30 * GIB,
        critical_watermark_bytes: 10 * GIB,
        max_bytes: 20 * GIB,
        effective_cap_bytes: 20 * GIB,
        shares_models_disk: true,
      },
    }],
  }
}

async function mockPromptCache(page, state) {
  const calls = []
  await page.route('**/api/prompt-cache', route => route.fulfill({ json: statusFixture(state) }))
  await page.route('**/api/prompt-cache/clear', route => {
    calls.push({ method: route.request().method(), path: '/api/prompt-cache/clear' })
    return route.fulfill({ json: { files: 4, bytes: 3 * GIB } })
  })
  await page.route('**/api/prompt-cache/mine', route => {
    calls.push({ method: route.request().method(), path: '/api/prompt-cache/mine' })
    return route.fulfill({ json: { files: 2, bytes: GIB } })
  })
  return calls
}

test.describe('Disk prompt cache', () => {
  test('Settings shows the state of the cache and clears it after a confirmation', async ({ page }) => {
    const calls = await mockPromptCache(page, 'on')
    await page.goto('/app/settings?group=memory')
    const panel = page.getByTestId('settings-prompt-cache')
    await expect(panel).toBeVisible()
    await expect(panel.getByTestId('prompt-cache-state')).toHaveText('On')
    await expect(panel).toContainText('3 GB in 4 entries')
    await expect(panel).toContainText('shares the disk with the models')
    await expect(panel.getByTestId('prompt-cache-message')).toHaveCount(0)

    await panel.getByTestId('prompt-cache-clear').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Clear' }).click()
    await expect(page.getByText('Cleared 4 cached prompts')).toBeVisible()
    expect(calls).toEqual([{ method: 'POST', path: '/api/prompt-cache/clear' }])
  })

  test('Settings says why a paused cache writes nothing', async ({ page }) => {
    await mockPromptCache(page, 'paused_low_disk')
    await page.goto('/app/settings?group=memory')
    const panel = page.getByTestId('settings-prompt-cache')
    await expect(panel.getByTestId('prompt-cache-state')).toHaveText('Paused: low disk')
    await expect(panel.getByTestId('prompt-cache-message')).toContainText('under the low mark')
  })

  test('the settings of the cache are in their own block', async ({ page }) => {
    await mockPromptCache(page, 'on')
    await page.goto('/app/settings?group=memory')
    await expect(page.getByRole('heading', { name: 'Disk prompt cache' })).toBeVisible()
    await expect(page.getByText('Keep long prompts on disk')).toBeVisible()
  })

  test('a user clears their own cached prompts in Account', async ({ page }) => {
    await mockAccess(page)
    const calls = await mockPromptCache(page, 'on')
    await page.goto('/app/account?tab=cache')
    const tab = page.getByTestId('account-prompt-cache')
    await expect(tab).toContainText('Incognito chats are never written to disk')
    await tab.getByTestId('account-prompt-cache-clear').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Clear my cached prompts' }).click()
    await expect(page.getByText('Cleared 2 cached prompts.')).toBeVisible()
    expect(calls).toEqual([{ method: 'DELETE', path: '/api/prompt-cache/mine' }])
  })
})
