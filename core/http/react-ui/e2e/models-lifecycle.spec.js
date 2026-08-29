import { test, expect } from './coverage-fixtures.js'

const installedModels = [
  {
    id: 'alpha',
    backend: 'llama-cpp',
    capabilities: ['FLAG_CHAT'],
    pinned: true,
  },
  {
    id: 'beta',
    backend: 'llama-cpp',
    capabilities: ['embeddings'],
  },
  {
    id: 'all',
    backend: 'llama-cpp',
    capabilities: ['chat'],
  },
  {
    id: 'remote-model',
    backend: 'llama-cpp',
    capabilities: ['chat'],
    loaded_on: [{
      node_id: 'worker-1',
      node_name: 'Worker one',
      node_status: 'healthy',
      state: 'loaded',
    }],
  },
  {
    id: 'disabled-model',
    backend: 'llama-cpp',
    capabilities: ['chat'],
    disabled: true,
  },
]

const galleryModels = installedModels.map(model => ({
  name: model.id,
  backend: model.backend,
  installed: true,
  description: `Gallery details for ${model.id}`,
  tags: model.capabilities,
}))

async function mockModelLifecycle(page) {
  let loadedModels = ['alpha']

  await page.route('**/api/models/capabilities', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ data: installedModels }),
  }))
  await page.route('**/api/models?*', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      models: galleryModels,
      allBackends: ['llama-cpp'],
      availableModels: galleryModels.length,
      installedModels: galleryModels.length,
      totalPages: 1,
    }),
  }))
  await page.route('**/api/models', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      models: galleryModels,
      allBackends: ['llama-cpp'],
      availableModels: galleryModels.length,
      installedModels: galleryModels.length,
      totalPages: 1,
    }),
  }))
  await page.route('**/api/models/estimate/*', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({}),
  }))
  await page.route('**/api/backends/usecases', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ 'llama-cpp': ['chat', 'embeddings'] }),
  }))
  await page.route('**/api/aliases', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify([]),
  }))
  await page.route('**/api/nodes', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify([{ id: 'worker-1', name: 'Worker one' }]),
  }))
  await page.route('**/system', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ loaded_models: loadedModels.map(id => ({ id })) }),
  }))
  await page.route('**/backend/shutdown', async route => {
    const body = route.request().postDataJSON()
    loadedModels = loadedModels.filter(id => id !== body.model)
    await route.fulfill({ contentType: 'application/json', body: '{}' })
  })

}

const installedRail = page => page.locator('[data-testid="installed-models"]')
const installedPane = page => page.locator('[data-testid="installed-models-pane"]')

