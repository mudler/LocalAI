import { test, expect } from '@playwright/test'
import { ADMIN, release, sessionFor, mockAddons } from './addons-fixtures.js'

// Backends in the library (phase 1): an entry has a kind, the Browse tab shows
// it, and a backend installs through the backend route, not the add-on
// lifecycle. The server is the authority, so these specs drive the page
// against a stand-in and assert what the page sent.

const POLICY = { requireSignature: true, issuer: 'https://token.actions.githubusercontent.com', identityRegex: '^https://github\\.com/o/r/\\.github/workflows/w\\.yml@refs/(heads/master|tags/.+)$' }
const backend = (over = {}) => release('cpu-local-store', {
  kind: 'backend', name: 'cpu-local-store', alias: 'local-store', description: 'A vector store.', version: '0.0.0-latest.0a1056fd8633',
  sourceId: 'official-backends', artifact: 'oci://registry.example/backends:latest-cpu-local-store', policy: POLICY, ...over,
})

async function signIn(page) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
}

// The backend routes of the host. `installed` is the mutable list GET /backends answers.
async function mockBackends(page, { installed = [], install } = {}) {
  const state = { installed: [...installed], installs: [] }
  await page.route(/\/backends$/, route => route.fulfill({ json: state.installed }))
  await page.route(/\/backends\/library\/install$/, route => {
    const body = route.request().postDataJSON()
    state.installs.push(body)
    if (install) {
      const custom = install(body)
      if (custom) return route.fulfill(custom)
    }
    state.installed.push({ Name: body.name, Metadata: { library: body.sourceId } })
    return route.fulfill({ json: { status: 'installed', name: body.name, sourceId: body.sourceId } })
  })
  return state
}

test.describe('Library backends', () => {
  test('browse: an entry shows its kind, and add-ons are still add-ons', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, { installed: [], releases: [release('tables'), backend()] })
    await mockBackends(page)
    await page.goto('/app/addons?tab=browse')
    await expect(page.locator('.cat-item')).toHaveCount(2)
    await expect(page.locator('.cat-item', { hasText: 'Tables' }).getByTestId('entry-kind')).toHaveText('Add-on')
    const row = page.locator('.cat-item', { hasText: 'cpu-local-store' })
    await expect(row.getByTestId('entry-kind')).toHaveText('Backend')
    await expect(row.getByTestId('entry-kind')).toHaveAttribute('data-kind', 'backend')
    // The page asks for every kind this host reads.
    expect(state.log.dispatch.find(d => d.action === 'catalog-search').arguments.kind).toBe('all')
  })

  test('browse: a backend installs through the backend route and never through the add-on lifecycle', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, { installed: [], releases: [backend()] })
    const backends = await mockBackends(page)
    await page.goto('/app/addons?tab=browse')
    const row = page.locator('.cat-item', { hasText: 'cpu-local-store' })
    await row.getByRole('button', { name: 'Install' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Install cpu-local-store?')
    await expect(dialog).toContainText('backend gallery is not changed')
    await expect(dialog).toContainText('native code')
    expect(backends.installs).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Install' }).click()
    await expect(row.getByText('Installed as a backend on this node')).toBeVisible()
    expect(backends.installs).toEqual([{ sourceId: 'official-backends', name: 'cpu-local-store', version: '0.0.0-latest.0a1056fd8633' }])
    expect(state.log.dispatch.filter(d => d.action === 'install')).toHaveLength(0)
    // Once installed the entry says so and offers no second install.
    await expect(row.getByText('Installed', { exact: true })).toBeVisible()
    await expect(row.getByRole('button', { name: 'Install' })).toBeDisabled()
  })

  test('browse: a refusal of the host is shown with its own reason', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, { installed: [], releases: [backend()] })
    await mockBackends(page, {
      install: () => ({ status: 409, json: { error: { type: 'backend_library', code: 409, message: 'backend is already installed from another origin: "cpu-local-store"' } } }),
    })
    await page.goto('/app/addons?tab=browse')
    const row = page.locator('.cat-item', { hasText: 'cpu-local-store' })
    await row.getByRole('button', { name: 'Install' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Install' }).click()
    await expect(row.getByRole('alert')).toContainText('The backend was not installed.')
    await expect(row.getByRole('alert')).toContainText('already installed from another origin')
    await expect(row.getByRole('button', { name: 'Install' })).toBeEnabled()
  })

  test('browse: the details give the kind, the alias and the signer pattern', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, { installed: [], releases: [backend()] })
    await mockBackends(page)
    await page.goto('/app/addons?tab=browse')
    await page.locator('.cat-item', { hasText: 'cpu-local-store' }).getByRole('button', { name: 'Details' }).click()
    await expect(page.getByTestId('details-kind')).toHaveText('Backend')
    await expect(page.getByText('local-store', { exact: true })).toBeVisible()
    await expect(page.getByText('workflows/w', { exact: false })).toBeVisible()
  })

  test('libraries: a signer pattern is edited and saved without an exact identity, and skipped entries are reported', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, {
      sources: [{ id: 'official-backends', name: 'Official backends', url: 'https://catalog.example/backends.yaml', enabled: true, policy: POLICY, refresh: { checkedAt: new Date().toISOString(), lastSuccess: new Date().toISOString(), count: 1, skipped: 2, skippedKinds: ['model'], failed: false } }],
    })
    await mockBackends(page)
    await page.goto('/app/addons?tab=libraries')
    await expect(page.getByTestId('skipped-kinds')).toContainText('2 entries skipped: kind model')
    await page.getByRole('button', { name: 'Edit' }).click()
    const pattern = page.getByLabel('Signer identity pattern (optional)')
    await expect(pattern).toHaveValue(POLICY.identityRegex)
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => state.log.dispatch.filter(d => d.action === 'catalog-source-save').length).toBe(1)
    const saved = state.log.dispatch.find(d => d.action === 'catalog-source-save').arguments
    expect(saved.policy).toEqual(POLICY)
  })
})
