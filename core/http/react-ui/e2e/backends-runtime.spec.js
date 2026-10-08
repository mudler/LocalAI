import { test, expect } from './coverage-fixtures.js'
import {
  CATALOG, FAILED_OP, INSTALLED, RUNNING_OP, UPGRADE_LLAMA, catalogBackend, installedBackend, mockOperate,
} from './operate-fixtures.js'

// Runtime, Backends: Installed and Catalog as two views of one list. A row says
// what the backend is doing now, carries the one button that matters, and opens
// into the rest.

const row = (page, name) => page.locator(`[data-entity="${name}"]`)
const jsonOk = route => route.fulfill({ json: { status: 'ok' } })

const VLLM_INSTALLING = { ...RUNNING_OP, name: 'vllm', id: 'vllm', fullName: 'localai@vllm', jobID: 'job-vllm', progress: 6 }

test.describe('Backends views', () => {
  test('Installed and Catalog carry their counts, and the current one says so', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends?view=installed')
    const installed = page.getByRole('link', { name: 'Installed', exact: true })
    const catalog = page.getByRole('link', { name: 'Catalog', exact: true })
    await expect(installed).toHaveAttribute('aria-current', 'page')
    await expect(installed).toContainText('3')
    await expect(catalog).toContainText('6')
    await expect(row(page, 'llama-cpp')).toBeVisible()
    await catalog.click()
    await expect(catalog).toHaveAttribute('aria-current', 'page')
    await expect(row(page, 'vllm')).toBeVisible()
  })

  test('the Installed list puts what has an update first', async ({ page }) => {
    await mockOperate(page, { upgrades: { whisper: { backend_name: 'whisper', installed_version: '1.7.2', available_version: '1.8.0' } } })
    await page.goto('/app/backends?view=installed')
    await expect(page.getByTestId('backend-row').first()).toHaveAttribute('data-entity', 'whisper')
    await expect(row(page, 'whisper')).toContainText('Update 1.8.0')
  })

  test('an empty Installed list sends you to the catalog', async ({ page }) => {
    await mockOperate(page, { installed: [] })
    await page.goto('/app/backends?view=installed')
    await expect(page.getByText('No backends installed yet')).toBeVisible()
    await page.getByRole('link', { name: 'Open the catalog' }).click()
    await expect(page.getByRole('link', { name: 'Catalog', exact: true })).toHaveAttribute('aria-current', 'page')
  })

  test('a first run recommends llama-cpp and installs it with one button', async ({ page }) => {
    await mockOperate(page, { catalog: CATALOG.map(b => ({ ...b, installed: false })), installed: [] })
    const calls = []
    await page.route('**/api/backends/install/llama-cpp', route => { calls.push('install'); return jsonOk(route) })
    await page.goto('/app/backends')
    const recommend = page.getByTestId('backends-recommend')
    await expect(recommend).toContainText('Start with llama-cpp')
    await recommend.getByRole('button', { name: 'Install llama-cpp' }).click()
    await expect.poll(() => calls).toEqual(['install'])
  })

  test('no recommendation once something is installed', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends')
    await expect(row(page, 'llama-cpp')).toBeVisible()
    await expect(page.getByTestId('backends-recommend')).toHaveCount(0)
  })
})

