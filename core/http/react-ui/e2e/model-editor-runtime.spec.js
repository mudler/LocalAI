import { test, expect } from './coverage-fixtures.js'
import {
  ALL_EXPERTS, INKLING_COMMAND, INKLING_OT, MIXED_COMMAND, MODEL, mockRuntime,
} from './llamacpp-runtime-fixtures.js'

// The Runtime tuning section of the model editor: the llama.cpp settings of a
// llama-cpp model as fields built from the server's schema, the tensor
// placement picker, whole-setup presets, the command preview, pasting a
// llama-server command and the raw options. The /api/llama-cpp routes answer
// with saved outputs of the real handlers; model-editor-runtime-real.spec.js
// runs the same flows against the handlers themselves.

const section = page => page.getByTestId('runtime')
const save = page => page.getByTestId('editor-save')
const field = (page, id) => page.getByTestId(`rt-${id}`)
const lastPatch = state => state.patches[state.patches.length - 1]?.patch

async function open(page, name = MODEL, options = {}) {
  const state = await mockRuntime(page, options)
  await page.goto(`/app/model-editor/${name}`)
  await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 15_000 })
  return state
}

async function ready(page) {
  await expect(field(page, 'context_size')).toBeVisible({ timeout: 10_000 })
}

test.describe('Runtime tuning: when it shows', () => {
  test('a llama-cpp model gets the section, with every field and its flag', async ({ page }) => {
    await open(page)
    await ready(page)
    await expect(page.getByTestId('rail-runtime')).toBeVisible()
    for (const g of ['memory', 'speed', 'placement', 'sampling', 'template', 'advanced']) {
      await expect(page.getByTestId(`rt-group-${g}`)).toBeVisible()
    }
    const flags = {
      context_size: '--ctx-size', gpu_layers: '-ngl', 'options-parallel': '-np', flash_attention: '-fa',
      cache_type_k: '-ctk', cache_type_v: '-ctv', 'options-ubatch': '-ub', threads: '-t',
      'options-threads_batch': '-tb', 'options-fit': '-fit', 'options-no_op_offload': '--no-op-offload',
      'parameters-temperature': '--temp', 'parameters-top_p': '--top-p', 'parameters-top_k': '--top-k',
      'parameters-min_p': '--min-p', 'options-use_jinja': '--jinja', 'parameters-batch': '-b',
    }
    for (const [id, flag] of Object.entries(flags)) {
      await expect(page.getByTestId(`rt-${id}-flag`), id).toHaveText(flag)
    }
    await expect(page.getByTestId('rt-load-mode')).toHaveValue('default')
    await expect(page.getByTestId('rt-load-mode-field')).toContainText('--load-mode')
    await expect(field(page, 'context_size')).toHaveValue('8192')
  })

  test('a model on another backend has no section', async ({ page }) => {
    await open(page, 'vllm-model')
    await expect(page.getByTestId('editor-placement')).toBeVisible()
    await expect(section(page)).toHaveCount(0)
    await expect(page.getByTestId('rail-runtime')).toHaveCount(0)
  })

  test('a model with no backend resolves to llama.cpp and gets it', async ({ page }) => {
    await open(page, 'no-backend')
    await ready(page)
  })

  test('the section waits for the schema with a placeholder', async ({ page }) => {
    await open(page, MODEL, { schema: 'slow', slowMs: 2500 })
    await expect(page.getByTestId('runtime-loading')).toBeVisible()
    await ready(page)
    await expect(page.getByTestId('runtime-loading')).toHaveCount(0)
  })

  test('a schema error says so, points to the YAML editor and can retry', async ({ page }) => {
    const state = await open(page, MODEL, { schema: 'error' })
    const callout = page.getByTestId('runtime-schema-error')
    await expect(callout).toBeVisible()
    await expect(callout).toContainText('Runtime tuning is not available.')
    await expect(callout).toContainText('settings table unavailable')
    await expect(callout).toContainText('as text in the other tab')
    await page.getByTestId('runtime-open-yaml').click()
    await expect(page.locator('.cm-content')).toContainText('name: moe-lab')
    await page.getByRole('button', { name: 'Interactive' }).click()
    state.schema = 'ok'
    await page.getByTestId('runtime-retry').click()
    await ready(page)
  })
})

