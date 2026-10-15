import { test, expect } from './coverage-fixtures.js'
import { CPU_HOST, GB, GPU_HOST } from './tools-fixtures.js'

// Import is a guided flow: say where the model is, review what LocalAI can tell
// from the source and what the form will send, run the import, see what it made.
// The server returns no preview before the import starts, so the review is built
// from the spelling of the source, the form, and the machine. These specs pin
// what it says and what it leaves out.

const BACKENDS = [
  { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'transformers', modality: 'text', auto_detect: true, installed: true, description: 'Runs the full-precision weights in Python.' },
  { name: 'vllm', modality: 'text', auto_detect: true, installed: false, description: 'A fast serving engine for NVIDIA GPUs.' },
  { name: 'piper', modality: 'tts', auto_detect: true, installed: false },
]

async function boot(page, { host = GPU_HOST, installed = ['qwen3-8b-instruct', 'my-model'] } = {}) {
  await page.route('**/backends/known', route => route.fulfill({ json: BACKENDS }))
  await page.route('**/api/resources', route => (host ? route.fulfill({ json: host }) : route.fulfill({ status: 403, json: { error: 'admin only' } })))
  await page.route('**/v1/models', route => route.fulfill({ json: { data: installed.map(id => ({ id })) } }))
  await page.addInitScript(() => { try { localStorage.removeItem('import-form-tab'); localStorage.removeItem('import-form-options') } catch { /* private mode */ } })
}

const expectStep = (page, key, state) => expect(page.locator(`[data-testid="tool-steps"] [data-step="${key}"]`)).toHaveAttribute('data-state', state)
const source = (page) => page.getByTestId('import-source-input')
const check = (page, id) => page.locator(`[data-testid="import-checks"] [data-check="${id}"]`)

test.describe('Import: the source', () => {
  test('starts on Source with examples and nothing to review', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await expectStep(page, 'source', 'current')
    await expect(page.getByTestId('import-found')).toHaveCount(0)
    await expect(page.getByTestId('import-submit')).toBeDisabled()
    await expect(page.getByTestId('import-bar')).toContainText('Enter a source to continue.')
    await expect(page.getByTestId('import-examples').getByRole('button')).toHaveCount(5)
  })

  test('an example fills the field', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await page.getByTestId('import-examples').getByRole('button', { name: 'ollama://llama3.2:3b' }).click()
    await expect(source(page)).toHaveValue('ollama://llama3.2:3b')
    await expect(page.getByTestId('import-source-kind')).toHaveAttribute('data-kind', 'ollama')
    await expect(page.getByTestId('import-examples')).toHaveCount(0)
  })

  for (const [uri, kind, label, ref] of [
    ['huggingface://Qwen/Qwen3-8B', 'huggingface', 'Hugging Face repository', 'Qwen/Qwen3-8B'],
    ['https://huggingface.co/owner/repo', 'huggingface', 'Hugging Face repository', 'owner/repo'],
    ['owner/repo', 'huggingface', 'Hugging Face repository', 'owner/repo'],
    ['https://example.com/models/model.gguf', 'url', 'Direct URL', 'example.com/models/model.gguf'],
    ['https://example.com/config.yaml', 'config', 'Configuration file', 'example.com/config.yaml'],
    ['oci://registry.example.com/model:tag', 'oci', 'OCI image', 'registry.example.com/model:tag'],
    ['ollama://llama3.2:3b', 'ollama', 'Ollama model', 'llama3.2:3b'],
    ['file:///models/model.gguf', 'local', 'File on this host', '/models/model.gguf'],
    ['/models/config.yml', 'config', 'Configuration file', '/models/config.yml'],
  ]) {
    test(`reads ${uri} as ${label}`, async ({ page }) => {
      await boot(page)
      await page.goto('/app/import-model')
      await source(page).fill(uri)
      await expect(page.getByTestId('import-source-kind')).toHaveAttribute('data-kind', kind)
      await expect(page.getByTestId('import-source-kind')).toContainText(label)
      await expect(page.getByTestId('import-found')).toContainText(ref)
      await expect(page.getByTestId('import-source-kind')).toContainText('The source has not been contacted or validated as a model.')
    })
  }

  test('a source the importer does not know is a warning, not a block', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await source(page).fill('nonsense')
    await expect(page.getByTestId('import-source-kind')).toContainText('Not recognised')
    await expect(check(page, 'source')).toHaveAttribute('data-tone', 'warn')
    await expect(page.getByTestId('import-submit')).toBeEnabled()
  })
})