test.describe('Backend install progress', () => {
  test('shows progress in the row and offers Cancel with an undo window', async ({ page }) => {
    await mockOperate(page, { operations: [VLLM_INSTALLING] })
    const calls = []
    await page.route('**/api/operations/job-vllm/cancel', route => { calls.push('cancel'); return jsonOk(route) })
    await page.goto('/app/backends')

    const vllm = row(page, 'vllm')
    await expect(vllm.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '6')
    await expect(vllm).toContainText('6%')
    await vllm.getByRole('button', { name: 'Cancel vllm' }).click()
    await expect(page.getByTestId('backends-undo-toast')).toContainText('Cancelling vllm')
    expect(calls).toEqual([])
    await expect.poll(() => calls, { timeout: 15_000 }).toEqual(['cancel'])
  })

  test('Undo keeps the install going', async ({ page }) => {
    await mockOperate(page, { operations: [VLLM_INSTALLING] })
    const calls = []
    await page.route('**/api/operations/job-vllm/cancel', route => { calls.push('cancel'); return jsonOk(route) })
    await page.goto('/app/backends')
    await row(page, 'vllm').getByRole('button', { name: 'Cancel vllm' }).click()
    await page.getByTestId('backends-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('backends-undo-toast')).toHaveCount(0)
    await expect(row(page, 'vllm').getByRole('progressbar')).toBeVisible()
    await page.waitForTimeout(9_000)
    expect(calls).toEqual([])
  })

  test('a queued install says so and a failed one says why, with Retry', async ({ page }) => {
    await mockOperate(page, {
      operations: [
        { ...VLLM_INSTALLING, isQueued: true, progress: 0 },
        { ...FAILED_OP, id: 'diffusers', name: 'diffusers', fullName: 'localai@diffusers', jobID: 'job-diff', isBackend: true, error: 'no space left on device' },
      ],
    })
    const calls = []
    await page.route('**/api/backends/install/diffusers', route => { calls.push('install'); return jsonOk(route) })
    await page.goto('/app/backends')
    await expect(row(page, 'vllm')).toContainText('Queued')
    await expect(row(page, 'diffusers')).toContainText('Failed')
    await expect(row(page, 'diffusers')).toContainText('no space left on device')
    await row(page, 'diffusers').getByRole('button', { name: 'Retry diffusers' }).click()
    await expect.poll(() => calls).toEqual(['install'])
  })
})

test.describe('Backend updates', () => {
  test('Update on the row starts that update only', async ({ page }) => {
    await mockOperate(page, { upgrades: UPGRADE_LLAMA })
    const calls = []
    await page.route('**/api/backends/upgrade/llama-cpp', route => { calls.push('llama-cpp'); return jsonOk(route) })
    await page.goto('/app/backends?view=installed')
    await row(page, 'llama-cpp').getByRole('button', { name: 'Update llama-cpp' }).click()
    await expect.poll(() => calls).toEqual(['llama-cpp'])
  })

  test('Update all starts every update and counts them', async ({ page }) => {
    await mockOperate(page, {
      upgrades: {
        ...UPGRADE_LLAMA,
        whisper: { backend_name: 'whisper', installed_version: '1.7.2', available_version: '1.8.0' },
      },
    })
    const calls = []
    await page.route('**/api/backends/upgrade/*', route => { calls.push(new URL(route.request().url()).pathname.split('/').pop()); return jsonOk(route) })
    await page.goto('/app/backends?view=installed')
    await page.getByRole('button', { name: 'Update all (2)' }).click()
    await expect.poll(() => calls.sort()).toEqual(['llama-cpp', 'whisper'])
  })

  test('Check for updates asks the server to check and says what it found', async ({ page }) => {
    await mockOperate(page)
    let checks = 0
    await page.route('**/api/backends/upgrades/check', route => { checks += 1; return route.fulfill({ json: UPGRADE_LLAMA }) })
    await page.goto('/app/backends?view=installed')
    await page.getByRole('button', { name: 'Check for updates' }).click()
    await expect.poll(() => checks).toBe(1)
    await expect(page.getByRole('button', { name: 'Update all (1)' })).toBeVisible()
  })

  test('says when every backend is current', async ({ page }) => {
    await mockOperate(page)
    await page.route('**/api/backends/upgrades/check', route => route.fulfill({ json: {} }))
    await page.goto('/app/backends?view=installed')
    await page.getByRole('button', { name: 'Check for updates' }).click()
    await expect(page.getByText('Every installed backend is current')).toBeVisible()
  })
})

test.describe('Removing a backend', () => {
  test('names the models that would stop working', async ({ page }) => {
    await mockOperate(page)
    const calls = []
    await page.route('**/api/backends/system/delete/llama-cpp', route => { calls.push('delete'); return jsonOk(route) })
    await page.goto('/app/backends?view=installed')
    await row(page, 'llama-cpp').getByRole('button', { name: 'Actions for llama-cpp' }).click()
    await page.getByRole('menuitem', { name: 'Delete backend' }).click()

    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete backend llama-cpp?')
    await expect(dialog.getByTestId('backend-remove-warning')).toContainText('2 models ask for this backend')
    await expect(dialog.getByTestId('backend-remove-warning')).toContainText('gemma-3-12b-it, qwen3-8b-instruct')
    expect(calls).toEqual([])
    await dialog.getByRole('button', { name: 'Delete', exact: true }).click()
    await expect.poll(() => calls).toEqual(['delete'])
  })

  test('a backend nothing depends on has no warning', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends?view=installed')
    await row(page, 'kokoro').getByRole('button', { name: 'Actions for kokoro' }).click()
    await page.getByRole('menuitem', { name: 'Delete backend' }).click()
    await expect(page.getByRole('alertdialog')).toContainText('Delete backend kokoro?')
    await expect(page.getByTestId('backend-remove-warning')).toHaveCount(0)
  })

  test('a meta backend that points at the one removed is named', async ({ page }) => {
    await mockOperate(page, {
      installed: [
        installedBackend('llama-cpp', '0.9.4', { Metadata: { version: '0.9.4', meta_backend_for: 'cuda12-llama-cpp' } }),
        installedBackend('cuda12-llama-cpp', '0.9.4'),
      ],
      models: { data: [] },
    })
    await page.goto('/app/backends?view=installed&show_all=1')
    await row(page, 'cuda12-llama-cpp').getByRole('button', { name: 'Actions for cuda12-llama-cpp' }).click()
    await page.getByRole('menuitem', { name: 'Delete backend' }).click()
    await expect(page.getByTestId('backend-remove-warning')).toContainText('llama-cpp points at it and will stop resolving.')
  })

  test('keeps a model that uses a backend visible in the open row', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends?view=installed&backend=whisper')
    await expect(page.getByTestId('backend-used-by')).toContainText('Used by 1 model: whisper-large-v3')
  })

  test('a system backend is protected and offers no removal', async ({ page }) => {
    await mockOperate(page, { installed: [installedBackend('cpu-ggml', '1.0.0', { IsSystem: true })] })
    await page.goto('/app/backends?view=installed')
    await expect(row(page, 'cpu-ggml')).toContainText('Protected')
    await expect(row(page, 'cpu-ggml').getByRole('button', { name: /Actions for/ })).toHaveCount(0)
  })
})

