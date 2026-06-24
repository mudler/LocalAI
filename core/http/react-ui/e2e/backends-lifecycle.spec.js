import { test, expect } from './coverage-fixtures.js'

const catalogBackends = [
  {
    id: 'llama-cpp',
    name: 'llama-cpp',
    description: 'GGUF inference',
    installed: true,
    version: '1.0.0',
    isMeta: true,
    isAlias: false,
    isDevelopment: false,
    tags: ['chat'],
  },
  {
    id: 'llama-cpp-cuda12',
    name: 'llama-cpp-cuda12',
    description: 'CUDA variant',
    installed: true,
    version: '1.0.0',
    isAlias: true,
    isDevelopment: false,
    tags: ['chat'],
  },
  {
    id: 'llama-cpp-development',
    name: 'llama-cpp-development',
    description: 'Development build',
    installed: true,
    version: '1.1.0-dev',
    isMeta: true,
    isAlias: false,
    isDevelopment: true,
    tags: ['chat'],
  },
]

const installedBackends = [
  {
    Name: 'llama-cpp',
    IsSystem: false,
    Version: '1.0.0',
    Metadata: { version: '1.0.0', installed_at: '2026-08-15T12:00:00Z' },
  },
  {
    Name: 'llama-cpp-cuda12',
    IsSystem: false,
    Version: '1.0.0',
    Metadata: { version: '1.0.0' },
  },
  {
    Name: 'llama-cpp-development',
    IsSystem: false,
    Version: '1.1.0-dev',
    Metadata: { version: '1.1.0-dev' },
  },
]

const upgrades = {
  'llama-cpp': {
    backend_name: 'llama-cpp',
    installed_version: '1.0.0',
    available_version: '1.1.0',
  },
}

async function mockBackendLifecycle(page) {
  await page.route('**/api/backends/upgrades', route => route.fulfill({ json: upgrades }))
  await page.route('**/api/backends?*', route => route.fulfill({
    json: { backends: catalogBackends },
  }))
  await page.route('**/backends', route => {
    if (new URL(route.request().url()).pathname === '/backends') {
      return route.fulfill({ json: installedBackends })
    }
    return route.continue()
  })
  await page.route('**/api/nodes', route => route.fulfill({
    json: [{ id: 'worker-1', name: 'GPU worker', status: 'healthy', node_type: 'backend' }],
  }))
}

const backendRow = (page, name) => page.locator(`[data-entity="${name}"]`)

