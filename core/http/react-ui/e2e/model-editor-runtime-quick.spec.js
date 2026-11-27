import { test, expect } from './coverage-fixtures.js'
import { CONFIGS, MODEL, mockRuntime } from './engine-runtime-fixtures.js'

// The Quick setup of Runtime tuning: the exclusive llama.cpp batch profiles
// (Balanced, Long prompts) and Custom, read back from
// the file, applied like a preset, and the memory each one costs from the
// VRAM estimate. The estimate route is a stand-in that sizes the KV cache
// from the context and cache types and the compute buffers from the batch,
// so the specs see the directions the real estimator gives. The host is a
// discrete 24 GiB GPU unless a spec says otherwise; the concurrency choice
// has its own spec (model-editor-runtime-concurrency.spec.js).

const field = (page, id) => page.getByTestId(`rt-${id}`)
const save = page => page.getByTestId('editor-save')
const choice = (page, id) => page.getByTestId(`rt-quick-${id}`)
const lastPatch = state => state.patches[state.patches.length - 1]?.patch

const MIB = 1024 * 1024
const BITS = { f16: 16, q8_0: 8.5, q4_0: 4.5 }

// A runtime answer for an estimate body.
function runtimeOf(body) {
  const ctx = body.context_size || 8192
  const batch = body.batch || 512
  const ubatch = Math.min(body.ubatch || batch, batch)
  const kv = ctx * 128 * 1024 * ((BITS[body.cache_type_k] || 16) + (BITS[body.cache_type_v] || 16)) / 32
  return {
    size_bytes: 4 * 1024 * MIB, vram_bytes: 6 * 1024 * MIB, vram_display: '6 GB',
    runtime: {
      context_length: ctx, cache_type_k: body.cache_type_k, cache_type_v: body.cache_type_v, ubatch,
      kv_cache_bytes: kv, compute_bytes: ubatch * MIB / 2,
    },
  }
}

const DISCRETE = { kind: 'discrete', device: 'NVIDIA GeForce RTX 4090', total: 24 * 1024 * MIB * 1024 }

async function open(page, { name = MODEL, configs = CONFIGS, estimate = 'ok', memory = DISCRETE } = {}) {
  const state = await mockRuntime(page, { configs })
  await page.route('**/api/resources', route => route.fulfill({ json: { type: 'gpu', available: true, gpus: [], aggregate: {}, memory } }))
  state.estimates = []
  // Registered after mockRuntime, so it answers first.
  await page.route('**/api/models/vram-estimate', route => {
    const body = route.request().postDataJSON()
    state.estimates.push(body)
    if (estimate === 'none') return route.fulfill({ json: { message: 'no weight files found for estimation' } })
    if (estimate === 'error') return route.fulfill({ status: 500, json: { error: 'estimator down' } })
    return route.fulfill({ json: runtimeOf(body) })
  })
  await page.goto(`/app/model-editor/${name}`)
  await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('rt-quick')).toBeVisible({ timeout: 10_000 })
  return state
}