test.describe('Runtime tuning: fields', () => {
  test('edits write the YAML keys and the options entries on save', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await expect(save(page)).toBeDisabled()
    await field(page, 'context_size').fill('32768')
    await field(page, 'threads').fill('6')
    await field(page, 'options-ubatch').fill('64')
    await field(page, 'flash_attention-on').click()
    await field(page, 'cache_type_k').selectOption('q8_0')
    await field(page, 'options-use_jinja-true').click()
    await field(page, 'options-fit-off').click()
    await field(page, 'options-no_op_offload-true').click()
    await page.locator('#rt-parameters-top_p').fill('0.9')
    await field(page, 'parameters-temperature').fill('0.7')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    const patch = lastPatch(state)
    expect(patch).toMatchObject({
      context_size: 32768, threads: 6, flash_attention: 'on', cache_type_k: 'q8_0',
      parameters: { temperature: 0.7, top_p: 0.9 },
    })
    expect(patch.options).toEqual(['context_shift:true', 'ubatch:64', 'use_jinja:true', 'fit:off', 'no_op_offload:true'])
  })

  test('back to Default removes the setting from the file', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await field(page, 'context_size').fill('')
    await field(page, 'flash_attention-off').click()
    await field(page, 'flash_attention-').click()
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).context_size).toBeNull()
    expect(lastPatch(state)).not.toHaveProperty('flash_attention')
  })

  test('text that does not validate is marked and blocks Save', async ({ page }) => {
    await open(page)
    await ready(page)
    await field(page, 'threads').fill('6')
    await expect(save(page)).toBeEnabled()
    await field(page, 'context_size').fill('12.5')
    await expect(page.getByTestId('rt-context_size-error')).toHaveText('Type a whole number.')
    await expect(field(page, 'context_size')).toHaveAttribute('aria-invalid', 'true')
    await expect(save(page)).toBeDisabled()
    await page.locator('#rt-parameters-top_p').fill('1.5')
    await expect(page.getByTestId('rt-parameters-top_p-field')).toContainText('Type a value from 0 to 1.')
    await field(page, 'context_size').fill('4096')
    await page.locator('#rt-parameters-top_p').fill('0.5')
    await expect(page.getByTestId('rt-context_size-error')).toHaveCount(0)
    await expect(save(page)).toBeEnabled()
  })

  test('the load mode writes mmap, mmlock and direct I/O', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-load-mode').selectOption('mmap_mlock')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state)).toMatchObject({ mmap: true, mmlock: true })
    await page.getByTestId('rt-load-mode').selectOption('direct_io')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(2)
    expect(lastPatch(state).options).toContain('direct_io:true')
    expect(lastPatch(state).mmap).toBeNull()
    expect(lastPatch(state).mmlock).toBeNull()
  })
})