test.describe('Import: review', () => {
  test('moves to Review and shows the found card, the request preview and the checks', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await source(page).fill('huggingface://bartowski/Qwen2.5-7B-Instruct-GGUF')
    await expectStep(page, 'review', 'current')
    const found = page.getByTestId('import-found')
    await expect(found).toContainText('bartowski/Qwen2.5-7B-Instruct-GGUF')
    await expect(found).toContainText('Chosen when the import starts')
    await expect(page.getByTestId('import-preview-code')).toHaveText('uri: huggingface://bartowski/Qwen2.5-7B-Instruct-GGUF\npreferences: none (LocalAI decides)')
    await expect(page.getByTestId('import-preview')).toContainText('LocalAI returns no preview of the model configuration')
    await expect(check(page, 'source')).toContainText('Reads as a Hugging Face repository')
    await expect(check(page, 'backend')).toContainText('picks the backend')
    await expect(check(page, 'disk')).toContainText('118 GB free on the models disk')
    await expect(check(page, 'memory')).toContainText('9.8 GB of GPU memory is free now')
    await expect(page.getByTestId('import-bar')).toContainText('Imports bartowski/Qwen2.5-7B-Instruct-GGUF')
    // What the server cannot say before the import starts is not claimed.
    await expect(page.getByTestId('import-checks')).not.toContainText(/licen[cs]e/i)
    await expect(page.getByTestId('import-checks')).not.toContainText(/reachable/i)
  })

  test('the preview is the request the form sends', async ({ page }) => {
    await boot(page)
    let sent = null
    await page.route('**/models/import-uri', route => { sent = route.request().postDataJSON(); route.fulfill({ json: { uuid: 'j', ID: 'j' } }) })
    await page.route('**/models/jobs/**', route => route.fulfill({ json: { processed: false, message: 'x', progress: 1 } }))
    await page.goto('/app/import-model')
    await source(page).fill('hf://o/r')
    await page.getByTestId('import-adjust').click()
    await page.fill('#import-name', 'my-new-model')
    await page.fill('input[placeholder*="q4_k_m"]', 'q5_k_m')
    await expect(page.getByTestId('import-preview-code')).toHaveText('uri: hf://o/r\npreferences:\n  name: my-new-model\n  quantizations: q5_k_m')
    await page.getByTestId('import-submit').click()
    await expect.poll(() => sent).toEqual({ uri: 'hf://o/r', preferences: { name: 'my-new-model', quantizations: 'q5_k_m' } })
  })

  test('the name check reads the installed models', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await source(page).fill('hf://o/r')
    await page.getByTestId('import-adjust').click()
    await page.fill('#import-name', 'my-model')
    await expect(check(page, 'name')).toHaveAttribute('data-tone', 'warn')
    await expect(check(page, 'name')).toContainText('A model named my-model is already installed.')
    await page.fill('#import-name', 'fresh-name')
    await expect(check(page, 'name')).toHaveAttribute('data-tone', 'ok')
  })

  test('a chosen backend that is not installed says it is downloaded first', async ({ page }) => {
    await boot(page)
    await page.goto('/app/import-model')
    await source(page).fill('hf://o/r')
    await page.getByTestId('import-adjust').click()
    await page.locator('main button[aria-haspopup="listbox"]').first().click()
    await page.getByRole('option', { name: 'vllm', exact: true }).click()
    await expect(check(page, 'backend')).toContainText('vllm is not installed yet. LocalAI downloads it first.')
    await expect(page.getByTestId('import-found')).toContainText('chosen by you, downloaded first')
  })

  test('on a machine with no GPU the memory line names RAM; for a user who cannot read it, there is none', async ({ page }) => {
    await boot(page, { host: CPU_HOST })
    await page.goto('/app/import-model')
    await source(page).fill('hf://o/r')
    await expect(check(page, 'memory')).toContainText('20 GB of RAM is free now')
    const second = await page.context().newPage()
    await boot(second, { host: null })
    await second.goto('/app/import-model')
    await second.getByTestId('import-source-input').fill('hf://o/r')
    await expect(second.getByTestId('import-found')).toBeVisible()
    await expect(second.locator('[data-testid="import-checks"] [data-check="disk"]')).toHaveCount(0)
    await expect(second.locator('[data-testid="import-checks"] [data-check="memory"]')).toHaveCount(0)
  })

  test('a low disk warns', async ({ page }) => {
    await boot(page, { host: { ...GPU_HOST, disk: { total: 500 * GB, used: 498 * GB, available: 2 * GB } } })
    await page.goto('/app/import-model')
    await source(page).fill('hf://o/r')
    await expect(check(page, 'disk')).toHaveAttribute('data-tone', 'warn')
  })
})

