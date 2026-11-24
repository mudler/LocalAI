// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'
import { GPU_HOST } from './tools-fixtures.js'

async function boot(page) {
  // Keep this full-page test independent of a running LocalAI backend.
  await page.route(/\/(api|v1|models|backends|system|version)(\/|\?|$)/, route => route.fulfill({ json: {} }))
  await mockAccess(page)
  await page.route('**/api/resources', route => route.fulfill({ json: GPU_HOST }))
  await page.route('**/v1/models', route => route.fulfill({ json: { data: [] } }))
  await page.route('**/backends/known', route => route.fulfill({ json: [
    { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
    { name: 'vllm', modality: 'text', auto_detect: true, installed: true },
  ] }))
  await page.goto('/app/import-model')
}

async function spinnerCount(button) {
  return button.evaluate(el => {
    const pseudo = getComputedStyle(el, '::before')
    const kit = pseudo.content !== 'none' && pseudo.display !== 'none' && pseudo.animationName.includes('spin') ? 1 : 0
    return kit + el.querySelectorAll('.spinner-ring, .dk-spinner').length
  })
}

for (const yaml of [false, true]) {
  test(`${yaml ? 'YAML save' : 'import'} uses one kit spinner and cannot submit twice while pending`, async ({ page }) => {
    await boot(page)
    let release
    const pending = new Promise(resolve => { release = resolve })
    const posts = []
    await page.route(yaml ? '**/models/import' : '**/models/import-uri', async route => {
      posts.push(route.request().postData())
      await pending
      await route.fulfill({ status: 500, json: { error: 'test response' } })
    })
    if (yaml) await page.getByRole('tab', { name: /Write YAML/ }).click()
    else await page.getByTestId('import-source-input').fill('hf://owner/repo')
    const button = page.getByTestId(yaml ? 'import-create' : 'import-submit')
    try {
      await button.click()
      await expect.poll(() => posts.length).toBe(1)
      await expect(button).toBeDisabled()
      await expect(button).toHaveAttribute('aria-busy', 'true')
      // DOM click on a disabled native button must not dispatch another POST.
      await button.evaluate(el => { el.click(); el.click() })
      if (!yaml) await page.getByTestId('import-source-input').press('Enter')
      await expect.poll(() => spinnerCount(button)).toBe(1)
      expect(posts).toHaveLength(1)
    } finally { release() }
    await expect(button).toBeEnabled()
    await expect(button).not.toHaveAttribute('aria-busy', 'true')
    expect(posts).toHaveLength(1)
  })
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`advanced controls are adjacent and reachable at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await boot(page)
    await page.getByTestId('import-source-input').fill('hf://owner/repo')
    await page.getByTestId('import-adjust').click()
    const toggle = page.getByTestId('import-options-toggle')
    const panel = page.getByTestId('import-options-panel')
    // Assert before any locator action can silently scroll the controls into view.
    await expect(toggle).toBeInViewport()
    await expect(toggle).toBeFocused()
    await expect(toggle).toHaveAccessibleName(/Advanced options/)
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(toggle).toHaveAttribute('aria-controls', 'import-options-panel')
    await expect(page.locator('[data-testid="import-preview"] + .import-options')).toHaveCount(1)
    await expect(panel.locator('button[aria-haspopup="listbox"]')).toBeInViewport()
    await page.locator('#import-name').fill('chosen-name')
    await page.locator('#import-quantizations').fill('q5_k_m')
    await toggle.focus()
    await page.keyboard.press('Space')
    await expect(panel).toHaveCount(0)
    await page.keyboard.press('Enter')
    await expect(panel).toBeVisible()
    await expect(page.locator('#import-name')).toHaveValue('chosen-name')
    await expect(page.locator('#import-quantizations')).toHaveValue('q5_k_m')
    await page.reload()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await toggle.click()
    await page.reload()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })
}

test('explicit backend reaches preview and POST; automatic omits the preference', async ({ page }) => {
  await boot(page)
  const posts = []
  await page.route('**/models/import-uri', route => {
    posts.push(route.request().postDataJSON())
    return route.fulfill({ status: 500, json: { error: 'test response' } })
  })
  await page.getByTestId('import-source-input').fill('hf://owner/repo')
  await page.getByTestId('import-options-toggle').click()
  const select = page.getByTestId('import-options-panel').locator('button[aria-haspopup="listbox"]')
  await select.click()
  await page.getByRole('option', { name: /^llama-cpp/ }).click()
  await expect(page.getByTestId('import-preview-code')).toContainText('backend: llama-cpp')
  await page.getByTestId('import-submit').click()
  await expect.poll(() => posts.length).toBe(1)
  expect(posts[0]).toEqual({ uri: 'hf://owner/repo', preferences: { backend: 'llama-cpp' } })
  await expect(page.getByTestId('import-submit')).toBeEnabled()
  await select.click()
  await page.getByRole('option', { name: /Auto-detect/ }).click()
  await expect(page.getByTestId('import-preview-code')).not.toContainText('backend:')
  await page.getByTestId('import-submit').click()
  await expect.poll(() => posts.length).toBe(2)
  expect(posts[1]).toEqual({ uri: 'hf://owner/repo', preferences: null })
})

test('an HTTPS documentation URL is recognised only as a format, without contacting it', async ({ page }) => {
  await boot(page)
  const requests = []
  page.on('request', request => requests.push(request.url()))
  await page.getByTestId('import-source-input').fill('https://example.com/docs')
  await expect(page.getByTestId('import-source-kind')).toHaveAttribute('data-kind', 'url')
  await expect(page.getByTestId('import-submit')).toBeEnabled()
  await expect(page.locator('.import-page')).toContainText('The source has not been contacted or validated as a model.')
  await expect(page.getByTestId('import-checks')).toContainText('URL format recognised')
  await expect(page.locator('.import-page')).not.toContainText('LocalAI reads the repository when you import')
  expect(requests.some(url => url.startsWith('https://example.com/'))).toBe(false)
})
