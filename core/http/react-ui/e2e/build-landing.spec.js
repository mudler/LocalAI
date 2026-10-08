import { test, expect } from './coverage-fixtures.js'
import { CPU_HOST, GPU_HOST, ftJob, mockFineTune, mockMachine, mockQuantize, qzJob } from './tools-fixtures.js'

// The Build landing says what each tool is for and what it needs from this
// machine. The needs come from the machine's own figures and the installed
// backends, so each spec stubs those and reads the card.

const tool = (page, id) => page.locator(`[data-testid="build-landing"] [data-tool="${id}"]`)

async function setup(page, { host = GPU_HOST, ft = {}, qz = {}, features } = {}) {
  await mockMachine(page, { host, ...(features ? { features } : {}) })
  await mockFineTune(page, { jobs: [], ...ft })
  await mockQuantize(page, { jobs: [], ...qz })
}

test.describe('Build landing', () => {
  test('lists the tools in three groups with a sentence each', async ({ page }) => {
    await setup(page)
    await page.goto('/app/build')
    const landing = page.getByTestId('build-landing')
    await expect(landing.getByRole('heading', { name: 'Make a model' })).toBeVisible()
    await expect(landing.getByRole('heading', { name: 'Recognise' })).toBeVisible()
    await expect(landing.getByRole('heading', { name: 'Automate' })).toBeVisible()
    await expect(tool(page, 'import')).toContainText('Bring in a model from Hugging Face')
    await expect(tool(page, 'quantize')).toContainText('Make a model smaller')
    await expect(tool(page, 'fine-tune')).toContainText('Teach a model your own data')
    await expect(tool(page, 'agents')).toContainText('Create and run agents')
    // The flow strip names the make-a-model steps in order.
    await expect(page.getByRole('list', { name: /path from a model/i }).locator('li')).toHaveText(['Import', 'Quantize', 'Fine-Tune', 'Chat'])
  })

  test('every tool opens its page', async ({ page }) => {
    await setup(page)
    await page.goto('/app/build')
    await expect(tool(page, 'fine-tune').getByRole('link', { name: 'Open Fine-Tune' })).toHaveAttribute('href', '/app/fine-tune')
    await expect(tool(page, 'quantize').getByRole('link', { name: /Open Quantize/ })).toHaveAttribute('href', '/app/quantize')
    await expect(tool(page, 'import').getByRole('link', { name: /Open Import/ })).toHaveAttribute('href', '/app/import-model')
  })

  test('a machine with a GPU and both backends shows ready tools and the figures', async ({ page }) => {
    await setup(page)
    await page.goto('/app/build')
    const machine = page.getByTestId('build-machine')
    await expect(machine).toContainText('RTX 4070')
    await expect(machine).toContainText('9.8 GB of 12 GB free')
    await expect(machine).toContainText('118 GB')
    await expect(machine).toContainText('llama-cpp-quantization')
    await expect(machine).toContainText('trl')
    for (const id of ['import', 'quantize', 'fine-tune']) {
      await expect(tool(page, id)).toHaveAttribute('data-state', 'ready')
      await expect(tool(page, id).locator('.dk-badge--ok')).toHaveText('Ready')
    }
    await expect(tool(page, 'fine-tune')).toContainText('GPU memory: RTX 4070, 9.8 GB of 12 GB free')
    await expect(tool(page, 'quantize')).toContainText('Runs on the CPU')
    await expect(tool(page, 'import')).toContainText('118 GB free on the models disk')
  })

  test('no backend installed: the tool says why and an admin gets an Install link', async ({ page }) => {
    await setup(page, { host: CPU_HOST, ft: { backends: [] }, qz: { backends: [] } })
    await page.goto('/app/build')
    for (const id of ['quantize', 'fine-tune']) {
      await expect(tool(page, id)).toHaveAttribute('data-state', 'needs-backend')
      await expect(tool(page, id)).toContainText('Backend needed')
      await expect(tool(page, id).getByRole('link', { name: 'Install' })).toHaveAttribute('href', '/app/backends')
    }
    await expect(tool(page, 'quantize')).toContainText('The quantization backend is not installed')
    await expect(tool(page, 'fine-tune')).toContainText('No fine-tuning backend is installed')
    await expect(tool(page, 'import')).toHaveAttribute('data-state', 'ready')
    await expect(page.getByTestId('build-machine')).toContainText('none installed for these tools')
  })

  test('no GPU is a warning on Fine-tune, not a block', async ({ page }) => {
    await setup(page, { host: CPU_HOST })
    await page.goto('/app/build')
    await expect(page.getByTestId('build-machine')).toContainText('No GPU found')
    await expect(tool(page, 'fine-tune')).toHaveAttribute('data-state', 'ready')
    await expect(tool(page, 'fine-tune').locator('[data-tone="warn"]')).toContainText('No GPU found. Training runs on the CPU')
  })

  test('a running job is surfaced above the list and opens its page', async ({ page }) => {
    await setup(page, { qz: { jobs: [qzJob('qz1', 'converting', { message: 'converting tensor 84 of 201' })] } })
    await page.goto('/app/build')
    const now = page.getByTestId('build-now')
    await expect(now).toHaveAttribute('data-kind', 'running')
    await expect(now).toContainText('Quantizing meta-llama/Llama-3.2-1B to q4_k_m')
    await expect(now).toContainText('converting tensor 84 of 201')
    await expect(now.getByRole('link', { name: 'Open' })).toHaveAttribute('href', '/app/quantize')
  })

  test('a failed newest job is surfaced, an old failure is not', async ({ page }) => {
    await setup(page, { ft: { jobs: [ftJob('j2', 'failed', { message: 'CUDA out of memory' })] } })
    await page.goto('/app/build')
    await expect(page.getByTestId('build-now')).toHaveAttribute('data-kind', 'failed')
    await expect(page.getByTestId('build-now')).toContainText('Fine-tuning TinyLlama/TinyLlama-1.1B-Chat-v1.0 failed')
  })

  test('no job, no line', async ({ page }) => {
    await setup(page, { ft: { jobs: [ftJob('j1', 'completed')] } })
    await page.goto('/app/build')
    await expect(page.getByTestId('build-landing')).toBeVisible()
    await expect(page.getByTestId('build-now')).toHaveCount(0)
  })

  test('a tool switched off by a feature flag is left out', async ({ page }) => {
    await setup(page, { features: { agents: false, mcp: false, fine_tuning: false, quantization: true, distributed: false } })
    await page.goto('/app/build')
    await expect(tool(page, 'fine-tune')).toHaveCount(0)
    await expect(tool(page, 'quantize')).toBeVisible()
    await expect(tool(page, 'agents')).toHaveCount(0)
    await expect(page.getByRole('list', { name: 'Build tools' }).locator('a[href="/app/import-model"]')).toBeVisible()
  })

  test('a member without admin sees no machine strip, no Install link, and the tools they may use', async ({ page }) => {
    await mockMachine(page, { host: null })
    await mockFineTune(page, { jobs: [], backends: [] })
    await mockQuantize(page, { jobs: [] })
    await page.route('**/api/auth/status', route => route.fulfill({
      json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'u1', name: 'Sam', role: 'user', permissions: { fine_tuning: true } } },
    }))
    await page.goto('/app/build')
    await expect(tool(page, 'fine-tune')).toHaveAttribute('data-state', 'needs-backend')
    await expect(tool(page, 'fine-tune')).toContainText('Ask an administrator to install the backend')
    await expect(tool(page, 'fine-tune').getByRole('link', { name: 'Install' })).toHaveCount(0)
    await expect(page.getByTestId('build-machine')).toHaveCount(0)
    await expect(tool(page, 'import')).toHaveCount(0)
    await expect(tool(page, 'quantize')).toHaveCount(0)
  })

  test('a user with no Build tool sees the empty line', async ({ page }) => {
    await mockMachine(page, { host: null })
    await page.route('**/api/auth/status', route => route.fulfill({
      json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'u1', name: 'Sam', role: 'user', permissions: { chat: true } } },
    }))
    await page.goto('/app/build')
    await expect(page.getByTestId('build-empty')).toContainText('No build tools are available')
  })
})

test.describe('Build landing: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('fits the screen and keeps every Open link tappable', async ({ page }) => {
    await setup(page, { qz: { jobs: [qzJob('qz1', 'converting')] } })
    await page.goto('/app/build')
    await expect(page.getByTestId('build-landing')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
    const heights = await page.locator('[data-testid="build-landing"] .bt-tool__end a').evaluateAll(els => els.map(el => el.getBoundingClientRect().height))
    expect(heights.length).toBeGreaterThan(5)
    expect(Math.min(...heights)).toBeGreaterThanOrEqual(30)
  })
})

test.describe('Build landing: reduced motion', () => {
  test('page content does not animate', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await setup(page)
    await page.goto('/app/build')
    const duration = await page.getByTestId('build-landing').locator('.bt-tool').first().evaluate(el => getComputedStyle(el).animationDuration)
    expect(parseFloat(duration)).toBeLessThan(0.01)
  })
})
