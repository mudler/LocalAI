import { test, expect } from './coverage-fixtures.js'
import { CONFIGS, MODEL, mockRuntime } from './engine-runtime-fixtures.js'

// The Concurrency choice of the quick setup: Default, 8, 16 and 32+ streams,
// with values that depend on the memory kind of the host (unified, discrete,
// no GPU). /api/resources says what the host is; the person can override it.
// The estimate route is a stand-in: weights plus a KV cache sized from the
// context and the cache types, so a spec can make f16 overflow and q8_0 fit.

const GIB = 1024 ** 3
const MIB = 1024 ** 2
const BITS = { f16: 16, q8_0: 8.5 }
const field = (page, id) => page.getByTestId(`rt-${id}`)
const tier = (page, id) => page.getByTestId(`rt-conc-${id}`)
const save = page => page.getByTestId('editor-save')
const lastPatch = state => state.patches[state.patches.length - 1]?.patch

const HOSTS = {
  discrete: { kind: 'discrete', device: 'NVIDIA GeForce RTX 4090', total: 24 * GIB },
  smallDiscrete: { kind: 'discrete', device: 'NVIDIA GeForce RTX 4080', total: 16 * GIB },
  tinyDiscrete: { kind: 'discrete', device: 'NVIDIA GeForce GTX 1060', total: 6 * GIB },
  unified: { kind: 'unified', device: 'NVIDIA GB10', total: 128 * GIB },
  system: { kind: 'system', total: 64 * GIB },
}

// weights: the bytes beside the KV cache. kvPerToken: f16 bytes of a token.
function estimator({ weights = 4 * GIB, kvPerToken = 128 * 1024 } = {}) {
  return (body) => {
    const ctx = body.context_size || 8192
    const kv = ctx * kvPerToken * ((BITS[body.cache_type_k] || 16) + (BITS[body.cache_type_v] || 16)) / 32
    return {
      size_bytes: weights, vram_bytes: weights + kv, vram_display: '',
      runtime: { context_length: ctx, kv_cache_bytes: kv, compute_bytes: 256 * MIB },
    }
  }
}

async function open(page, { host = HOSTS.discrete, name = MODEL, configs = CONFIGS, estimate = estimator() } = {}) {
  const state = await mockRuntime(page, { configs })
  await page.route('**/api/resources', route => route.fulfill({ json: { type: 'gpu', available: true, gpus: [], aggregate: {}, memory: host } }))
  await page.route('**/api/models/vram-estimate', route => route.fulfill({ json: estimate(route.request().postDataJSON()) }))
  await page.goto(`/app/model-editor/${name}`)
  await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('rt-conc-set')).toBeVisible({ timeout: 10_000 })
  return state
}

