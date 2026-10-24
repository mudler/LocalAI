import { test, expect } from '@playwright/test'
import { ADMIN, release, sessionFor, mockAddons } from './addons-fixtures.js'

// Backends in the library: an entry has a kind, the add-on Browse tab lists
// add-ons only, and backends are handled on the Backends page. The server is the authority, so these specs drive the page
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
  test('browse: a backend entry is not listed, and add-ons are', async ({ page }) => {
    await signIn(page)
    const state = await mockAddons(page, { installed: [], releases: [release('tables'), backend()] })
    await mockBackends(page)
    await page.goto('/app/addons?tab=browse')
    // Backends belong to the Backends page: only the add-on is listed.
    await expect(page.locator('.cat-item')).toHaveCount(1)
    await expect(page.locator('.cat-item', { hasText: 'Tables' })).toBeVisible()
    await expect(page.locator('.cat-item', { hasText: 'cpu-local-store' })).toHaveCount(0)
    await expect(page.getByTestId('entry-kind')).toHaveCount(0)
    // The page asks for add-ons only.
    expect(state.log.dispatch.find(d => d.action === 'catalog-search').arguments.kind).toBe('addon')
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
