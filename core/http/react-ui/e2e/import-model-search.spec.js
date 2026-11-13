import { test, expect } from './coverage-fixtures.js'

// The "Search online" panel of the import page. The API is mocked: the specs
// pin what the panel sends, what it shows for a partial answer, and that
// "Import" only fills the source field.

const MAGNET = 'magnet:?xt=urn:btih:7158ba0f294cdd7da7f4d4f7a496c6162a63a622&tr=udp%3A%2F%2Ftracker.example%3A6969%2Fannounce'

const ANSWER = {
  results: [
    { provider: 'huggingface', id: 'acme/Tiny-GGUF', title: 'acme/Tiny-GGUF', uri: 'huggingface://acme/Tiny-GGUF', url: 'https://huggingface.co/acme/Tiny-GGUF', downloads: 3400000, likes: 1240, license: 'apache-2.0', pipeline_tag: 'text-generation' },
    { provider: 'huggingface', id: 'acme/Locked-GGUF', title: 'acme/Locked-GGUF', uri: 'huggingface://acme/Locked-GGUF', url: 'https://huggingface.co/acme/Locked-GGUF', downloads: 5, gated: true },
    { provider: 'pirateface', id: 'acme/Tiny', title: 'acme/Tiny', uri: 'pirateface://acme/Tiny', magnet: MAGNET, url: 'https://huggingface.co/acme/Tiny', size_bytes: 1500000000, seeders: 6 },
  ],
  providers: [{ name: 'huggingface', ok: true, count: 2 }, { name: 'pirateface', ok: true, count: 1 }],
}

async function boot(page, answer = ANSWER) {
  const seen = []
  await page.route('**/backends/known', route => route.fulfill({ json: [] }))
  await page.route('**/api/resources', route => route.fulfill({ status: 403, json: { error: 'admin only' } }))
  await page.route('**/v1/models', route => route.fulfill({ json: { data: [] } }))
  await page.route(/\/api\/models\/search\?/, route => {
    seen.push(new URL(route.request().url()).searchParams)
    return typeof answer === 'function' ? answer(route) : route.fulfill({ json: answer })
  })
  await page.addInitScript(() => { try { localStorage.removeItem('import-form-tab') } catch { /* private mode */ } })
  return seen
}

const panel = (page) => page.getByTestId('model-search')

test.describe('Import: search online', () => {
  test('searches all providers for GGUF by default and lists the results', async ({ page }) => {
    const seen = await boot(page)
    await page.goto('/app/import-model')
    await expect(page.getByTestId('model-search-submit')).toBeDisabled()
    await page.getByTestId('model-search-input').fill('tiny')
    await page.getByTestId('model-search-submit').click()
    await expect(panel(page).locator('.import-search__row')).toHaveCount(3)
    expect(seen).toHaveLength(1)
    expect(seen[0].get('q')).toBe('tiny')
    expect(seen[0].get('format')).toBe('gguf')
    expect(seen[0].has('providers')).toBe(false)
    const first = panel(page).locator('.import-search__row').first()
    await expect(first).toContainText('acme/Tiny-GGUF')
    await expect(first).toContainText('3.4M')
    await expect(first).toContainText('apache-2.0')
    await expect(panel(page).locator('.import-search__row').nth(1)).toContainText('Gated')
    await expect(panel(page).locator('.import-search__row').nth(2)).toContainText('Seeders')
  })

  test('a provider switch and GGUF only change the query', async ({ page }) => {
    const seen = await boot(page)
    await page.goto('/app/import-model')
    await page.getByTestId('model-search-provider-huggingface').click()
    await page.getByTestId('model-search-gguf').click()
    await page.getByTestId('model-search-input').fill('x')
    await page.getByTestId('model-search-input').press('Enter')
    await expect(panel(page).locator('.import-search__row')).toHaveCount(3)
    expect(seen[0].get('providers')).toBe('pirateface')
    expect(seen[0].get('format')).toBe('any')
  })

  test('with every provider off nothing can be searched', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await page.getByTestId('model-search-provider-huggingface').click()
    await page.getByTestId('model-search-provider-pirateface').click()
    await page.getByTestId('model-search-input').fill('x')
    await expect(page.getByTestId('model-search-submit')).toBeDisabled()
    await expect(panel(page)).toContainText('Turn on at least one provider.')
  })

  test('a provider that is down is reported next to the results of the others', async ({ page }) => {
    await boot(page, {
      results: [ANSWER.results[0]],
      providers: [{ name: 'huggingface', ok: true, count: 1 }, { name: 'pirateface', ok: false, error: 'context deadline exceeded', count: 0 }],
    })
    await page.goto('/app/import-model')
    await page.getByTestId('model-search-input').fill('tiny')
    await page.getByTestId('model-search-submit').click()
    await expect(page.getByTestId('model-search-partial')).toContainText('Pirate Face did not answer')
    await expect(panel(page).locator('.import-search__row')).toHaveCount(1)
  })

  test('a failed request shows the error', async ({ page }) => {
    await boot(page, route => route.fulfill({ status: 400, json: { error: { message: 'q is required' } } }))
    await page.goto('/app/import-model')
    await page.getByTestId('model-search-input').fill('tiny')
    await page.getByTestId('model-search-submit').click()
    await expect(page.getByTestId('model-search-error')).toContainText('q is required')
  })

  test('no result says so', async ({ page }) => {
    await boot(page, { results: [], providers: [{ name: 'huggingface', ok: true, count: 0 }] })
    await page.goto('/app/import-model')
    await page.getByTestId('model-search-input').fill('zzz')
    await page.getByTestId('model-search-submit').click()
    await expect(page.getByTestId('model-search-empty')).toContainText('No models found')
  })

  test('Import fills the source field with the uri and does not start the import', async ({ page }) => {
    await boot(page)
    let imported = false
    await page.route('**/models/import-uri', route => { imported = true; return route.fulfill({ json: {} }) })
    await page.goto('/app/import-model')
    await page.getByTestId('model-search-input').fill('tiny')
    await page.getByTestId('model-search-submit').click()
    await panel(page).locator('.import-search__row').nth(2).getByTestId('model-search-pick').click()
    await expect(page.getByTestId('import-source-input')).toHaveValue('pirateface://acme/Tiny')
    await expect(page.getByTestId('import-source-kind')).toHaveAttribute('data-kind', 'pirateface')
    await panel(page).locator('.import-search__row').first().getByTestId('model-search-pick').click()
    await expect(page.getByTestId('import-source-input')).toHaveValue('huggingface://acme/Tiny-GGUF')
    await expect(page.getByTestId('import-source-kind')).toHaveAttribute('data-kind', 'huggingface')
    expect(imported).toBe(false)
  })
})
