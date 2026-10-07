import { test, expect } from './coverage-fixtures.js'
import { mockLedger, mockPlacement, GALLERY, GB, PROFILES, vramFor } from './ledger-fixtures.js'

// The Placement section: where a model runs. On the model page's Configuration
// tab it saves on its own; in the model editor it feeds the editor's own Save.
// Both write gpu_layers, tensor_split, main_gpu and context_size and nothing
// else, and every figure comes from the device list and the estimate.

const page_ = (id, tab = 'config') => `/app/models/${id}?tab=${tab}`
const mode = (page, id) => page.getByTestId(`placement-mode-${id}`)
const file = (page) => page.getByTestId('placement-file')
const verdict = (page) => page.getByTestId('placement-verdict')
const save = (page) => page.getByTestId('config-save')

async function setup(page, { url = page_('qwen3-14b-instruct'), ledger = {}, placement = {} } = {}) {
  const state = await mockLedger(page, ledger)
  const handle = await mockPlacement(page, placement)
  await page.goto(url)
  await expect(page.getByTestId('placement')).toBeVisible({ timeout: 15_000 })
  return Object.assign(handle, { ledger: state })
}

const entry = (name) => GALLERY.find(e => e.name === name)

// The laptop's GPU with plenty of system memory behind it, for the spill cases
// that are about the GPU and not about running out of memory.
const laptopBigRam = { ...PROFILES.laptop, ram: { total: 64 * GB, used: 8 * GB, free: 56 * GB, available: 56 * GB, usage_percent: 12 } }