test.describe('Runtime tuning: concurrency', () => {
  test('a discrete GPU: the detected type, Default first, and 8 streams with paged serving', async ({ page }) => {
    const state = await open(page)
    await expect(page.getByTestId('rt-memory-line')).toHaveText('Discrete GPU on this server: NVIDIA GeForce RTX 4090, 24 GB')
    await expect(page.getByTestId('rt-memory-kind')).toHaveValue('')
    await expect(tier(page, 'concurrencyDefault')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-conc-best-streams8')).toHaveText('Best for: A team chatting with one model.')
    // The slots share the context: 8192 tokens over 8 streams.
    await expect(page.getByTestId('rt-conc-share-streams8')).toContainText('1,024 context tokens per stream')
    // About the same memory as now: no figure.
    await expect(page.getByTestId('rt-conc-mem-streams8')).toHaveCount(0)
    await expect(page.getByTestId('rt-conc-shared')).toContainText('share one KV cache')
    await expect(page.getByTestId('rt-conc-mem-streams32')).toContainText('less memory')

    await tier(page, 'streams8').click()
    await expect(tier(page, 'streams8')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-applied')).toContainText('8 streams')
    await expect(field(page, 'options-parallel')).toHaveValue('8')
    await expect(field(page, 'options-paged_kv-true')).toHaveAttribute('aria-checked', 'true')
    await expect(field(page, 'options-paged_preempt-true')).toHaveAttribute('aria-checked', 'true')
    // The batch choice is a separate one and stays on Balanced.
    await expect(page.getByTestId('rt-quick-balanced')).toHaveAttribute('aria-checked', 'true')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    // A discrete GPU keeps the prompt cache at its default size.
    expect(lastPatch(state).options).toEqual(expect.arrayContaining(['keep_me:true', 'parallel:8', 'paged_kv:true', 'paged_preempt:true']))
    expect(lastPatch(state).options).toHaveLength(4)
  })

  test('16 streams turns GPU sampling on and drafting off, and 32+ uses an 8-bit cache on a discrete GPU', async ({ page }) => {
    const state = await open(page, {
      configs: { ...CONFIGS, 'mtp-lab': 'name: mtp-lab\nbackend: llama-cpp\nparameters:\n  model: m.gguf\ncontext_size: 32768\noptions:\n  - spec_type:draft-mtp\n' },
      name: 'mtp-lab',
    })
    await tier(page, 'streams16').click()
    await expect(page.getByTestId('rt-backend-sampling')).toHaveValue('on')
    await expect(page.getByTestId('rt-draft-mode-off')).toHaveAttribute('aria-checked', 'true')
    await expect(field(page, 'options-parallel')).toHaveValue('16')

    await tier(page, 'streams32').click()
    await expect(field(page, 'cache_type_k')).toHaveValue('q8_0')
    await expect(field(page, 'cache_type_v')).toHaveValue('q8_0')
    await expect(page.getByTestId('rt-backend-sampling')).toHaveValue('on')
    await expect(page.getByTestId('rt-conc-kind-streams32')).toContainText('8-bit KV cache')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state)).toMatchObject({ cache_type_k: 'q8_0', cache_type_v: 'q8_0', flash_attention: 'on' })
    expect(lastPatch(state).options).toEqual(expect.arrayContaining(['spec_type:none', 'parallel:32', 'backend_sampling:on']))

    // Back to Default removes what the tiers wrote.
    await tier(page, 'concurrencyDefault').click()
    await expect(field(page, 'cache_type_k')).toHaveValue('')
    await expect(field(page, 'options-parallel')).toHaveValue('')
    await expect(page.getByTestId('rt-backend-sampling')).toHaveValue('off')
  })

  test('a 6 GiB card is too small for the stream setups, a 16 GiB one is not', async ({ page }) => {
    await open(page, { host: HOSTS.tinyDiscrete })
    await expect(tier(page, 'streams32')).toBeDisabled()
    await expect(page.getByTestId('rt-conc-mem-streams32')).toHaveText('Needs 8 GB of memory')
    await open(page, { host: HOSTS.smallDiscrete })
    await expect(tier(page, 'streams32')).toBeEnabled()
    await expect(tier(page, 'streams16')).toBeEnabled()
  })

  test('unified memory: f16 at 32 streams and the host caches bounded', async ({ page }) => {
    const state = await open(page, { host: HOSTS.unified })
    await expect(page.getByTestId('rt-memory-line')).toHaveText('Unified memory on this server: NVIDIA GB10, 128 GB')
    await tier(page, 'streams32').click()
    await expect(field(page, 'cache_type_k')).toHaveValue('')
    await expect(page.getByTestId('rt-conc-kind-streams32')).toHaveCount(0)
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).options).toEqual(expect.arrayContaining(['parallel:32', 'cache_ram:4096', 'backend_sampling:on']))
  })

  test('unified memory that f16 would overflow gets the 8-bit cache', async ({ page }) => {
    // 100 GiB of weights and an f16 KV cache of 16 GiB at 32768 tokens pass
    // 90% of 128 GiB; with the q8_0 cache (8.5 GiB) they fit.
    const configs = { ...CONFIGS, big: 'name: big\nbackend: llama-cpp\nparameters:\n  model: big.gguf\ncontext_size: 32768\n' }
    await open(page, { host: HOSTS.unified, name: 'big', configs, estimate: estimator({ weights: 100 * GIB, kvPerToken: GIB / 2048 }) })
    await expect(page.getByTestId('rt-conc-kind-streams32')).toContainText('does not fit, so this uses an 8-bit one')
    await tier(page, 'streams32').click()
    await expect(field(page, 'cache_type_k')).toHaveValue('q8_0')
    await expect(tier(page, 'streams32')).toHaveAttribute('aria-checked', 'true')
  })

  test('no GPU: no 32 streams and no GPU sampling', async ({ page }) => {
    await open(page, { host: HOSTS.system })
    await expect(page.getByTestId('rt-memory-line')).toHaveText('No GPU on this server (64 GB)')
    await expect(tier(page, 'streams32')).toBeDisabled()
    await expect(page.getByTestId('rt-conc-mem-streams32')).toHaveText('Not offered here')
    await tier(page, 'streams16').click()
    await expect(page.getByTestId('rt-backend-sampling')).toHaveValue('off')
  })

  test('the person overrides the detected type, and the choice is kept', async ({ page }) => {
    await open(page, { host: HOSTS.smallDiscrete })
    await page.getByTestId('rt-memory-kind').selectOption('unified')
    await expect(page.getByTestId('rt-memory-line')).toHaveText('Unified memory, chosen by you')
    // The detected size describes the discrete card, so no size check applies.
    await expect(tier(page, 'streams32')).toBeEnabled()
    await page.reload()
    await expect(page.getByTestId('rt-memory-kind')).toHaveValue('unified', { timeout: 15_000 })
    await page.getByTestId('rt-memory-kind').selectOption('')
    await expect(page.getByTestId('rt-memory-line')).toContainText('Discrete GPU on this server')
  })

  test('unified memory advises the default load mode over mmap', async ({ page }) => {
    const configs = { ...CONFIGS, mapped: 'name: mapped\nbackend: llama-cpp\nparameters:\n  model: m.gguf\nmmap: true\n' }
    const state = await open(page, { host: HOSTS.unified, name: 'mapped', configs })
    await expect(page.getByTestId('rt-advice-unifiedLoadMode')).toContainText('held twice')
    await page.getByTestId('rt-advice-unifiedLoadMode-fix').click()
    await expect(page.getByTestId('rt-advice-unifiedLoadMode')).toHaveCount(0)
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).mmap ?? null).toBeNull()
  })

  test('a discrete GPU gives no load mode advice', async ({ page }) => {
    const configs = { ...CONFIGS, mapped: 'name: mapped\nbackend: llama-cpp\nparameters:\n  model: m.gguf\nmmap: true\n' }
    await open(page, { name: 'mapped', configs })
    await expect(page.getByTestId('rt-conc-set')).toBeVisible()
    await expect(page.getByTestId('rt-advice-unifiedLoadMode')).toHaveCount(0)
  })
})
