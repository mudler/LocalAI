import { test, expect } from './coverage-fixtures.js'
import { mockLedger, mockPlacement, CONFIGS } from './ledger-fixtures.js'

// The Experts (MoE) block of the Placement section: where the experts of a
// mixture-of-experts model sit. It edits three entries of the model's `options`
// list (cpu_moe, n_cpu_moe, moe_cache_mib) and leaves every other entry alone.

const NAME = 'qwen3-14b-instruct'
const base = CONFIGS[NAME]
const withBackend = (backend, extra = '') => ({
  ...CONFIGS,
  [NAME]: backend === null
    ? `${base.replace('backend: llama-cpp\n', '')}${extra}`
    : `${base.replace('backend: llama-cpp', `backend: ${backend}`)}${extra}`,
})
const withOptions = (...entries) => ({ ...CONFIGS, [NAME]: `${base}options:\n${entries.map(e => `  - ${e}\n`).join('')}` })

const moe = (page, id) => page.getByTestId(`placement-moe-${id}`)
const block = page => page.getByTestId('placement-moe')
const file = page => page.getByTestId('placement-file')
const save = page => page.getByTestId('config-save')

async function setup(page, { configs = CONFIGS, expertCount = 128, layerCount = 0, ledger = {} } = {}) {
  await mockLedger(page, ledger)
  const handle = await mockPlacement(page, { configs, expertCount, layerCount })
  await page.goto(`/app/models/${NAME}?tab=config`)
  await expect(page.getByTestId('placement')).toBeVisible({ timeout: 15_000 })
  return handle
}

test.describe('Placement - experts: visibility', () => {
  test('a dense model has no experts block', async ({ page }) => {
    await setup(page, { expertCount: 0 })
    await expect(page.getByTestId('placement-memory')).toBeVisible()
    await expect(block(page)).toHaveCount(0)
  })

  test('a MoE model shows it, with GPU chosen and nothing written', async ({ page }) => {
    const { patches } = await setup(page)
    await expect(block(page)).toBeVisible()
    await expect(block(page)).toContainText('cpu_moe, n_cpu_moe, moe_cache_mib')
    await expect(moe(page, 'gpu')).toHaveAttribute('aria-checked', 'true')
    await expect(moe(page, 'gpu')).toContainText('writes nothing')
    await expect(moe(page, 'some')).toContainText('n_cpu_moe: N')
    await expect(moe(page, 'all')).toContainText('cpu_moe: true')
    await expect(page.getByTestId('placement-moe-cache')).toHaveCount(0)
    await expect(page.getByTestId('placement-moe-note')).toContainText('The estimate does not include expert placement.')
    await expect(page.getByTestId('placement-moe-auto-hint')).toContainText('Auto-fit may move experts')
    await expect(save(page)).toBeDisabled()
    expect(patches).toEqual([])
  })

  test('CPU only hides the block, and so does a machine with no GPU', async ({ page }) => {
    await setup(page)
    await page.getByTestId('placement-mode-cpu').click()
    await expect(block(page)).toHaveCount(0)
    await page.getByTestId('placement-mode-custom').click()
    await expect(block(page)).toBeVisible()
    await expect(page.getByTestId('placement-moe-auto-hint')).toHaveCount(0)
  })

  test('only the llama.cpp backend gets the block', async ({ page }) => {
    await setup(page, { configs: withBackend('vllm') })
    await expect(page.getByTestId('placement-memory')).toBeVisible()
    await expect(block(page)).toHaveCount(0)
  })

  test('a pinned llama.cpp variant gets it, ik-llama-cpp does not', async ({ page }) => {
    await setup(page, { configs: withBackend('cuda12-llama-cpp') })
    await expect(block(page)).toBeVisible()
    await page.goto('about:blank')
    await setup(page, { configs: withBackend('ik-llama-cpp') })
    await expect(page.getByTestId('placement-memory')).toBeVisible()
    await expect(block(page)).toHaveCount(0)
  })

  test('no backend set resolves to llama.cpp, so the block shows', async ({ page }) => {
    await setup(page, { configs: withBackend(null) })
    await expect(block(page)).toBeVisible()
  })

  test('expert entries on another backend do not bring the block back', async ({ page }) => {
    await setup(page, { configs: withBackend('vllm', 'options:\n  - cpu_moe:true\n') })
    await expect(page.getByTestId('placement-memory')).toBeVisible()
    await expect(block(page)).toHaveCount(0)
  })

  test('no GPU, no block', async ({ page }) => {
    await setup(page, { ledger: { profile: 'nogpu' } })
    await expect(page.getByTestId('placement-memory')).toBeVisible()
    await expect(block(page)).toHaveCount(0)
  })
})

