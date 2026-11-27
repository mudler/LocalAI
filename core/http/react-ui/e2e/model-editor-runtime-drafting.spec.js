import { test, expect } from './coverage-fixtures.js'
import { CONFIGS, mockRuntime } from './engine-runtime-fixtures.js'

// The Drafting section of Runtime tuning: one choice of how tokens are
// drafted (Off, N-gram, Draft model, MTP), read back from the file, written
// as spec_type, draft_model and options entries, the draft model picked from
// the installed models with a fit and a memory line from the VRAM estimate,
// and MTP offered only for a model with an MTP head.

const field = (page, id) => page.getByTestId(`rt-${id}`)
const mode = (page, id) => page.getByTestId(`rt-draft-mode-${id}`)
const save = page => page.getByTestId('editor-save')
const lastPatch = state => state.patches[state.patches.length - 1]?.patch

const GIB = 1024 * 1024 * 1024
const MODELS = [
  { id: 'qwen3-32b', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
  { id: 'qwen3-0.6b', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
  { id: 'llama-3.2-1b', backend: 'cuda12-llama-cpp', capabilities: ['FLAG_CHAT'] },
  { id: 'whisper-base', backend: 'whisper', capabilities: ['FLAG_TRANSCRIPT'] },
  { id: 'qwen3-mtp', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
]
const FILES = { 'qwen3-0.6b': 'Qwen3-0.6B-Q8_0.gguf', 'llama-3.2-1b': 'Llama-3.2-1B-Q4_K_M.gguf', 'qwen3-32b': 'Qwen3-32B-Q4_K_M.gguf' }
const FACTS = {
  'qwen3-32b': { architecture: 'qwen3', vocab_size: 151936 },
  'qwen3-0.6b': { architecture: 'qwen3', vocab_size: 151936, size_bytes: 0.6 * GIB, vram_bytes: 0.9 * GIB, kv: 0.3 * GIB },
  'llama-3.2-1b': { architecture: 'llama', vocab_size: 128256, size_bytes: 0.8 * GIB, vram_bytes: 1.1 * GIB, kv: 0.3 * GIB },
  'qwen3-mtp': { architecture: 'qwen35', vocab_size: 248320, mtp_layers: 1 },
}

const CONFIGS_DRAFT = {
  ...CONFIGS,
  'qwen3-32b': 'name: qwen3-32b\nbackend: llama-cpp\nparameters:\n  model: Qwen3-32B-Q4_K_M.gguf\ncontext_size: 16384\noptions:\n  - keep_me:true\n',
  'qwen3-saved': 'name: qwen3-saved\nbackend: llama-cpp\nparameters:\n  model: Qwen3-32B-Q4_K_M.gguf\ndraft_model: Qwen3-0.6B-Q8_0.gguf\noptions:\n  - draft_max:8\n',
  'qwen3-mtp': 'name: qwen3-mtp\nbackend: llama-cpp\nparameters:\n  model: Qwen3.5-MTP.gguf\n',
  'qwen3-custom': 'name: qwen3-custom\nbackend: llama-cpp\nparameters:\n  model: Qwen3-32B-Q4_K_M.gguf\noptions:\n  - spec_type:ngram-mod,draft-mtp\n',
}

async function open(page, name = 'qwen3-32b') {
  const state = await mockRuntime(page, { configs: CONFIGS_DRAFT })
  state.estimates = []
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: MODELS } }))
  // Registered after mockRuntime, so these answer first.
  await page.route('**/api/models/config-json/*', route => {
    if (route.request().method() === 'PATCH') return route.fallback()
    const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    return route.fulfill({ json: { name: id, parameters: { model: FILES[id] || `${id}.gguf` } } })
  })
  await page.route('**/api/models/vram-estimate', route => {
    const body = route.request().postDataJSON()
    state.estimates.push(body)
    const f = FACTS[body.model] || {}
    const ctx = body.context_size || 8192
    return route.fulfill({
      json: {
        size_bytes: f.size_bytes || 4 * GIB, vram_bytes: f.vram_bytes || 6 * GIB, context_length: ctx,
        architecture: f.architecture, vocab_size: f.vocab_size, mtp_layers: f.mtp_layers,
        runtime: { context_length: ctx, kv_cache_bytes: f.kv || GIB, compute_bytes: 0.2 * GIB },
      },
    })
  })
  await page.goto(`/app/model-editor/${name}`)
  await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('rt-drafting')).toBeVisible({ timeout: 10_000 })
  return state
}