test.describe('Runtime tuning: tensor placement', () => {
  test('it is off by default and writes nothing', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('off')
    await expect(page.getByTestId('rt-ot-none')).toBeVisible()
    await field(page, 'threads').fill('4')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(JSON.stringify(lastPatch(state))).not.toContain('-ot')
    expect(state.placements).toEqual([])
  })

  test('MoE experts on CPU asks the server and shows the pattern', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-ot-mode').selectOption('experts')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText(ALL_EXPERTS.slice(4))
    expect(state.placements[0]).toEqual({ placement: { mode: 'experts_cpu_all' } })
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).options).toEqual(['context_shift:true', ALL_EXPERTS])
  })

  test('the layer-range picker builds the -ot regex', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-ot-mode').selectOption('ranges')
    await page.getByTestId('rt-ot-to-0').fill('6')
    await page.getByTestId('rt-ot-add').click()
    await page.getByTestId('rt-ot-from-1').fill('18')
    await page.getByTestId('rt-ot-to-1').fill('23')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText(INKLING_OT.slice(4))
    expect(state.placements[state.placements.length - 1]).toEqual({
      placement: { mode: 'experts_cpu_layers', layers: [{ from: 0, to: 6 }, { from: 18, to: 23 }] },
    })
    await page.getByTestId('rt-ot-to-1').fill('')
    await expect(page.getByTestId('rt-ot-error')).toContainText('Each range needs a start and an end layer')
    await expect(save(page)).toBeDisabled()
    await page.getByTestId('rt-ot-to-1').fill('23')
    await expect(save(page)).toBeEnabled()
    await page.getByTestId('rt-ot-remove-1').click()
    await expect(page.getByTestId('rt-ot-regex')).toContainText('blk')
  })

  test('first N layers on the GPU sends the counts and shows a server error', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-ot-mode').selectOption('rest')
    await page.getByTestId('rt-ot-gpu-layers').fill('12')
    await page.getByTestId('rt-ot-total-layers').fill('48')
    await expect.poll(() => state.placements[state.placements.length - 1]).toEqual({
      placement: { mode: 'first_layers_gpu', first_gpu_layers: 12, total_layers: 48 },
    })
    await page.getByTestId('rt-ot-gpu-layers').fill('5000')
    await expect(page.getByTestId('rt-ot-error')).toContainText('first_gpu_layers must be between 0 and 999')
  })

  test('a raw regex is checked and written as typed', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-ot-mode').selectOption('raw')
    await page.getByTestId('rt-ot-raw').fill('blk\\.(=CPU')
    await expect(page.getByTestId('rt-ot-error')).toContainText('not a valid regular expression')
    await page.getByTestId('rt-ot-raw').fill('token_embd=CUDA0')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText('token_embd=CUDA0')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).options).toContain('-ot:token_embd=CUDA0')
  })

  test('an entry in the file is read back into the picker', async ({ page }) => {
    await open(page, 'moe-ot')
    await ready(page)
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('ranges')
    await expect(page.getByTestId('rt-ot-from-0')).toHaveValue('0')
    await expect(page.getByTestId('rt-ot-to-0')).toHaveValue('6')
    await expect(page.getByTestId('rt-ot-from-1')).toHaveValue('18')
    await expect(page.getByTestId('rt-ot-to-1')).toHaveValue('23')
    // The placement entries are not raw options.
    await expect(page.getByTestId('rt-raw-empty')).toBeVisible()
  })
})

test.describe('Runtime tuning: presets', () => {
  test('a preset fills fields, lists the changes, stays editable and undoes', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-preset-bigMoe').click()
    const applied = page.getByTestId('rt-applied')
    await expect(applied).toContainText('Big MoE on a small GPU')
    await expect(page.getByTestId('rt-change-flash_attention')).toContainText('Default')
    await expect(page.getByTestId('rt-change-flash_attention')).toContainText('On')
    await expect(page.getByTestId('rt-change-cache_type_k')).toContainText('q8_0')
    await expect(page.getByTestId('rt-change-options--ot')).toContainText(ALL_EXPERTS)
    await expect(field(page, 'flash_attention-on')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('experts')
    // Still editable after the preset.
    await field(page, 'cache_type_k').selectOption('f16')
    await page.getByTestId('rt-undo').click()
    await expect(applied).toHaveCount(0)
    await expect(field(page, 'flash_attention-')).toHaveAttribute('aria-checked', 'true')
    // The edit made after the preset is kept.
    await expect(field(page, 'cache_type_k')).toHaveValue('f16')
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('off')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state)).toMatchObject({ cache_type_k: 'f16' })
    expect(lastPatch(state)).not.toHaveProperty('flash_attention')
  })

  test('the other presets fill their settings', async ({ page }) => {
    await open(page)
    await ready(page)
    await page.getByTestId('rt-preset-maxContext').click()
    await expect(field(page, 'context_size')).toHaveValue('131072')
    await page.getByTestId('rt-preset-lowVram').click()
    await expect(field(page, 'context_size')).toHaveValue('4096')
    await expect(field(page, 'cache_type_v')).toHaveValue('q4_0')
    await expect(field(page, 'options-ubatch')).toHaveValue('128')
  })
})

