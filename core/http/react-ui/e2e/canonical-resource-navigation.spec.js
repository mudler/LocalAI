import { test, expect } from './coverage-fixtures.js'

async function backendInventory(page, installed) {
  await page.route('**/api/backends?*', route => route.fulfill({ json: {
    backends: [{ id: 'llama-cpp', name: 'llama-cpp', installed: true }],
  } }))
  await page.route('**/api/backends/upgrades', route => route.fulfill({ json: {} }))
  await page.route('**/backends', route => route.fulfill({ json: installed }))
}

function urlState(page) {
  const url = new URL(page.url())
  return { path: url.pathname, params: url.searchParams }
}

test.describe('Canonical resource navigation', () => {
  test('redirects legacy model management state and replaces browser history', async ({ page }) => {
    await page.goto('/app')
    await page.goto('/app/manage?tab=models&sel=alpha&mq=alp&mf=running')

    await expect.poll(() => urlState(page).path).toBe('/app/models')
    const state = urlState(page)
    expect(state.params.get('view')).toBe('installed')
    expect(state.params.get('model')).toBe('alpha')
    expect(state.params.get('q')).toBe('alp')
    expect(state.params.get('state')).toBe('running')

    await page.goBack()
    await expect(page).toHaveURL(/\/app\/?$/)
  })

  test('redirects legacy backend management state including visibility flags', async ({ page }) => {
    // The installed view clears selections absent from its inventory.
    await backendInventory(page, [{ Name: 'llama-cpp', IsSystem: false }])
    await page.goto('/app/manage?tab=backends&sel=llama-cpp&bq=llama&bf=user&bv=1&bd=1')

    const row = page.locator('[data-entity="llama-cpp"]')
    await expect(row).toBeVisible()
    await expect(row).toHaveAttribute('data-selected', 'true')
    await expect(page).toHaveURL('/app/backends?view=installed&backend=llama-cpp&q=llama&state=user&show_all=1&development=1')
    const state = urlState(page)
    expect(state.params.get('view')).toBe('installed')
    expect(state.params.get('backend')).toBe('llama-cpp')
    expect(state.params.get('q')).toBe('llama')
    expect(state.params.get('state')).toBe('user')
    expect(state.params.get('show_all')).toBe('1')
    expect(state.params.get('development')).toBe('1')
  })

  test('clears a missing installed backend selection without losing URL refinements', async ({ page }) => {
    await backendInventory(page, [])
    await page.goto('/app/backends?view=installed&backend=llama-cpp&q=llama&state=user&show_all=1&development=1&target=worker-alpha')

    await expect(page).toHaveURL('/app/backends?view=installed&q=llama&state=user&show_all=1&development=1&target=worker-alpha')
    await expect(page.locator('[data-entity="llama-cpp"]')).toHaveCount(0)
  })

  test('defaults legacy management links to Installed Models', async ({ page }) => {
    await page.goto('/app/manage')

    await expect.poll(() => urlState(page).path).toBe('/app/models')
    expect(urlState(page).params.get('view')).toBe('installed')
  })

  test('names the canonical sidebar destination Models and removes Host from Operate', async ({ page }) => {
    await page.goto('/app')

    const sidebar = page.locator('.sidebar-nav')
    await expect(sidebar.getByRole('link', { name: 'Models', exact: true })).toBeVisible()
    await expect(sidebar.getByRole('link', { name: 'Discover', exact: true })).toHaveCount(0)

    await sidebar.getByRole('link', { name: 'Administration', exact: true }).click()
    const operateBar = page.locator('.dk-hubtabs')
    await expect(operateBar.getByRole('link', { name: /Runtime/ })).toBeVisible()
    await expect(operateBar.getByRole('link', { name: /Host/ })).toHaveCount(0)
  })
})