test.describe('Import: ambiguous source', () => {
  test('asks which backend, says what each is, and imports with the pick', async ({ page }) => {
    await boot(page)
    const bodies = []
    await page.route('**/models/import-uri', route => {
      bodies.push(route.request().postDataJSON())
      if (bodies.length === 1) {
        return route.fulfill({ status: 400, json: { error: 'ambiguous import', detail: 'x', modality: 'text', candidates: ['transformers', 'vllm'], hint: '' } })
      }
      return route.fulfill({ json: { uuid: 'j', ID: 'j' } })
    })
    await page.route('**/models/jobs/**', route => route.fulfill({ json: { processed: false, message: 'x', progress: 5 } }))
    await page.goto('/app/import-model')
    await source(page).fill('huggingface://Qwen/Qwen3-8B')
    await page.getByTestId('import-submit').click()
    const alert = page.getByTestId('ambiguity-alert')
    await expect(alert).toContainText('More than one backend could run this')
    await expect(alert).toContainText('Runs the full-precision weights in Python.')
    await expect(alert).toContainText('Not installed. LocalAI downloads it first.')
    await expect(alert.getByTestId('ambiguity-chip-vllm')).toContainText('Use vllm')
    await alert.getByTestId('ambiguity-chip-transformers').click()
    await expect.poll(() => bodies.length).toBe(2)
    expect(bodies[1]).toEqual({ uri: 'huggingface://Qwen/Qwen3-8B', preferences: { backend: 'transformers' } })
    await expect(alert).toHaveCount(0)
  })
})