test.describe('Placement - modes', () => {
  test('Auto is the default for a model that sets nothing, writes nothing and says what the engine does', async ({ page }) => {
    const { patches } = await setup(page)
    await expect(mode(page, 'auto')).toHaveAttribute('aria-checked', 'true')
    await expect(mode(page, 'auto')).toContainText('writes nothing')
    await expect(mode(page, 'cpu')).toContainText('gpu_layers: 0')
    await expect(mode(page, 'custom')).toContainText('gpu_layers: N')
    const hint = page.getByTestId('placement-auto-hint')
    await expect(hint).toContainText('every layer')
    await expect(hint).toContainText('llama.cpp')
    await expect(file(page)).not.toContainText('gpu_layers')
    await expect(save(page)).toBeDisabled()
    await expect(save(page)).toContainText('Saved')
    expect(patches).toEqual([])
  })

  test('CPU only writes gpu_layers: 0, says the model runs on the CPU and saves exactly that', async ({ page }) => {
    const { patches } = await setup(page)
    await mode(page, 'cpu').click()
    await expect(mode(page, 'cpu')).toHaveAttribute('aria-checked', 'true')
    await expect(file(page).locator('[data-written="true"]', { hasText: 'gpu_layers: 0' })).toBeVisible()
    await expect(verdict(page)).toHaveAttribute('data-state', 'cpu')
    await expect(verdict(page)).toContainText('Runs on CPU only')
    await expect(verdict(page)).toContainText('Slower than on a GPU')
    await expect(save(page)).toBeEnabled()
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { gpu_layers: 0 } }])
  })

  test('CPU only has no GPU bars, only system memory', async ({ page }) => {
    await setup(page)
    await mode(page, 'cpu').click()
    await expect(page.getByTestId('placement-bar-ram')).toBeVisible()
    await expect(page.getByTestId('placement-bar-gpu-0')).toHaveCount(0)
  })

  test('Custom starts at all layers and the field takes a number that goes into the file', async ({ page }) => {
    const { patches } = await setup(page)
    await mode(page, 'custom').click()
    await expect(page.getByTestId('placement-layers')).toHaveValue('All')
    await expect(file(page)).toContainText('gpu_layers: 99999999')
    await page.getByTestId('placement-layers').fill('12')
    await expect(file(page).locator('[data-written="true"]', { hasText: 'gpu_layers: 12' })).toBeVisible()
    await expect(mode(page, 'custom')).toContainText('gpu_layers: 12')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { gpu_layers: 12 } }])
  })

  test('All layers writes the value LocalAI already uses by default', async ({ page }) => {
    const { patches } = await setup(page)
    await mode(page, 'custom').click()
    await page.getByTestId('placement-layers').fill('7')
    await expect(file(page)).toContainText('gpu_layers: 7')
    await page.getByTestId('placement-all-layers').click()
    await expect(file(page)).toContainText('gpu_layers: 99999999')
    await expect(page.getByTestId('placement-layers')).toHaveValue('All')
    await expect(page.getByTestId('placement-all-layers')).toHaveAttribute('aria-pressed', 'true')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { gpu_layers: 99999999 } }])
  })

  test('typing the word all, in any case, means all layers', async ({ page }) => {
    await setup(page, { url: page_('llama-3.3-70b-instruct-iq2') })
    await expect(page.getByTestId('placement-layers')).toHaveValue('20')
    await page.getByTestId('placement-layers').fill('ALL')
    await expect(file(page)).toContainText('gpu_layers: 99999999')
  })

  test('a model already set to all layers opens in Custom with All selected', async ({ page }) => {
    await setup(page, { url: page_('qwen3-8b-instruct') })
    await expect(mode(page, 'custom')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('placement-layers')).toHaveValue('All')
  })

  test('going back to Auto removes the key, and the save sends null for it', async ({ page }) => {
    const { patches } = await setup(page, { url: page_('qwen3-8b-instruct') })
    await mode(page, 'auto').click()
    await expect(file(page)).not.toContainText('gpu_layers')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-8b-instruct', patch: { gpu_layers: null } }])
  })

  test('a model set to zero opens as CPU only', async ({ page }) => {
    await setup(page, { url: page_('gemma-3-12b-it') })
    await expect(mode(page, 'cpu')).toHaveAttribute('aria-checked', 'true')
  })

  test('with a layer count from the estimate there is a slider, and it writes the count', async ({ page }) => {
    const { patches } = await setup(page, { placement: { layerCount: 40 } })
    await mode(page, 'custom').click()
    const slider = page.getByTestId('placement-slider')
    await expect(slider).toBeVisible()
    await expect(slider).toHaveAttribute('max', '40')
    await expect(page.getByTestId('placement-no-slider')).toHaveCount(0)
    await slider.fill('25')
    await expect(page.getByTestId('placement-layers')).toHaveValue('25')
    await expect(file(page)).toContainText('gpu_layers: 25')
    await expect(page.getByTestId('placement-custom')).toContainText('25 of 40 layers on the GPU')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { gpu_layers: 25 } }])
  })

  test('without a layer count there is no slider and the page says why, never a made-up range', async ({ page }) => {
    await setup(page)
    await mode(page, 'custom').click()
    await expect(page.getByTestId('placement-slider')).toHaveCount(0)
    await expect(page.getByTestId('placement-no-slider')).toContainText('not reported')
    await expect(page.getByTestId('placement-no-slider')).toContainText('99999999')
  })
})

test.describe('Placement - context size', () => {
  test('the presets write context_size and the estimate follows', async ({ page }) => {
    const { patches, estimateCalls } = await setup(page)
    await expect(page.getByTestId('placement-context-8192')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('placement-context-32768').click()
    await expect(page.getByTestId('placement-context-32768')).toHaveAttribute('aria-pressed', 'true')
    await expect(file(page).locator('[data-written="true"]', { hasText: 'context_size: 32768' })).toBeVisible()
    await expect.poll(() => estimateCalls.some(call => call.context_size === 32768)).toBe(true)
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { context_size: 32768 } }])
  })

  test('the number field takes any size', async ({ page }) => {
    await setup(page)
    await page.getByTestId('placement-context-input').fill('20000')
    await expect(file(page)).toContainText('context_size: 20000')
    await expect(page.locator('[data-testid^="placement-context-"][aria-pressed="true"]')).toHaveCount(0)
  })

  test('a bigger context grows the KV segment of the bar, from the estimate and nothing else', async ({ page }) => {
    await setup(page)
    const kv = (p) => p.getByTestId('placement-bar-gpu-0').locator('[data-segment="kv"]')
    await expect(kv(page)).toBeVisible()
    const width = async () => (await kv(page).boundingBox()).width
    const small = await width()
    await page.getByTestId('placement-context-65536').click()
    await expect.poll(width).toBeGreaterThan(small * 2)
  })

  test('a model that sets no context size says which one the estimate used', async ({ page }) => {
    await setup(page, { placement: { configs: { 'qwen3-14b-instruct': 'name: qwen3-14b-instruct\nbackend: llama-cpp\nparameters:\n  model: a.gguf\n' } } })
    await expect(page.getByTestId('placement')).toContainText('Not set. The estimate uses 8K.')
  })
})