test.describe('Runtime tuning: command preview', () => {
  test('the preview follows the form and copies', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    const state = await open(page)
    await ready(page)
    const cmd = page.getByTestId('rt-preview-command')
    await expect(cmd).toHaveText('llama-server -m moe-lab-Q4_K_M.gguf --ctx-size 8192')
    await field(page, 'threads').fill('6')
    await expect(cmd).toHaveText('llama-server -m moe-lab-Q4_K_M.gguf --ctx-size 8192 -t 6')
    // Typing is debounced: one request for the whole word.
    const before = state.previews.length
    await field(page, 'context_size').click()
    await page.keyboard.press('End')
    await page.keyboard.type('00', { delay: 30 })
    await expect(cmd).toContainText('--ctx-size 819200')
    expect(state.previews.length - before).toBe(1)
    expect(state.previews[state.previews.length - 1]).toMatchObject({ context_size: 819200, threads: 6, options: ['context_shift:true'] })
    await page.getByTestId('rt-preview-copy').click()
    await expect(page.getByTestId('rt-preview-copy')).toHaveText('Copied')
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('--ctx-size 819200 -t 6')
  })

  test('a preview error is shown in place', async ({ page }) => {
    await open(page, MODEL, { preview: 'error' })
    await ready(page)
    await expect(page.getByTestId('rt-preview-error')).toContainText('preview failed')
    await expect(page.getByTestId('rt-preview-copy')).toBeDisabled()
  })
})

test.describe('Runtime tuning: paste a command', () => {
  test('the Inkling command fills every field and leaves nothing unmapped', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-paste-text').fill(INKLING_COMMAND)
    await page.getByTestId('rt-paste-fill').click()
    await expect(page.getByTestId('rt-paste-filled')).toContainText('Every flag was mapped.')
    await expect(page.getByTestId('rt-paste-unmapped')).toHaveCount(0)
    await expect(page.getByTestId('rt-paste-warnings')).toHaveCount(0)
    expect(state.parses).toEqual([INKLING_COMMAND])

    const values = {
      context_size: '18000', gpu_layers: '99', 'options-parallel': '1', 'options-ubatch': '64',
      threads: '6', 'options-threads_batch': '12', 'parameters-temperature': '1', 'parameters-top_k': '0',
    }
    for (const [id, v] of Object.entries(values)) await expect(field(page, id), id).toHaveValue(v)
    await expect(page.locator('#rt-parameters-top_p')).toHaveValue('1')
    await expect(page.locator('#rt-parameters-min_p')).toHaveValue('0')
    await expect(field(page, 'cache_type_k')).toHaveValue('q8_0')
    await expect(field(page, 'cache_type_v')).toHaveValue('q8_0')
    for (const id of ['flash_attention-on', 'options-fit-off', 'options-no_op_offload-true', 'options-use_jinja-true']) {
      await expect(field(page, id), id).toHaveAttribute('aria-checked', 'true')
    }
    await expect(page.getByTestId('rt-load-mode')).toHaveValue('none')
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('ranges')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText(INKLING_OT.slice(4))
    // Nothing from the command lands in the raw options.
    await expect(page.getByTestId('rt-raw-0')).toHaveValue('context_shift:true')
    await expect(page.getByTestId('rt-raw-1')).toHaveCount(0)

    // The model file of the command is offered, not written.
    await expect(page.getByTestId('rt-paste-model-file')).toContainText('Inkling-2-Mini-UD-Q3_K_XL.gguf')
    await expect(page.getByTestId('rt-applied')).toContainText('Pasted command')

    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    const patch = lastPatch(state)
    expect(patch).toMatchObject({
      context_size: 18000, threads: 6, gpu_layers: 99, flash_attention: 'on', cache_type_k: 'q8_0', cache_type_v: 'q8_0', mmap: false,
      parameters: { model: 'moe-lab-Q4_K_M.gguf', temperature: 1, top_p: 1, top_k: 0, min_p: 0 },
    })
    expect(patch.options).toEqual(['context_shift:true', 'use_jinja:true', 'parallel:1', 'ubatch:64', 'threads_batch:12', 'fit:off', 'no_op_offload', INKLING_OT])
  })

  test('the model file of a command is used only when chosen', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-paste-text').fill(INKLING_COMMAND)
    await page.getByTestId('rt-paste-fill').click()
    await page.getByTestId('rt-paste-use-model').click()
    await expect(page.getByTestId('rt-paste-model-file')).toHaveCount(0)
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).parameters.model).toBe('Inkling-2-Mini-UD-Q3_K_XL.gguf')
  })

  test('flags it cannot map are listed with reasons and can be kept or dropped', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByTestId('rt-paste-text').fill(MIXED_COMMAND)
    await page.getByTestId('rt-paste-fill').click()
    const list = page.getByTestId('rt-paste-unmapped')
    await expect(list).toContainText('Flags not mapped: 5')
    await expect(page.getByTestId('rt-unmapped-0')).toContainText('--host 0.0.0.0')
    await expect(page.getByTestId('rt-unmapped-0')).toContainText('ignored: LocalAI starts')
    await expect(page.getByTestId('rt-unmapped-ignored-0')).toHaveText('Ignored')
    await expect(page.getByTestId('rt-unmapped-keep-0')).toHaveCount(0)
    // Kept by the server as raw options.
    await expect(page.getByTestId('rt-unmapped-2')).toContainText('Kept')
    await expect(page.getByTestId('rt-raw-1')).toHaveValue('--rope-freq-base:10000')
    await expect(page.getByTestId('rt-raw-2')).toHaveValue('--no-warmup')
    // A bad value is offered to keep, in one click.
    await expect(page.getByTestId('rt-unmapped-4')).toContainText('"abc" is not an integer')
    await page.getByTestId('rt-unmapped-keep-4').click()
    await expect(page.getByTestId('rt-unmapped-4')).toContainText('Kept')
    await expect(page.getByTestId('rt-raw-3')).toHaveValue('-t:abc')
    // And a kept one can be dropped.
    await page.getByTestId('rt-unmapped-drop-3').click()
    await expect(page.getByTestId('rt-raw-2')).toHaveValue('-t:abc')
    await expect(page.getByTestId('rt-paste-warnings')).toContainText('is a path')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).options).toEqual(['context_shift:true', '--rope-freq-base:10000', '-t:abc'])
  })

  test('a command the server cannot read says why', async ({ page }) => {
    await open(page)
    await ready(page)
    await page.getByTestId('rt-paste-text').fill('llama-server -m "unclosed "')
    await page.getByTestId('rt-paste-fill').click()
    await expect(page.getByTestId('rt-paste-error')).toContainText('unclosed quote in the command')
  })
})