test.describe('Backends lifecycle page', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackendLifecycle(page)
  })

  test('defaults invalid or absent views to Catalog and switches to Installed', async ({ page }) => {
    await page.goto('/app/backends')

    const catalog = page.getByRole('link', { name: 'Catalog', exact: true })
    const installed = page.getByRole('link', { name: 'Installed', exact: true })
    await expect(catalog).toHaveAttribute('aria-current', 'page')
    await expect(installed).not.toHaveAttribute('aria-current', 'page')

    await page.goto('/app/backends?view=unknown')

    await expect(catalog).toHaveAttribute('aria-current', 'page')
    await expect(installed).not.toHaveAttribute('aria-current', 'page')

    await installed.click()
    await expect(page).toHaveURL(/[?&]view=installed(?:&|$)/)
    await expect(installed).toHaveAttribute('aria-current', 'page')
    await expect(backendRow(page, 'llama-cpp')).toBeVisible()

    await page.getByRole('textbox', { name: /search installed backends/i }).fill('llama')
    await expect(page).toHaveURL(/[?&]q=llama(?:&|$)/)
    await page.getByRole('tab', { name: /user/i }).click()
    await expect(page).toHaveURL(/[?&]state=user(?:&|$)/)
  })

  test('restores Installed search, state, selection, and target-node scope from the URL', async ({ page }) => {
    await page.goto('/app/backends?view=installed&q=llama&state=upgradable&backend=llama-cpp&target=worker-1')

    await expect(page.getByRole('link', { name: 'Installed', exact: true })).toHaveAttribute('aria-current', 'page')
    await expect(page.getByRole('textbox', { name: /search installed backends/i })).toHaveValue('llama')
    await expect(page.getByRole('tab', { name: /updates/i })).toHaveAttribute('aria-selected', 'true')
    await expect(page.locator('[data-testid="backends-installed-pane"]')).toContainText('llama-cpp')
    await expect(page).toHaveURL(/[?&]target=worker-1(?:&|$)/)

    await page.getByRole('link', { name: 'Catalog', exact: true }).click()
    await expect(page).toHaveURL(/[?&]target=worker-1(?:&|$)/)
    await expect(page.getByText(/installing only on GPU worker/i)).toBeVisible()
    await backendRow(page, 'llama-cpp').click()
    await expect(page).toHaveURL(/[?&]backend=llama-cpp(?:&|$)/)
    await expect(page).toHaveURL(/[?&]target=worker-1(?:&|$)/)
  })

  test('keeps Upgrade and Reinstall on the same upgradable backend', async ({ page }) => {
    let reinstallRequests = 0
    await page.route('**/api/backends/install/llama-cpp', route => {
      reinstallRequests += 1
      return route.fulfill({ json: { status: 'ok' } })
    })
    await page.goto('/app/backends?view=installed&backend=llama-cpp')

    await expect(page.getByRole('button', { name: /upgrade to v1\.1\.0/i })).toBeVisible()
    await page.getByRole('button', { name: 'Actions for llama-cpp' }).click()
    const reinstall = page.getByRole('menuitem', { name: 'Reinstall backend' })
    await expect(reinstall).toBeVisible()
    await reinstall.click()
    await expect.poll(() => reinstallRequests).toBe(1)
  })

  test('keeps Reinstall beside Upgrade for an installed backend in Catalog', async ({ page }) => {
    await page.goto('/app/backends?view=catalog&backend=llama-cpp')

    await expect(page.locator('button[title^="Upgrade to"]')).toBeVisible()
    await expect(page.locator('button[title="Reinstall"]')).toBeVisible()
  })

  test('shows a backend action failure inline with the selected backend', async ({ page }) => {
    await page.route('**/api/backends/install/llama-cpp', route => route.fulfill({
      status: 500,
      json: { error: 'registry unavailable' },
    }))
    await page.goto('/app/backends?view=installed&backend=llama-cpp')

    await page.getByRole('button', { name: 'Actions for llama-cpp' }).click()
    await page.getByRole('menuitem', { name: 'Reinstall backend' }).click()

    const detail = page.locator('[data-testid="backends-installed-pane"]')
    await expect(detail.getByRole('alert')).toContainText('registry unavailable')
  })

  test('shows Upgrade All failures in the rendered global inline error', async ({ page }) => {
    await page.route('**/api/backends/upgrade/llama-cpp', route => route.fulfill({
      status: 500,
      json: { error: 'upgrade registry unavailable' },
    }))
    await page.goto('/app/backends?view=installed')

    await page.getByRole('button', { name: /upgrade all/i }).click()

    await expect(page.getByRole('alert')).toContainText('upgrade registry unavailable')
  })

  test('continues Upgrade All after an earlier backend fails', async ({ page }) => {
    let laterUpgradeRequests = 0
    await page.route('**/api/backends/upgrades', route => route.fulfill({
      json: {
        ...upgrades,
        'llama-cpp-cuda12': {
          backend_name: 'llama-cpp-cuda12',
          installed_version: '1.0.0',
          available_version: '1.1.0',
        },
      },
    }))
    await page.route('**/api/backends/upgrade/llama-cpp', route => route.fulfill({
      status: 500,
      json: { error: 'first registry unavailable' },
    }))
    await page.route('**/api/backends/upgrade/llama-cpp-cuda12', route => {
      laterUpgradeRequests += 1
      return route.fulfill({ json: { status: 'ok' } })
    })
    await page.goto('/app/backends?view=installed')

    await page.getByRole('button', { name: /upgrade all/i }).click()

    await expect(page.getByRole('alert')).toContainText('first registry unavailable')
    await expect.poll(() => laterUpgradeRequests).toBe(1)
  })

  test('refetches term-sensitive Catalog results after Installed changes and browser history', async ({ page }) => {
    const requestedTerms = []
    const visionBackend = {
      id: 'vision-cpp',
      name: 'vision-cpp',
      description: 'Vision inference',
      installed: false,
      isMeta: true,
      isAlias: false,
      isDevelopment: false,
      tags: ['vision'],
    }
    await page.route('**/api/backends?*', route => {
      const term = new URL(route.request().url()).searchParams.get('term') || ''
      requestedTerms.push(term)
      const backends = term === 'vision'
        ? [visionBackend]
        : term === 'llama'
          ? [catalogBackends[0]]
          : catalogBackends
      return route.fulfill({ json: { backends } })
    })

    await page.goto('/app/backends?view=catalog&q=llama')
    await expect.poll(() => requestedTerms.at(-1)).toBe('llama')
    await expect(backendRow(page, 'llama-cpp')).toBeVisible()

    await page.getByRole('link', { name: 'Installed', exact: true }).click()
    await page.getByRole('textbox', { name: /search installed backends/i }).fill('vision')
    await page.getByRole('link', { name: 'Catalog', exact: true }).click()
    await expect.poll(() => requestedTerms.at(-1)).toBe('vision')
    await expect(backendRow(page, 'vision-cpp')).toBeVisible()

    await page.goBack()
    await expect(page.getByRole('link', { name: 'Installed', exact: true })).toHaveAttribute('aria-current', 'page')
    await page.goBack()
    await expect(page.getByRole('link', { name: 'Catalog', exact: true })).toHaveAttribute('aria-current', 'page')
    await expect(page.getByPlaceholder(/search backends/i)).toHaveValue('llama')
    await expect.poll(() => requestedTerms.at(-1)).toBe('llama')
    await expect(backendRow(page, 'llama-cpp')).toBeVisible()
    await expect(backendRow(page, 'vision-cpp')).toHaveCount(0)
  })

  test('keeps variants and development builds opt-in', async ({ page }) => {
    await page.goto('/app/backends?view=installed')

    await expect(backendRow(page, 'llama-cpp')).toBeVisible()
    await expect(backendRow(page, 'llama-cpp-cuda12')).toHaveCount(0)
    await expect(backendRow(page, 'llama-cpp-development')).toHaveCount(0)

    await page.getByText(/variants \(1\)/i).click()
    await page.getByText(/development \(1\)/i).click()
    await expect(backendRow(page, 'llama-cpp-cuda12')).toBeVisible()
    await expect(backendRow(page, 'llama-cpp-development')).toBeVisible()
  })

  test('preserves confirmation before deleting an installed backend', async ({ page }) => {
    let deleteRequests = 0
    await page.route('**/api/backends/system/delete/llama-cpp', route => {
      deleteRequests += 1
      return route.fulfill({ json: { status: 'ok' } })
    })
    await page.goto('/app/backends?view=installed&backend=llama-cpp')

    await page.getByRole('button', { name: 'Actions for llama-cpp' }).click()
    await page.getByRole('menuitem', { name: 'Delete backend' }).click()
    await expect(page.getByRole('alertdialog')).toContainText('Delete backend llama-cpp?')
    expect(deleteRequests).toBe(0)

    await page.getByRole('button', { name: 'Delete', exact: true }).click()
    await expect.poll(() => deleteRequests).toBe(1)
  })

  test('narrow detail Back restores focus to the originating backend', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/backends?view=installed')

    const backend = backendRow(page, 'llama-cpp')
    await backend.click()
    await expect(page.locator('[data-testid="backends-installed-pane"]')).toContainText('llama-cpp')
    await expect(backend).not.toBeVisible()

    await page.locator('[data-testid="backends-installed-back"]').click()

    await expect(backend).toBeVisible()
    await expect(backend).toBeFocused()
  })
})