test.describe('Placement - the bars and the verdict', () => {
  test('one GPU: a bar with other apps, the model and what is free after, and a verdict that fits', async ({ page }) => {
    await setup(page)
    const bar = page.getByTestId('placement-bar-gpu-0')
    await expect(bar).toContainText('RTX 4090')
    await expect(bar).toContainText('free after')
    await expect(bar.locator('[data-segment="other"]')).toBeVisible()
    await expect(bar.locator('[data-segment="model"]')).toBeVisible()
    await expect(verdict(page)).toHaveAttribute('data-state', 'fits')
    await expect(verdict(page)).toContainText('Fits in GPU')
    await expect(page.getByTestId('placement-bar-ram')).toHaveCount(0)
  })

  test('the bar says how much is free after, from the estimate and the device', async ({ page }) => {
    await setup(page)
    const need = vramFor(entry('qwen3-14b-instruct'), 8192, null)
    const free = 21.4 * GB - need
    await expect(page.getByTestId('placement-bar-gpu-0')).toContainText(`${(free / GB).toFixed(1)} GB free after`)
  })

  test('a spill: some layers on the GPU and the rest in system memory, in words, without a made-up speed', async ({ page }) => {
    await setup(page, { ledger: { resources: laptopBigRam }, url: page_('qwen3-14b-instruct') })
    await mode(page, 'custom').click()
    await page.getByTestId('placement-layers').fill('10')
    await expect(verdict(page)).toHaveAttribute('data-state', 'spill')
    await expect(verdict(page)).toContainText('Spills to CPU')
    await expect(verdict(page)).toContainText('stays in system memory')
    await expect(verdict(page)).toContainText('Slower than all layers on the GPU')
    await expect(verdict(page)).not.toContainText(/\d+(\.\d+)?\s?x\b/)
    await expect(verdict(page)).not.toContainText('tokens per second')
    await expect(page.getByTestId('placement-bar-gpu-0')).toBeVisible()
    await expect(page.getByTestId('placement-bar-ram')).toBeVisible()
  })

  test('a spill that does not fit in system memory either says so, with both figures', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' } })
    await mode(page, 'custom').click()
    await page.getByTestId('placement-layers').fill('10')
    await expect(verdict(page)).toHaveAttribute('data-state', 'spill-over')
    await expect(verdict(page)).toContainText('Not enough memory')
    await expect(verdict(page)).toContainText('would stay in system memory, which has')
    await expect(page.getByTestId('placement-bar-ram')).toContainText('over')
  })

  test('too many layers for the GPU is said plainly, with the engine behaviour that is true', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' } })
    await mode(page, 'custom').click()
    await page.getByTestId('placement-layers').fill('40')
    await expect(verdict(page)).toHaveAttribute('data-state', 'toomany')
    await expect(verdict(page)).toContainText('Too many layers for the GPU')
    await expect(verdict(page)).toContainText('llama.cpp')
    await expect(page.getByTestId('placement-bar-gpu-0')).toContainText('over')
  })

  test('Auto that does not all fit says the engine will lower the count, and what all layers need', async ({ page }) => {
    await setup(page, { ledger: { resources: laptopBigRam } })
    await expect(verdict(page)).toHaveAttribute('data-state', 'trimmed')
    await expect(verdict(page)).toContainText('lowers the layer count at load time')
    const need = vramFor(entry('qwen3-14b-instruct'), 8192, null)
    await expect(verdict(page)).toContainText(`All layers need ${(need / GB).toFixed(1)} GB`)
  })

  test('Auto that does not fit in memory after the trim says so', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' } })
    await expect(verdict(page)).toHaveAttribute('data-state', 'trimmed-over')
    await expect(verdict(page)).toContainText('Not enough memory')
  })

  test('the bar grows past the tick when it does not fit, never clipped to look like it fits', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' } })
    await mode(page, 'custom').click()
    await page.getByTestId('placement-layers').fill('40')
    await expect(page.getByTestId('placement-bar-gpu-0').locator('.memorybar')).toHaveAttribute('data-over', 'true')
  })
})