test.describe('Import: running and done', () => {
  test('shows the estimate against free memory and disk, then progress, while the review steps away', async ({ page }) => {
    await boot(page)
    await page.route('**/models/import-uri', route => route.fulfill({ json: { uuid: 'j', ID: 'j', estimated_size_display: '4.68 GB', estimated_size_bytes: 4.68 * GB, estimated_vram_display: '5.6 GB', estimated_vram_bytes: 5.6 * GB } }))
    await page.route('**/models/jobs/**', route => route.fulfill({ json: { processed: false, message: 'downloading', progress: 37.5, file_name: 'Qwen2.5-7B-Instruct-Q4_K_M.gguf', downloaded_size: '1.8 GB', file_size: '4.68 GB' } }))
    await page.goto('/app/import-model')
    await source(page).fill('huggingface://bartowski/Qwen2.5-7B-Instruct-GGUF')
    await page.getByTestId('import-submit').click()
    await expectStep(page, 'import', 'current')
    await expect(page.getByTestId('import-estimate')).toContainText('4.68 GB')
    await expect(page.getByTestId('import-fit')).toHaveAttribute('data-fit', 'fits')
    await expect(page.getByTestId('import-fit')).toContainText('Fits in the 9.8 GB that is free.')
    await expect(page.getByTestId('import-progress').getByRole('progressbar')).toHaveAttribute('aria-valuenow', '38')
    await expect(page.getByTestId('import-found')).toHaveCount(0)
    await expect(page.getByTestId('import-bar')).toContainText('Importing.')
  })

  test('an estimate over the free memory says so', async ({ page }) => {
    await boot(page)
    await page.route('**/models/import-uri', route => route.fulfill({ json: { uuid: 'j', ID: 'j', estimated_size_display: '40 GB', estimated_size_bytes: 40 * GB, estimated_vram_display: '48 GB', estimated_vram_bytes: 48 * GB } }))
    await page.route('**/models/jobs/**', route => route.fulfill({ json: { processed: false, message: 'x', progress: 1 } }))
    await page.goto('/app/import-model')
    await source(page).fill('hf://big/model')
    await page.getByTestId('import-submit').click()
    await expect(page.getByTestId('import-fit')).toHaveAttribute('data-fit', 'over')
    await expect(page.getByTestId('import-fit')).toContainText('More than the 9.8 GB that is free.')
  })

  test('done: the model is named, with links to Chat and Models, and Import another starts over', async ({ page }) => {
    await boot(page)
    await page.route('**/models/import-uri', route => route.fulfill({ json: { uuid: 'j', ID: 'j' } }))
    await page.route('**/models/jobs/**', route => route.fulfill({ json: { completed: true, message: 'done', gallery_element_name: 'qwen2.5-7b-instruct' } }))
    await page.goto('/app/import-model')
    await source(page).fill('huggingface://bartowski/Qwen2.5-7B-Instruct-GGUF')
    await page.getByTestId('import-submit').click()
    const done = page.getByTestId('import-done')
    await expect(done).toContainText('qwen2.5-7b-instruct is imported')
    await expectStep(page, 'done', 'current')
    await expect(done.getByRole('link', { name: 'Chat with qwen2.5-7b-instruct' })).toHaveAttribute('href', '/app/chat/qwen2.5-7b-instruct')
    await expect(done.getByTestId('import-open-models')).toHaveAttribute('href', '/app/models?view=installed')
    await done.getByRole('button', { name: 'Import another' }).click()
    await expect(source(page)).toHaveValue('')
    await expect(page.getByTestId('import-done')).toHaveCount(0)
  })

  test('a failed import keeps the review and shows the message', async ({ page }) => {
    await boot(page)
    await page.route('**/models/import-uri', route => route.fulfill({ status: 500, json: { error: 'failed to discover model config: no such file' } }))
    await page.goto('/app/import-model')
    await source(page).fill('/models/missing.gguf')
    await page.getByTestId('import-submit').click()
    await expect(page.getByText('Failed to start import: failed to discover model config: no such file')).toBeVisible()
    await expect(page.getByTestId('import-found')).toBeVisible()
    await expect(page.getByTestId('import-submit')).toBeEnabled()
  })
})

test.describe('Import: Write YAML', () => {
  test('swaps the surface, keeps the steps away, and creates from the editor', async ({ page }) => {
    await boot(page)
    let body = null
    await page.route('**/models/import', route => { body = route.request().postData(); route.fulfill({ json: { message: 'ok' } }) })
    await page.goto('/app/import-model')
    await page.getByTestId('import-tab-yaml').click()
    await expect(page.getByTestId('import-yaml')).toContainText('Written to the models folder as it is')
    await expect(page.getByTestId('tool-steps')).toHaveCount(0)
    await expect(page.getByTestId('import-source-input')).toHaveCount(0)
    await page.getByTestId('import-create').click()
    await expect.poll(() => body).toContain('name: my-model')
    await expect(page).toHaveURL(/\/app\/models\?view=installed/)
  })
})

test.describe('Import: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  const noOverflow = (page) => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)

  test('empty, review and ambiguous states fit the screen', async ({ page }) => {
    await boot(page)
    await page.route('**/models/import-uri', route => route.fulfill({ status: 400, json: { error: 'ambiguous import', detail: 'x', modality: 'text', candidates: ['transformers', 'vllm'], hint: '' } }))
    await page.goto('/app/import-model')
    await expect(page.getByTestId('import-examples')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
    await source(page).fill('huggingface://bartowski/a-very-long-repository-name-GGUF')
    await expect(page.getByTestId('import-found')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
    await page.getByTestId('import-submit').click()
    await expect(page.getByTestId('ambiguity-alert')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
    await expect(page.getByTestId('import-submit')).toBeInViewport()
  })
})