test.describe('Install from URL and the catalog', () => {
  test('posts the image, the name and the alias', async ({ page }) => {
    await mockOperate(page)
    const bodies = []
    await page.route('**/api/backends/install-external', route => { bodies.push(route.request().postDataJSON()); return jsonOk(route) })
    await page.goto('/app/backends')
    await page.getByRole('button', { name: 'From URL' }).click()
    await page.getByLabel('OCI image, URL or path').fill('oci://quay.io/example/backend:latest')
    await page.getByLabel('Name (required for an OCI image)').fill('my-backend')
    await page.getByLabel('Alias (optional)').fill('mine')
    await page.getByRole('button', { name: 'Install', exact: true }).click()
    await expect.poll(() => bodies).toEqual([{ uri: 'oci://quay.io/example/backend:latest', name: 'my-backend', alias: 'mine' }])
  })

  test('asks for an address before posting anything', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends')
    await page.getByRole('button', { name: 'From URL' }).click()
    await page.getByRole('button', { name: 'Install', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('Enter an image, URL or path')
  })

  test('a catalog row installs a backend that is not there', async ({ page }) => {
    await mockOperate(page)
    const calls = []
    await page.route('**/api/backends/install/vllm', route => { calls.push('vllm'); return jsonOk(route) })
    await page.goto('/app/backends')
    await row(page, 'vllm').getByRole('button', { name: 'Install vllm' }).click()
    await expect.poll(() => calls).toEqual(['vllm'])
  })

  test('the open row links to the logs list', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends?view=installed&backend=llama-cpp')
    await page.getByTestId('backend-detail').getByRole('link', { name: 'Logs' }).click()
    await expect(page).toHaveURL(/\/app\/backend-logs$/)
  })

  test('shows no size, no rollback and no version history, because the API has none', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends?view=installed&backend=llama-cpp')
    await expect(page.getByTestId('backend-detail')).not.toContainText(/roll back|rollback|history/i)
    await expect(page.locator('.bk-table thead')).not.toContainText(/size/i)
  })
})

test.describe('Backends in a cluster', () => {
  const nodes = [
    { id: 'n1', name: 'gpu-box-1', status: 'healthy', node_type: 'backend' },
    { id: 'n2', name: 'gpu-box-2', status: 'healthy', node_type: 'backend' },
  ]

  test('a meta backend installs on all nodes, with a way to choose', async ({ page }) => {
    await mockOperate(page, { distributed: true, nodes, catalog: [catalogBackend('vllm', { isMeta: true })], installed: [] })
    await page.goto('/app/backends')
    const vllm = row(page, 'vllm')
    await expect(vllm.getByRole('button', { name: 'Install vllm' })).toContainText('Install on all')
    await vllm.getByRole('button', { name: 'More install options' }).click()
    await expect(page.getByRole('menuitem', { name: 'Install on specific nodes…' })).toBeVisible()
  })

  test('a hardware-specific build goes straight to choosing nodes', async ({ page }) => {
    await mockOperate(page, { distributed: true, nodes, catalog: [catalogBackend('cuda12-vllm')], installed: [] })
    await page.goto('/app/backends')
    await expect(row(page, 'cuda12-vllm').getByRole('button', { name: 'Choose nodes…' })).toBeVisible()
  })

  test('the Nodes column shows where a backend is installed', async ({ page }) => {
    await mockOperate(page, {
      distributed: true,
      nodes,
      installed: [installedBackend('llama-cpp', '0.9.4', { Nodes: [{ node_id: 'n1', node_name: 'gpu-box-1', node_status: 'healthy', version: '0.9.4' }] })],
    })
    await page.goto('/app/backends?view=installed')
    await expect(page.locator('.bk-table thead')).toContainText('Nodes')
    await expect(row(page, 'llama-cpp')).toContainText('gpu-box-1')
  })
})

test.describe('Backends layout', () => {
  test('a catalog row fits a phone with its progress and Cancel', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await mockOperate(page, { operations: [VLLM_INSTALLING], installed: INSTALLED })
    await page.goto('/app/backends')
    const vllm = row(page, 'vllm')
    await expect(vllm.getByRole('progressbar')).toBeVisible()
    await expect(vllm.getByRole('button', { name: 'Cancel vllm' })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })

  test('the install bar does not move when motion is reduced', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockOperate(page, { operations: [VLLM_INSTALLING] })
    await page.goto('/app/backends')
    const duration = await row(page, 'vllm').locator('.dk-progress-bar').evaluate(el => parseFloat(getComputedStyle(el).transitionDuration))
    expect(duration).toBeLessThan(0.001)
  })
})