test.describe('Placement - two GPUs', () => {
  test('one bar per GPU with its name, and the split controls appear', async ({ page }) => {
    await setup(page, { ledger: { profile: 'twogpu' } })
    await mode(page, 'custom').click()
    await expect(page.getByTestId('placement-bar-gpu-0')).toContainText('GPU 0, RTX 4090')
    await expect(page.getByTestId('placement-bar-gpu-1')).toContainText('GPU 1, RTX 3060')
    await expect(page.getByTestId('placement-split')).toBeVisible()
  })

  test('the split slider writes tensor_split as percentages that sum to 100', async ({ page }) => {
    const { patches } = await setup(page, { ledger: { profile: 'twogpu' } })
    await page.getByTestId('placement-split-slider').fill('70')
    await expect(page.getByTestId('placement-split-0')).toHaveValue('70')
    await expect(page.getByTestId('placement-split-1')).toHaveValue('30')
    await expect(file(page).locator('[data-written="true"]', { hasText: 'tensor_split: 70,30' })).toBeVisible()
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { tensor_split: '70,30' } }])
  })

  test('Split by free memory writes the shares from what each card has free', async ({ page }) => {
    await setup(page, { ledger: { profile: 'twogpu' } })
    await page.getByTestId('placement-split-free').click()
    // 21.4 and 11.6 GB free
    await expect(file(page)).toContainText('tensor_split: 65,35')
    await expect(page.getByTestId('placement-split-0')).toHaveValue('65')
  })

  test('the model is shared between the bars by the split', async ({ page }) => {
    await setup(page, { ledger: { profile: 'twogpu' } })
    // A bar is as wide as its own card, so a segment's width is its share of
    // that card. Scale by the card (24 and 12 GB) to compare the bytes.
    const bytes = (id, card) => page.getByTestId(`placement-bar-gpu-${id}`).locator('[data-segment="model"]')
      .evaluate((el, size) => el.getBoundingClientRect().width * size, card)
    const ratio = async () => (await bytes(0, 24)) / (await bytes(1, 12))
    await page.getByTestId('placement-split-slider').fill('50')
    await expect.poll(async () => Math.abs((await ratio()) - 1)).toBeLessThan(0.05)
    await page.getByTestId('placement-split-slider').fill('90')
    await expect.poll(ratio).toBeGreaterThan(5)
  })

  test('Main GPU writes main_gpu as the card index', async ({ page }) => {
    const { patches } = await setup(page, { ledger: { profile: 'twogpu' } })
    await page.getByTestId('placement-main-1').click()
    await expect(page.getByTestId('placement-main-1')).toHaveAttribute('aria-pressed', 'true')
    await expect(file(page)).toContainText('main_gpu: "1"')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { main_gpu: '1' } }])
  })

  test('CPU only hides the split controls', async ({ page }) => {
    await setup(page, { ledger: { profile: 'twogpu' } })
    await mode(page, 'cpu').click()
    await expect(page.getByTestId('placement-split')).toHaveCount(0)
  })

  test('with one GPU there is no split at all', async ({ page }) => {
    await setup(page)
    await expect(page.getByTestId('placement-split')).toHaveCount(0)
  })
})