test.describe('Placement - experts: choices', () => {
  test('All on CPU writes cpu_moe:true, shows the cache, and saves exactly that', async ({ page }) => {
    const { patches } = await setup(page)
    await moe(page, 'all').click()
    await expect(moe(page, 'all')).toHaveAttribute('aria-checked', 'true')
    await expect(file(page).locator('[data-written="true"]', { hasText: 'cpu_moe:true' })).toBeVisible()
    await expect(page.getByTestId('placement-moe-cache')).toBeVisible()
    await expect(page.getByTestId('placement-moe-cache-hint')).toContainText('Experimental')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: NAME, patch: { options: ['cpu_moe:true'] } }])
  })

  test('Some layers writes n_cpu_moe, takes a number and replaces cpu_moe', async ({ page }) => {
    const { patches } = await setup(page, { configs: withOptions('cpu_moe:true') })
    await expect(moe(page, 'all')).toHaveAttribute('aria-checked', 'true')
    await moe(page, 'some').click()
    await expect(page.getByTestId('placement-moe-layers-input')).toHaveValue('1')
    await page.getByTestId('placement-moe-layers-input').fill('12')
    await expect(moe(page, 'some')).toContainText('n_cpu_moe: 12')
    await expect(file(page)).toContainText('n_cpu_moe:12')
    await expect(file(page)).not.toContainText('cpu_moe:true')
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: NAME, patch: { options: ['n_cpu_moe:12'] } }])
  })

  test('with a known layer count the layers get a slider', async ({ page }) => {
    await setup(page, { layerCount: 48 })
    await moe(page, 'some').click()
    const slider = page.getByTestId('placement-moe-slider')
    await expect(slider).toHaveAttribute('max', '48')
    await slider.fill('20')
    await expect(page.getByTestId('placement-moe-layers-input')).toHaveValue('20')
    await expect(file(page)).toContainText('n_cpu_moe:20')
  })

  test('without a layer count there is the input and a hint, no slider', async ({ page }) => {
    await setup(page)
    await moe(page, 'some').click()
    await expect(page.getByTestId('placement-moe-slider')).toHaveCount(0)
    await expect(page.getByTestId('placement-moe-no-slider')).toBeVisible()
  })

  test('an estimate with a layer count and an expert count shows both sliders and neither hint', async ({ page }) => {
    await setup(page, { layerCount: 48 })
    await page.getByTestId('placement-mode-custom').click()
    await moe(page, 'some').click()
    await expect(page.getByTestId('placement-slider')).toHaveAttribute('max', '48')
    await expect(page.getByTestId('placement-moe-slider')).toHaveAttribute('max', '48')
    await expect(page.getByTestId('placement-no-slider')).toHaveCount(0)
    await expect(page.getByTestId('placement-moe-no-slider')).toHaveCount(0)
  })

  test('the cache is shown for experts on the CPU, written in MiB, and removed with Off or with the experts', async ({ page }) => {
    const { patches } = await setup(page)
    await moe(page, 'all').click()
    await page.getByTestId('placement-moe-cache-4096').click()
    await expect(page.getByTestId('placement-moe-cache-4096')).toHaveAttribute('aria-pressed', 'true')
    await expect(file(page)).toContainText('moe_cache_mib:4096')
    await page.getByTestId('placement-moe-cache-input').fill('1500')
    await expect(file(page)).toContainText('moe_cache_mib:1500')
    await expect(page.getByTestId('placement-moe-cache-4096')).toHaveAttribute('aria-pressed', 'false')
    await page.getByTestId('placement-moe-cache-0').click()
    await expect(file(page)).not.toContainText('moe_cache_mib')
    await page.getByTestId('placement-moe-cache-8192').click()
    await moe(page, 'gpu').click()
    await expect(page.getByTestId('placement-moe-cache')).toHaveCount(0)
    await expect(file(page)).not.toContainText('moe')
    await expect(save(page)).toBeDisabled()
    expect(patches).toEqual([])
  })
})

