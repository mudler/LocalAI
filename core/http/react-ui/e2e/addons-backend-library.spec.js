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

// The backend routes of the host. `installed` is the mutable list GET /backends
// answers. An install is a job: the request answers with its ID and the job
// route reports the steps in `phases`, one per read, then the result. `fail`
// ends the job with that error at the last step reached.
async function mockBackends(page, { installed = [], phases = ['resolving', 'size', 'fetching', 'writing'], fail, refuse } = {}) {
  const state = { installed: [...installed], installs: [], reads: 0 }
  await page.route(/\/backends$/, route => route.fulfill({ json: state.installed }))
  await page.route(/\/backends\/library\/install$/, route => {
    const body = route.request().postDataJSON()
    state.installs.push(body)
    state.reads = 0
    if (refuse) return route.fulfill(refuse)
    return route.fulfill({ json: { id: 'job-1', status_url: '/backends/jobs/job-1' } })
  })
  await page.route(/\/backends\/jobs\/job-1$/, route => {
    const body = state.installs.at(-1)
    const at = state.reads++
    const phase = phases[Math.min(at, phases.length - 1)]
    const last = at >= phases.length
    if (last && fail) return route.fulfill({ json: { phase, processed: true, error: fail, message: `error: ${fail}` } })
    if (last) {
      state.installed.push({ Name: body.name, Metadata: { library: body.sourceId } })
      return route.fulfill({ json: { phase: 'done', processed: true, progress: 100, message: 'completed' } })
    }
    return route.fulfill({ json: { phase, processed: false, progress: phase === 'fetching' ? 40 : 0 } })
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
    // The steps show while the job runs: a signed library lists the signature step.
    const steps = row.getByTestId('install-steps')
    await expect(steps.locator('[data-step]')).toHaveCount(5)
    await expect(steps.locator('[data-step="fetching"]')).toHaveAttribute('data-state', /active|done/)
    await expect(row.getByText('Installed as a backend on this node')).toBeVisible()
    await expect(steps).toHaveCount(0)
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
      phases: ['resolving'],
      fail: 'backend is already installed from another origin: "cpu-local-store"',
    })
    await page.goto('/app/addons?tab=browse')
    const row = page.locator('.cat-item', { hasText: 'cpu-local-store' })
    await row.getByRole('button', { name: 'Install' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Install' }).click()
    await expect(row.getByRole('alert')).toContainText('The backend was not installed.')
    await expect(row.getByRole('alert')).toContainText('already installed from another origin')
    await expect(row.getByRole('button', { name: 'Install' })).toBeEnabled()
    // The step that failed stays marked.
    await expect(row.locator('[data-step="resolving"]')).toHaveAttribute('data-state', 'failed')
    await expect(row.locator('[data-step="fetching"]')).toHaveAttribute('data-state', 'pending')
  })

  test('browse: a request the host refuses is shown with its reason', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, { installed: [], releases: [backend()] })
    await mockBackends(page, { refuse: { status: 409, json: { error: { type: 'backend_library', code: 409, message: 'this backend is already being installed' } } } })
    await page.goto('/app/addons?tab=browse')
    const row = page.locator('.cat-item', { hasText: 'cpu-local-store' })
    await row.getByRole('button', { name: 'Install' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Install' }).click()
    await expect(row.getByRole('alert')).toContainText('already being installed')
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

// The Backends page (phase 2): a backend that came from a library says so, and
// a newer build in its library is an update the administrator starts.
const PINNED = 'sha256:' + 'a1'.repeat(32)
const NEWER = 'sha256:' + 'b2'.repeat(32)

async function mockBackendsPage(page, { updates = true, phases = ['resolving', 'verifying', 'size', 'fetching', 'writing'] } = {}) {
  const state = { digest: PINNED, updates: updates ? [{ name: 'cpu-local-store', sourceId: 'official-backends', installedVersion: '0.0.0-latest.0a1056fd8633', installedDigest: PINNED, version: '0.0.0-latest.0b2', digest: NEWER, signed: true }] : [], installs: [], reads: 0 }
  await page.route('**/api/backends?*', route => route.fulfill({ json: { backends: [] } }))
  await page.route('**/api/backends/upgrades', route => route.fulfill({ json: { 'llama-cpp': { backend_name: 'llama-cpp', installed_version: '1', available_version: '2' } } }))
  await page.route(/\/backends$/, route => route.fulfill({
    json: [
      { Name: 'cpu-local-store', Metadata: { name: 'cpu-local-store', library: 'official-backends', digest: state.digest, version: '0.0.0-latest.0a1056fd8633' } },
      { Name: 'llama-cpp', Metadata: { name: 'llama-cpp', gallery_url: 'https://gallery.example/index.yaml', version: '1' } },
    ],
  }))
  await page.route(/\/backends\/library\/updates$/, route => route.fulfill({ json: { updates: state.updates } }))
  await page.route(/\/backends\/library\/install$/, route => {
    state.installs.push(route.request().postDataJSON())
    state.reads = 0
    return route.fulfill({ json: { id: 'job-2', status_url: '/backends/jobs/job-2' } })
  })
  await page.route(/\/backends\/jobs\/job-2$/, route => {
    const at = state.reads++
    if (at >= phases.length) {
      state.digest = NEWER
      state.updates = []
      return route.fulfill({ json: { phase: 'done', processed: true, progress: 100, message: 'completed' } })
    }
    return route.fulfill({ json: { phase: phases[at], processed: false, progress: 0 } })
  })
  return state
}

test.describe('Backends page and libraries', () => {
  test('a library backend carries its library and the digest it pinned; a gallery backend does not', async ({ page }) => {
    await signIn(page)
    await mockBackendsPage(page)
    await page.goto('/app/backends?view=installed')
    const row = page.locator('[data-entity="cpu-local-store"]')
    await expect(row.getByTestId('backend-library')).toContainText('Library: official-backends')
    await expect(page.locator('[data-entity="llama-cpp"]').getByTestId('backend-library')).toHaveCount(0)
    await row.click()
    const detail = page.getByTestId('backend-detail')
    await expect(detail.getByTestId('library-id')).toHaveText('official-backends')
    await expect(detail).toContainText('Pinned digest')
    await expect(detail).toContainText(PINNED)
  })

  test('a newer build in the library shows as an update and is installed only on request', async ({ page }) => {
    await signIn(page)
    const state = await mockBackendsPage(page)
    await page.goto('/app/backends?view=installed')
    const row = page.locator('[data-entity="cpu-local-store"]')
    await expect(row).toContainText('Update available')
    // Nothing is installed by looking at the page.
    expect(state.installs).toHaveLength(0)
    await row.click()
    await expect(page.getByTestId('library-update-facts')).toContainText('sha256:b2b2b2b2b2b2')
    await row.getByTestId('backend-library-update').click()
    await expect.poll(() => state.installs).toEqual([{ sourceId: 'official-backends', name: 'cpu-local-store', version: '0.0.0-latest.0b2' }])
    // The steps of the install show in the detail while it runs.
    await expect(page.getByTestId('backend-detail').getByTestId('install-steps').locator('[data-step]')).toHaveCount(5)
    // When it ends the row is current and pins the new digest.
    await expect(row).not.toContainText('Update available', { timeout: 15000 })
    await expect(page.getByTestId('backend-detail')).toContainText(NEWER)
    await expect(row.getByTestId('backend-library-update')).toHaveCount(0)
  })

  test('with no newer build the library backend is current, and gallery upgrades are unchanged', async ({ page }) => {
    await signIn(page)
    const state = await mockBackendsPage(page, { updates: false })
    await page.goto('/app/backends?view=installed')
    await expect(page.locator('[data-entity="cpu-local-store"]')).toContainText('Current')
    await expect(page.locator('[data-entity="cpu-local-store"]').getByTestId('backend-library-update')).toHaveCount(0)
    // The gallery backend keeps the gallery upgrade state and button.
    const gallery = page.locator('[data-entity="llama-cpp"]')
    await expect(gallery).toContainText('Update')
    await expect(gallery.getByRole('button', { name: 'Update llama-cpp' })).toBeVisible()
    expect(state.installs).toHaveLength(0)
  })
})