test.describe('Placement - machines without a usable GPU', () => {
  test('no GPU: only CPU only can be chosen, and the page says why', async ({ page }) => {
    await setup(page, { ledger: { profile: 'nogpu' } })
    await expect(mode(page, 'auto')).toBeDisabled()
    await expect(mode(page, 'custom')).toBeDisabled()
    await expect(mode(page, 'cpu')).toBeEnabled()
    await expect(page.getByTestId('placement-nogpu')).toContainText('No GPU was found')
    await expect(verdict(page)).toHaveAttribute('data-state', 'nogpu')
    await expect(verdict(page)).toContainText('No GPU found')
    await expect(page.getByTestId('placement-bar-ram')).toBeVisible()
    await expect(page.getByTestId('placement-bar-gpu-0')).toHaveCount(0)
    await expect(page.getByTestId('placement-fit')).toHaveCount(0)
  })

  test('no GPU and a model bigger than free memory says there is not enough', async ({ page }) => {
    await setup(page, { ledger: { profile: 'nogpu' }, url: page_('llama-3.3-70b-instruct-iq2') })
    await expect(verdict(page)).toHaveAttribute('data-state', 'nogpu-over')
    await expect(verdict(page)).toContainText('Not enough memory')
  })

  test('a CPU only choice that does not fit in memory says so', async ({ page }) => {
    await setup(page, { ledger: { resources: { ...PROFILES.laptop, ram: { total: 8 * GB, used: 7 * GB, free: 1 * GB, available: 1 * GB, usage_percent: 87 } } } })
    await mode(page, 'cpu').click()
    await expect(verdict(page)).toHaveAttribute('data-state', 'cpu-over')
    await expect(verdict(page)).toContainText('Not enough memory')
  })

  test('a cluster controller shows no device bars, because they would describe the wrong machine', async ({ page }) => {
    await setup(page, { ledger: { resources: { ...PROFILES.gpu24, cluster: { enabled: true, total_memory: 80 * GB, node_name: 'worker-1', node_count: 2, is_gpu: true } } } })
    await expect(page.getByTestId('placement-cluster')).toContainText('other machines')
    await expect(page.getByTestId('placement-bar-gpu-0')).toHaveCount(0)
    await expect(verdict(page)).toHaveCount(0)
    await expect(page.getByTestId('placement-fit')).toHaveCount(0)
  })

  test('when the machine cannot be read the section says so and still edits the keys', async ({ page }) => {
    const { patches } = await setup(page, { ledger: { failResources: true } })
    await expect(page.getByTestId('placement')).toContainText('could not be read')
    await expect(verdict(page)).toHaveCount(0)
    await mode(page, 'cpu').click()
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { gpu_layers: 0 } }])
  })
})

test.describe('Placement - the estimate', () => {
  test('while it loads the bars are skeletons and Fit it for me waits', async ({ page }) => {
    await setup(page, { placement: { estimate: 'slow', slowMs: 2500 } })
    await expect(page.getByTestId('placement-loading')).toBeVisible()
    await expect(page.getByTestId('placement-fit')).toBeDisabled()
    await expect(page.getByTestId('placement-bar-gpu-0')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('placement-fit')).toBeEnabled()
  })

  test('unavailable: a note with Retry replaces the bars, and Fit it for me is gone', async ({ page }) => {
    const { estimateCalls } = await setup(page, { placement: { estimate: 'unavailable' } })
    const note = page.getByTestId('placement-unavailable')
    await expect(note).toContainText('Estimate unavailable')
    await expect(note).toContainText('could not read the model file')
    await expect(page.getByTestId('placement-fit')).toHaveCount(0)
    await expect(page.getByTestId('placement-bar-gpu-0')).toHaveCount(0)
    await expect(verdict(page)).toHaveCount(0)
    // The keys can still be edited.
    await mode(page, 'cpu').click()
    await expect(file(page)).toContainText('gpu_layers: 0')
    const calls = estimateCalls.length
    await page.getByTestId('placement-retry').click()
    await expect.poll(() => estimateCalls.length).toBeGreaterThan(calls)
  })

  test('a failing estimate endpoint is the same as unavailable', async ({ page }) => {
    await setup(page, { placement: { estimate: 'error' } })
    await expect(page.getByTestId('placement-unavailable')).toBeVisible()
  })

  test('Retry reads the estimate again and the bars come back', async ({ page }) => {
    const handle = await setup(page, { placement: { estimate: 'unavailable' } })
    await expect(page.getByTestId('placement-unavailable')).toBeVisible()
    // The stub reads its mode on every call.
    handle.estimate = 'ok'
    await page.getByTestId('placement-retry').click()
    await expect(page.getByTestId('placement-bar-gpu-0')).toBeVisible()
  })
})