test.describe('Models lifecycle', () => {
  test.beforeEach(async ({ page }) => {
    await mockModelLifecycle(page)
  })

  test('Explore is the default for absent and invalid views', async ({ page }) => {
    await page.goto('/app/models')

    const explore = page.getByRole('link', { name: 'Explore', exact: true })
    const installed = page.getByRole('link', { name: 'Installed', exact: true })
    await expect(explore).toHaveAttribute('aria-current', 'page')
    await expect(installed).not.toHaveAttribute('aria-current', 'page')
    await expect(page.locator('[data-testid="discover"]')).toBeVisible()

    await page.goto('/app/models?view=not-a-view')
    await expect(explore).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('[data-testid="discover"]')).toBeVisible()
  })

  test('an installed Explore model opens or moves to management without destructive actions', async ({ page }) => {
    await page.goto('/app/models?model=alpha')

    await expect(page.getByRole('button', { name: 'Open Chat' })).toBeVisible()
    const manage = page.getByRole('button', { name: 'Manage installation' })
    await expect(manage).toBeVisible()
    await expect(page.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Reinstall', exact: true })).toHaveCount(0)

    await manage.click()
    await expect(page).toHaveURL(/[?&]view=installed/)
    await expect(page).toHaveURL(/[?&]model=alpha/)
  })

  test('switches to Installed and restores URL state through history', async ({ page }) => {
    await page.goto('/app/models')
    await page.getByRole('link', { name: 'Installed', exact: true }).click()

    await expect(page).toHaveURL(/[?&]view=installed/)
    await expect(page.getByRole('link', { name: 'Installed', exact: true })).toHaveAttribute('aria-current', 'page')
    await expect(installedRail(page)).toBeVisible()

    const search = page.getByRole('textbox', { name: 'Search installed models' })
    await search.fill('beta')
    await page.getByRole('tab', { name: /Idle$/ }).click()
    await page.locator('[data-entity="beta"]').click()

    await expect(page).toHaveURL(/[?&]q=beta/)
    await expect(page).toHaveURL(/[?&]state=idle/)
    await expect(page).toHaveURL(/[?&]model=beta/)
    await expect(installedPane(page)).toContainText('beta')

    await page.goBack()
    await expect(page).not.toHaveURL(/[?&]model=beta/)
    await expect(search).toHaveValue('beta')
    await expect(page.getByRole('tab', { name: /Idle$/ })).toHaveAttribute('aria-selected', 'true')

    await page.goForward()
    await expect(page).toHaveURL(/[?&]model=beta/)
    await expect(installedPane(page)).toContainText('beta')
  })

  test('restores a selected installed model and runtime state from the URL', async ({ page }) => {
    await page.goto('/app/models?view=installed&q=remote&state=distributed&model=remote-model')

    await expect(page.getByRole('textbox', { name: 'Search installed models' })).toHaveValue('remote')
    await expect(page.getByRole('tab', { name: /Distributed$/ })).toHaveAttribute('aria-selected', 'true')
    await expect(installedPane(page)).toContainText('remote-model')
    await expect(installedPane(page)).toContainText('Worker one')
  })

  test('stops a running model with confirmation', async ({ page }) => {
    await page.goto('/app/models?view=installed&model=alpha')

    const stop = page.getByRole('button', { name: 'Stop', exact: true })
    await expect(stop).toBeVisible()
    await stop.click()
    await expect(page.getByRole('alertdialog')).toContainText('Stop model alpha?')

    const requestPromise = page.waitForRequest(request => request.url().endsWith('/backend/shutdown'))
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop', exact: true }).click()
    const request = await requestPromise
    expect(request.postDataJSON()).toEqual({ model: 'alpha' })
    await expect(page.getByRole('button', { name: 'Load', exact: true })).toBeVisible({ timeout: 3_000 })
  })

  test('keeps a runtime failure inline with its model', async ({ page }) => {
    await page.route('**/backend/load', route => route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: { message: 'engine unavailable' } }),
    }))
    await page.goto('/app/models?view=installed&model=beta')

    await page.getByRole('button', { name: 'Load', exact: true }).click()

    const alert = installedPane(page).getByRole('alert')
    await expect(alert).toContainText('Could not load beta: engine unavailable')
  })

  test('keeps a failed delete selected so its error remains inline', async ({ page }) => {
    await page.route('**/models/delete/beta', route => route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: { message: 'model is busy' } }),
    }))
    await page.goto('/app/models?view=installed&model=beta')

    await page.getByRole('button', { name: 'Actions for beta' }).click()
    await page.getByRole('menuitem', { name: 'Delete model' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete model' }).click()

    await expect(installedPane(page).getByRole('heading', { name: 'beta', exact: true })).toBeVisible()
    await expect(installedPane(page).getByRole('alert')).toContainText('Could not delete beta: model is busy')
  })

  test('clears selection after deleting an installed model', async ({ page }) => {
    await page.route('**/models/delete/beta', route => route.fulfill({
      contentType: 'application/json',
      body: '{}',
    }))
    await page.goto('/app/models?view=installed&model=beta')

    await page.getByRole('button', { name: 'Actions for beta' }).click()
    await page.getByRole('menuitem', { name: 'Delete model' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete model' }).click()

    await expect(page).not.toHaveURL(/[?&]model=beta(?:&|$)/)
    await expect(installedPane(page).getByRole('heading', { name: 'beta', exact: true })).toHaveCount(0)
  })

  test('refreshes distributed runtime state every ten seconds', async ({ page }) => {
    let runtimeRequests = 0
    await page.route('**/system', route => {
      runtimeRequests += 1
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ loaded_models: [{ id: 'alpha' }] }),
      })
    })
    await page.goto('/app/models?view=installed')

    await expect.poll(() => runtimeRequests).toBeGreaterThan(0)
    const initialRequests = runtimeRequests
    await expect.poll(() => runtimeRequests, { timeout: 12_000 }).toBeGreaterThan(initialRequests)
  })

  test('preserves literal all values for search and model selection', async ({ page }) => {
    await page.goto('/app/models?view=installed&q=all&model=all')

    await expect(page.getByRole('textbox', { name: 'Search installed models' })).toHaveValue('all')
    await expect(installedPane(page).getByRole('heading', { name: 'all', exact: true })).toBeVisible()
    await expect(page).toHaveURL(/[?&]q=all(?:&|$)/)
    await expect(page).toHaveURL(/[?&]model=all(?:&|$)/)
  })

  test('narrow detail Back restores focus to the originating model', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/models?view=installed')

    const model = page.locator('[data-entity="beta"]')
    await model.click()
    await expect(page.locator('[data-testid="installed-models-pane"]')).toContainText('beta')
    await expect(model).not.toBeVisible()

    await page.locator('[data-testid="installed-models-back"]').click()

    await expect(model).toBeVisible()
    await expect(model).toBeFocused()
  })
})