test.describe('Runtime tuning: drafting', () => {
  test('starts Off, offers no MTP without a head, and writes n-gram drafting with its fields', async ({ page }) => {
    const state = await open(page)
    await expect(mode(page, 'off')).toHaveAttribute('aria-checked', 'true')
    await expect(mode(page, 'mtp')).toHaveCount(0)
    await expect(page.getByTestId('rt-draft-panel-off')).toContainText('No drafting')
    // The old collapsed group is gone; its rare flags are folded in the section.
    await expect(page.getByTestId('rt-group-speculative')).toHaveCount(0)
    await expect(page.getByTestId('rt-draft-more')).toBeVisible()

    await mode(page, 'ngram').click()
    await expect(page.getByTestId('rt-draft-note')).toContainText('can cost a few percent')
    await expect(field(page, 'options-spec_ngram_mod_n_max-field')).toContainText('Draft tokens, at most')
    await field(page, 'options-spec_max_streams').fill('2')
    await field(page, 'options-spec_max_streams').blur()
    await save(page).click()
    await expect.poll(() => state.patches.length).toBeGreaterThan(0)
    expect(lastPatch(state).options).toEqual(['keep_me:true', 'spec_type:ngram-mod', 'spec_max_streams:2'])
    // The streams option is the section's, not a raw option.
    await expect(page.getByTestId('rt-raw-1')).toHaveCount(0)
  })

  test('picks a draft model of the same family, with its fit and memory', async ({ page }) => {
    const state = await open(page)
    await mode(page, 'model').click()
    await expect(page.getByTestId('rt-draft-panel-model')).toBeVisible()
    await page.getByTestId('rt-draft-model').click()
    const menu = page.getByTestId('rt-draft-model-menu')
    await expect(menu).toContainText('Same family as this model')
    // The model itself and models of other engines are not offered.
    await expect(menu.getByRole('option', { name: 'qwen3-32b' })).toHaveCount(0)
    await expect(menu.getByRole('option', { name: 'whisper-base' })).toHaveCount(0)
    await menu.getByRole('option', { name: 'qwen3-0.6b' }).click()

    await expect(page.getByTestId('rt-draft-fit-chip')).toHaveText('Compatible')
    await expect(page.getByTestId('rt-draft-memory')).toContainText('more')
    await expect(page.getByTestId('rt-draft-memory')).toContainText('16,384 tokens')
    expect(state.estimates.some(b => b.model === 'qwen3-0.6b' && b.context_size === 16384 && b.cache_type_k === 'f16')).toBe(true)

    await field(page, 'options-spec_p_min-input').fill('0.6')
    await field(page, 'options-spec_p_min-input').blur()
    await save(page).click()
    await expect.poll(() => state.patches.length).toBeGreaterThan(0)
    const patch = lastPatch(state)
    expect(patch.draft_model).toBe('Qwen3-0.6B-Q8_0.gguf')
    expect(patch.options).toEqual(['keep_me:true', 'spec_p_min:0.6'])
  })

  test('warns when the vocabularies differ', async ({ page }) => {
    await open(page)
    await mode(page, 'model').click()
    await page.getByTestId('rt-draft-model').click()
    await page.getByTestId('rt-draft-model-menu').getByRole('option', { name: 'llama-3.2-1b' }).click()
    await expect(page.getByTestId('rt-draft-fit-chip')).toHaveText('Will not load')
    await expect(page.getByTestId('rt-draft-fit')).toContainText('151,936')
  })

  test('reads a saved draft model and its old option name back, and Off removes them', async ({ page }) => {
    const state = await open(page, 'qwen3-saved')
    await expect(mode(page, 'model')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-draft-model')).toContainText('qwen3-0.6b')
    await expect(field(page, 'options-spec_n_max')).toHaveValue('8')
    await mode(page, 'off').click()
    await save(page).click()
    await expect.poll(() => state.patches.length).toBeGreaterThan(0)
    const patch = lastPatch(state)
    // The editor's patch removes a key with null.
    expect(patch.draft_model ?? null).toBeNull()
    expect(patch.options ?? null).toBeNull()
  })

  test('shows MTP on by default for a model with a head, and Off writes none', async ({ page }) => {
    const state = await open(page, 'qwen3-mtp')
    await expect(mode(page, 'mtp')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-draft-auto')).toContainText('on by default')
    // The window LocalAI applies to an MTP head, not the llama.cpp default.
    await expect(field(page, 'options-spec_n_max')).toHaveAttribute('placeholder', 'Default (6)')
    await mode(page, 'off').click()
    await save(page).click()
    await expect.poll(() => state.patches.length).toBeGreaterThan(0)
    expect(lastPatch(state).options).toEqual(['spec_type:none'])
  })

  test('keeps a custom drafting setup until a mode is picked', async ({ page }) => {
    await open(page, 'qwen3-custom')
    await expect(page.getByTestId('rt-draft-custom')).toContainText('ngram-mod, draft-mtp')
    await expect(page.getByTestId('rt-raw-0')).toHaveCount(0)
  })
})