test.describe('Placement - experts: the options list', () => {
  const others = ['context_shift:true', 'parallel:2']

  test('other entries are kept, in order, when experts change', async ({ page }) => {
    const { patches } = await setup(page, { configs: withOptions('context_shift:true', 'cpu_moe:1', 'parallel:2') })
    await expect(moe(page, 'all')).toHaveAttribute('aria-checked', 'true')
    await page.getByTestId('placement-moe-cache-2048').click()
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: NAME, patch: { options: ['context_shift:true', 'cpu_moe:1', 'parallel:2', 'moe_cache_mib:2048'] } }])
  })

  test('going back to the GPU leaves only the other entries', async ({ page }) => {
    const { patches } = await setup(page, { configs: withOptions('cpu_moe:true', 'moe_cache_mib:4096', ...others) })
    await expect(page.getByTestId('placement-moe-cache-4096')).toHaveAttribute('aria-pressed', 'true')
    await moe(page, 'gpu').click()
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: NAME, patch: { options: others } }])
  })

  test('the last expert entry going away unsets the list', async ({ page }) => {
    const { patches } = await setup(page, { configs: withOptions('n_cpu_moe:8') })
    await expect(moe(page, 'some')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('placement-moe-layers-input')).toHaveValue('8')
    await moe(page, 'gpu').click()
    await save(page).click()
    await expect.poll(() => patches).toEqual([{ name: NAME, patch: { options: null } }])
  })

  test('a flag set another way is shown as set elsewhere and is not rewritten', async ({ page }) => {
    await setup(page, { configs: withOptions('--cpu-moe', ...others), expertCount: 0 })
    await expect(page.getByTestId('placement-moe-elsewhere')).toContainText('--cpu-moe')
    await expect(moe(page, 'all')).toHaveCount(0)
    await expect(save(page)).toBeDisabled()
  })
})

test.describe('Placement - experts: in the model editor', () => {
  test('the choice goes through the editor save with the other options kept', async ({ page }) => {
    await mockLedger(page, {})
    const { patches } = await mockPlacement(page, { configs: withOptions(...['parallel:2']), expertCount: 64 })
    await page.goto(`/app/model-editor/${NAME}`)
    await expect(page.getByTestId('editor-placement')).toBeVisible({ timeout: 15_000 })
    await moe(page, 'all').click()
    await page.getByRole('button', { name: 'Save Changes' }).click()
    await expect.poll(() => patches.length).toBe(1)
    expect(patches[0].patch.options).toEqual(['parallel:2', 'cpu_moe:true'])
  })

  test('the editor hides the block for another backend and shows it for llama.cpp', async ({ page }) => {
    await mockLedger(page, {})
    await mockPlacement(page, { configs: withBackend('vllm'), expertCount: 64 })
    await page.goto(`/app/model-editor/${NAME}`)
    await expect(page.getByTestId('editor-placement')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('placement-memory')).toBeVisible()
    await expect(block(page)).toHaveCount(0)
    await mockPlacement(page, { configs: CONFIGS, expertCount: 64 })
    await page.reload()
    await expect(block(page)).toBeVisible({ timeout: 15_000 })
  })
})