test.describe('Runtime tuning: advanced options', () => {
  test('raw entries can be added, edited and removed, but not a field twice', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await expect(page.getByTestId('rt-raw-0')).toHaveValue('context_shift:true')
    await page.getByTestId('rt-raw-add').click()
    await page.getByTestId('rt-raw-1').fill('ubatch:32')
    await expect(page.getByTestId('rt-raw-error-1')).toContainText('ubatch is set by a field above')
    await expect(save(page)).toBeDisabled()
    await page.getByTestId('rt-raw-1').fill('--rope-freq-base:10000')
    await page.getByTestId('rt-raw-0').fill('context_shift:false')
    await page.getByTestId('rt-raw-remove-0').click()
    await expect(page.getByTestId('rt-raw-0')).toHaveValue('--rope-freq-base:10000')
    await save(page).click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(lastPatch(state).options).toEqual(['--rope-freq-base:10000'])
  })
})

test.describe('Runtime tuning: the YAML tab', () => {
  test('a YAML save shows in the form', async ({ page }) => {
    const state = await open(page)
    await ready(page)
    await page.getByRole('button', { name: 'YAML' }).click()
    const editor = page.locator('.cm-content')
    await editor.click()
    await page.keyboard.press('ControlOrMeta+a')
    await page.keyboard.press('Delete')
    await page.keyboard.insertText('name: moe-lab\nbackend: llama-cpp\nparameters:\n  model: moe-lab-Q4_K_M.gguf\n  temperature: 0.4\ncontext_size: 16384\nflash_attention: "off"\noptions:\n  - ubatch:256\n  - \'-ot:\\.ffn_.*_exps.*=CPU\'\n')
    await save(page).click()
    await expect.poll(() => state.edits.length).toBe(1)
    await page.getByRole('button', { name: 'Interactive' }).click()
    await expect(field(page, 'context_size')).toHaveValue('16384')
    await expect(field(page, 'options-ubatch')).toHaveValue('256')
    await expect(field(page, 'parameters-temperature')).toHaveValue('0.4')
    await expect(field(page, 'flash_attention-off')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('experts')
  })
})

test.describe('Runtime tuning: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the section stacks and the preview sits under the form', async ({ page }) => {
    await open(page)
    await ready(page)
    const form = await page.getByTestId('rt-group-memory').boundingBox()
    const preview = await page.getByTestId('rt-preview').boundingBox()
    expect(preview.y).toBeGreaterThan(form.y)
    const width = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(width).toBeLessThanOrEqual(390)
  })
})