test.describe('Runtime tuning: quick setup', () => {
  test('a model with no profile setting is on Balanced, and each profile shows its memory change', async ({ page }) => {
    const state = await open(page)
    await expect(choice(page, 'balanced')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-quick-row-balanced')).toContainText('Balanced')
    // The checked option is the current one: it carries no memory figure.
    await expect(page.getByTestId('rt-quick-mem-balanced')).toHaveCount(0)
    await expect(page.getByTestId('rt-quick-mem-longPrompts')).toContainText('more memory')
    await expect(page.getByTestId('rt-quick-line')).toContainText('LocalAI picks for your GPU')
    await expect(page.getByTestId('rt-conc-mem-streams32')).toContainText('less memory')
    await expect(page.getByTestId('rt-quick-mem-custom')).toHaveCount(0)
    await expect(page.getByTestId('rt-quick-now')).toContainText('KV cache')
    await expect(page.getByTestId('rt-quick-now')).toContainText('Estimate')
    // The current settings ask for the defaults; each profile for its own values.
    await expect.poll(() => state.estimates.length).toBeGreaterThanOrEqual(4)
    expect(state.estimates).toContainEqual(expect.objectContaining({ model: MODEL, context_size: 8192, cache_type_k: 'f16', batch: 0, ubatch: 0 }))
    // Long prompts on a discrete GPU: a micro-batch of 1024.
    expect(state.estimates).toContainEqual(expect.objectContaining({ batch: 2048, ubatch: 1024 }))
    expect(state.estimates).toContainEqual(expect.objectContaining({ cache_type_k: 'q8_0', cache_type_v: 'q8_0' }))
    // Nothing is applied until a profile is picked.
    await expect(save(page)).toBeDisabled()
  })

  test('Long prompts writes the batch sizes for the memory kind and keeps the other options', async ({ page }) => {
    const state = await open(page)
    await choice(page, 'longPrompts').click()
    await expect(choice(page, 'longPrompts')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-applied')).toContainText('Long prompts')
    await expect(field(page, 'parameters-batch')).toHaveValue('2048')
    await expect(field(page, 'options-ubatch')).toHaveValue('1024')
    await expect(page.getByTestId('rt-quick-kind-longPrompts')).toContainText('compute buffer must fit one card')
    await expect(page.getByTestId('rt-quick-mem-longPrompts')).toHaveCount(0)
    await expect(page.getByTestId('rt-quick-mem-balanced')).toContainText('less memory')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state)).toMatchObject({ parameters: { batch: 2048 } })
    expect(lastPatch(state).options).toEqual(['keep_me:true', 'ubatch:1024'])
  })

  test('a file saved with a profile opens on it, and an edit off the profile is Custom', async ({ page }) => {
    const configs = {
      ...CONFIGS,
      'long-lab': 'name: long-lab\nbackend: llama-cpp\nparameters:\n  model: long.gguf\n  batch: 2048\noptions:\n  - ubatch:2048\n  - max_prefill_tokens:512\n',
    }
    // A micro-batch of 2048 is Long prompts on unified memory.
    await open(page, { name: 'long-lab', configs, memory: { kind: 'unified', device: 'NVIDIA GB10', total: 128 * 1024 * MIB * 1024 } })
    await expect(choice(page, 'longPrompts')).toHaveAttribute('aria-checked', 'true')
    // max_prefill_tokens is an alias the backend also reads; the field shows it.
    await expect(field(page, 'options-prefill_budget')).toHaveValue('512')
    // paged_kv is not a batch setting: turning it off keeps the profile.
    await field(page, 'options-paged_kv-false').click()
    await expect(choice(page, 'longPrompts')).toHaveAttribute('aria-checked', 'true')
    await field(page, 'options-ubatch').fill('1024')
    await expect(choice(page, 'custom')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-quick-row-custom')).toHaveClass(/rt-quick__row--current/)
  })

  test('GPU sampling is Off, Auto or On in the speed group, Off writing no entry', async ({ page }) => {
    const state = await open(page)
    const gpu = page.getByTestId('rt-group-speed').getByTestId('rt-backend-sampling-field')
    await expect(gpu).toContainText('GPU sampling')
    const select = page.getByTestId('rt-backend-sampling')
    await expect(select).toHaveValue('off')
    await expect(select.locator('option')).toHaveText(['Off', 'Auto', 'On'])
    await select.selectOption('auto')
    await expect(page.getByTestId('rt-backend-sampling-choice-hint')).toContainText('Use GPU sampling when the model runs on a GPU')
    await select.selectOption('on')
    await expect(page.getByTestId('rt-backend-sampling-choice-hint')).toContainText('Sample tokens on the GPU. Faster with many parallel requests.')
    // Not part of any profile.
    await expect(choice(page, 'balanced')).toHaveAttribute('aria-checked', 'true')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).options).toEqual(['keep_me:true', 'backend_sampling:on'])
    await select.selectOption('off')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(2)
    expect(lastPatch(state).options).toEqual(['keep_me:true'])
  })

  test('a file with backend_sampling:auto opens on Auto', async ({ page }) => {
    const configs = { ...CONFIGS, 'auto-lab': 'name: auto-lab\nbackend: llama-cpp\nparameters:\n  model: a.gguf\noptions:\n  - backend_sampling:auto\n' }
    await open(page, { name: 'auto-lab', configs })
    await expect(page.getByTestId('rt-backend-sampling')).toHaveValue('auto')
  })

  test('a model on another engine has no GPU sampling switch', async ({ page }) => {
    await mockRuntime(page)
    await page.goto('/app/model-editor/toy-model')
    await expect(page.getByTestId('runtime-engine')).toContainText('Toy Engine', { timeout: 15_000 })
    await expect(page.getByTestId('rt-backend-sampling-field')).toHaveCount(0)
    await expect(page.getByTestId('rt-quick')).toHaveCount(0)
  })

  test('Balanced keeps a flash attention the person set, and Default drops the one 32+ streams set', async ({ page }) => {
    await open(page)
    await field(page, 'flash_attention-off').click()
    await expect(choice(page, 'balanced')).toHaveAttribute('aria-checked', 'true')
    await choice(page, 'longPrompts').click()
    await choice(page, 'balanced').click()
    await expect(field(page, 'flash_attention-off')).toHaveAttribute('aria-checked', 'true')

    await field(page, 'flash_attention-').click()
    await page.getByTestId('rt-conc-streams32').click()
    await expect(field(page, 'flash_attention-on')).toHaveAttribute('aria-checked', 'true')
    await page.getByTestId('rt-conc-concurrencyDefault').click()
    await expect(field(page, 'flash_attention-')).toHaveAttribute('aria-checked', 'true')
    await expect(save(page)).toBeDisabled()
  })

  test('the advanced group starts closed and holds every other backend option', async ({ page }) => {
    const configs = {
      ...CONFIGS,
      'alias-lab': 'name: alias-lab\nbackend: llama-cpp\nparameters:\n  model: a.gguf\noptions:\n  - unified_kv:false\n  - paged_serving:true\n  - max_prefill_tokens:auto\n  - draft_max:8\n',
    }
    const state = await open(page, { name: 'alias-lab', configs })
    const advanced = page.getByTestId('rt-group-engine_advanced')
    await expect(advanced).not.toHaveAttribute('open', '')
    // Speculative decoding is the Drafting section: a draft window with no
    // kind and no draft model drafts nothing, so it reads as Off.
    await expect(page.getByTestId('rt-draft-mode-off')).toHaveAttribute('aria-checked', 'true')
    await expect(field(page, 'options-kv_unified-field')).toBeHidden()
    await expect(page.getByTestId('rt-group-engine_advanced-toggle')).toContainText('Advanced llama.cpp options')
    await page.getByTestId('rt-group-engine_advanced-toggle').click()
    // An alias in the file shows in the field of its canonical option.
    await expect(field(page, 'options-kv_unified-false')).toHaveAttribute('aria-checked', 'true')
    await expect(field(page, 'options-context_shift-field')).toBeVisible()
    await expect(field(page, 'options-backend_sampling_min_slots')).toHaveAttribute('placeholder', 'Default (16)')
    // Paged serving and its new fields.
    await expect(field(page, 'options-paged_kv-true')).toHaveAttribute('aria-checked', 'true')
    await expect(field(page, 'options-prefill_budget')).toHaveValue('auto')
    await expect(field(page, 'options-kv_output_reserve')).toHaveAttribute('placeholder', 'Default (256)')
    await field(page, 'options-kv_pool_tokens').fill('65536')
    await field(page, 'options-kv_unified-true').click()
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    // An edited alias is written with its canonical name in place; the others stay as written.
    expect(lastPatch(state).options).toEqual(['kv_unified:true', 'paged_serving:true', 'max_prefill_tokens:auto', 'draft_max:8', 'kv_pool_tokens:65536'])
  })

  test('Custom keeps the values as they are', async ({ page }) => {
    await open(page)
    await choice(page, 'custom').click()
    await expect(choice(page, 'custom')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-applied')).toHaveCount(0)
    await expect(save(page)).toBeDisabled()
  })

  test('an estimate with no runtime part shows no memory figure', async ({ page }) => {
    await open(page, { estimate: 'none' })
    await expect(page.getByTestId('rt-quick-mem-longPrompts')).toHaveCount(0)
    await expect(page.getByTestId('rt-quick-now')).toHaveCount(0)
  })

  test('a failed estimate says so and the profiles still apply', async ({ page }) => {
    await open(page, { estimate: 'error' })
    await expect(page.getByTestId('rt-quick-error')).toBeVisible()
    await choice(page, 'longPrompts').click()
    await expect(field(page, 'parameters-batch')).toHaveValue('2048')
  })
})