test.describe('Placement - Fit it for me', () => {
  const laptopFree = 5.4 * GB
  const largest = (name, ctx) => {
    let best = 0
    for (let layers = 1; layers <= 256; layers++) {
      if (vramFor(entry(name), ctx, layers) <= laptopFree * 0.95) best = layers
    }
    return best
  }

  test('picks the largest layer count whose estimate fits, from real estimate calls', async ({ page }) => {
    const { patches, estimateCalls } = await setup(page, { ledger: { resources: laptopBigRam } })
    const expected = largest('qwen3-14b-instruct', 8192)
    expect(expected).toBeGreaterThan(0)
    expect(expected).toBeLessThan(40)
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-applied')).toContainText(`Set to ${expected} layers on the GPU`)
    await expect(mode(page, 'custom')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('placement-layers')).toHaveValue(String(expected))
    await expect(file(page)).toContainText(`gpu_layers: ${expected}`)
    await expect(verdict(page)).toHaveAttribute('data-state', 'spill')
    // The search asked the server about more than one layer count.
    const layerAsks = new Set(estimateCalls.map(call => call.gpu_layers))
    expect(layerAsks.size).toBeGreaterThan(4)
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: 'qwen3-14b-instruct', patch: { gpu_layers: expected } }])
  })

  test('Undo puts back what was there', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' } })
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-applied')).toBeVisible()
    await page.getByTestId('placement-undo').click()
    await expect(mode(page, 'auto')).toHaveAttribute('aria-checked', 'true')
    await expect(file(page)).not.toContainText('gpu_layers')
    await expect(page.getByTestId('placement-applied')).toHaveCount(0)
    await expect(save(page)).toBeDisabled()
  })

  test('Undo restores a previous number, not just Auto', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' }, url: page_('llama-3.3-70b-instruct-iq2') })
    await expect(page.getByTestId('placement-layers')).toHaveValue('20')
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-applied')).toBeVisible()
    await page.getByTestId('placement-undo').click()
    await expect(page.getByTestId('placement-layers')).toHaveValue('20')
    await expect(file(page)).toContainText('gpu_layers: 20')
  })

  test('another edit clears the Undo, because it would no longer mean what it says', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' } })
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-applied')).toBeVisible()
    await page.getByTestId('placement-context-4096').click()
    await expect(page.getByTestId('placement-applied')).toHaveCount(0)
  })

  test('when every layer fits it sets all layers and says so', async ({ page }) => {
    await setup(page)
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-applied')).toContainText('Set to all layers')
    await expect(file(page)).toContainText('gpu_layers: 99999999')
  })

  test('when not even one layer fits it changes nothing and says what to lower', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' }, url: page_('llama-3.3-70b-instruct-iq2') })
    await page.getByTestId('placement-context-131072').click()
    await expect(page.getByTestId('placement-bar-gpu-0')).toBeVisible()
    const before = await file(page).textContent()
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-fit-note')).toContainText('Not even one layer fits')
    await expect(page.getByTestId('placement-applied')).toHaveCount(0)
    expect(await file(page).textContent()).toBe(before)
  })

  test('when layers do not change the estimate it says so instead of guessing', async ({ page }) => {
    await setup(page, { ledger: { profile: 'laptop' }, placement: { modelLayers: 0 } })
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-fit-note')).toContainText('does not change with the layer count')
  })

  test('it sits with the bars on the model page for a model that is running', async ({ page }) => {
    await setup(page, { url: page_('qwen3-8b-instruct') })
    await expect(page.getByTestId('placement-fit')).toBeVisible()
    await expect(page.getByText('Changes apply the next time it loads.')).toBeVisible()
  })
})

test.describe('Placement - in the model editor', () => {
  async function editor(page, name = 'qwen3-14b-instruct', ledger = {}, placement = {}) {
    await mockLedger(page, ledger)
    const handle = await mockPlacement(page, placement)
    await page.goto(`/app/model-editor/${name}`)
    await expect(page.getByTestId('editor-placement')).toBeVisible({ timeout: 15_000 })
    return handle
  }

  test('the section is in the editor with a link in the section rail and keeps the YAML tab and field search', async ({ page }) => {
    await editor(page)
    await expect(page.getByTestId('rail-placement')).toBeVisible()
    await expect(page.locator('input[placeholder="Search fields to add..."]')).toBeVisible()
    await expect(page.getByRole('button', { name: /YAML/ })).toBeVisible()
    await expect(page.getByTestId('placement-mode-auto')).toHaveAttribute('aria-checked', 'true')
  })

  test('a choice dirties the editor and the editor save sends it with the other fields', async ({ page }) => {
    const { patches } = await editor(page)
    await expect(page.getByRole('button', { name: 'Saved' })).toBeDisabled()
    await page.getByTestId('placement-mode-cpu').click()
    const saveButton = page.getByRole('button', { name: 'Save Changes' })
    await expect(saveButton).toBeEnabled()
    await saveButton.click()
    await expect.poll(() => patches.length).toBe(1)
    expect(patches[0].name).toBe('qwen3-14b-instruct')
    expect(patches[0].patch.gpu_layers).toBe(0)
    expect(patches[0].patch.context_size).toBe(8192)
    expect(patches[0].patch.name).toBe('qwen3-14b-instruct')
    await expect(page.getByRole('button', { name: 'Saved' })).toBeDisabled()
  })

  test('Auto after a number sends null so the key leaves the file', async ({ page }) => {
    const { patches } = await editor(page, 'llama-3.3-70b-instruct-iq2')
    await expect(page.getByTestId('placement-layers')).toHaveValue('20')
    await page.getByTestId('placement-mode-auto').click()
    await page.getByRole('button', { name: 'Save Changes' }).click()
    await expect.poll(() => patches.length).toBe(1)
    expect(patches[0].patch.gpu_layers).toBeNull()
    expect('gpu_layers' in patches[0].patch).toBe(true)
  })

  test('the generic fields and the section agree: editing one moves the other', async ({ page }) => {
    await editor(page, 'llama-3.3-70b-instruct-iq2')
    await page.getByTestId('placement-layers').fill('9')
    await expect(page.getByText('GPU Layers').first()).toBeVisible()
    await expect(page.locator('input[type="number"]').nth(1)).toHaveValue('9')
  })

  test('the context presets write context_size into the same form', async ({ page }) => {
    const { patches } = await editor(page)
    await page.getByTestId('placement-context-16384').click()
    await page.getByRole('button', { name: 'Save Changes' }).click()
    await expect.poll(() => patches.length).toBe(1)
    expect(patches[0].patch.context_size).toBe(16384)
  })

  test('Fit it for me works in the editor and Undo returns to the saved state, which is clean again', async ({ page }) => {
    await editor(page, 'qwen3-14b-instruct', { profile: 'laptop' })
    await page.getByTestId('placement-fit').click()
    await expect(page.getByTestId('placement-applied')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
    await page.getByTestId('placement-undo').click()
    await expect(page.getByRole('button', { name: 'Saved' })).toBeDisabled()
  })

  test('a key that was set back to Auto and saved as null opens as Auto, with no empty field', async ({ page }) => {
    await editor(page, 'qwen3-14b-instruct', {}, { configs: { 'qwen3-14b-instruct': 'name: qwen3-14b-instruct\nbackend: llama-cpp\ncontext_size: 8192\ngpu_layers: null\n' } })
    await expect(page.getByTestId('placement-mode-auto')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByRole('button', { name: 'Saved' })).toBeDisabled()
    await expect(page.locator('input[type="number"]')).toHaveCount(1)
  })

  test('Add Model has no Placement section, because there is no model to estimate yet', async ({ page }) => {
    await mockLedger(page)
    await mockPlacement(page)
    await page.goto('/app/model-editor')
    await expect(page.locator('h1', { hasText: 'Add Model' })).toBeVisible()
    await expect(page.getByTestId('editor-placement')).toHaveCount(0)
  })

  test('the section fits a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await editor(page)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    await expect(page.getByTestId('placement-mode-custom')).toBeVisible()
  })
})
