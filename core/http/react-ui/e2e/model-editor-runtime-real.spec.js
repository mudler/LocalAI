import { test, expect } from './coverage-fixtures.js'
import { INKLING_COMMAND, MODEL, mockRuntime } from './engine-runtime-fixtures.js'

// The Runtime tuning section against the real /api/engines handlers of the
// test server: only the model file and its save are stubbed. It checks that
// the section reads the server's own schema, parse, preview and placement
// answers, not only the saved copies the other spec uses.

const field = (page, id) => page.getByTestId(`rt-${id}`)

async function open(page) {
  const state = await mockRuntime(page, { engines: false })
  await page.goto(`/app/model-editor/${MODEL}`)
  await expect(field(page, 'context_size')).toBeVisible({ timeout: 15_000 })
  return state
}

test.describe('Runtime tuning against the real handlers', () => {
  test('the Inkling command fills every field, maps every flag and previews back', async ({ page }) => {
    const state = await open(page)
    await page.getByTestId('rt-paste-text').fill(INKLING_COMMAND)
    await page.getByTestId('rt-paste-fill').click()
    await expect(page.getByTestId('rt-paste-filled')).toContainText('Every flag was mapped.')
    await expect(page.getByTestId('rt-paste-unmapped')).toHaveCount(0)

    for (const [id, v] of Object.entries({
      context_size: '18000', gpu_layers: '99', 'options-parallel': '1', 'options-ubatch': '64', threads: '6',
      'options-threads_batch': '12', 'parameters-temperature': '1', 'parameters-top_k': '0',
    })) await expect(field(page, id), id).toHaveValue(v)
    await expect(page.locator('#rt-parameters-top_p')).toHaveValue('1')
    await expect(page.locator('#rt-parameters-min_p')).toHaveValue('0')
    for (const id of ['flash_attention-on', 'options-fit-off', 'options-no_op_offload-true', 'options-use_jinja-true']) {
      await expect(field(page, id), id).toHaveAttribute('aria-checked', 'true')
    }
    await expect(field(page, 'cache_type_k')).toHaveValue('q8_0')
    await expect(field(page, 'cache_type_v')).toHaveValue('q8_0')
    await expect(page.getByTestId('rt-load-mode')).toHaveValue('none')
    // The server reads its -ot entry back as layer ranges.
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('ranges')
    await expect(page.getByTestId('rt-ot-to-0')).toHaveValue('6')
    await expect(page.getByTestId('rt-ot-from-1')).toHaveValue('18')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText('blk\\.([0-6]|1[89]|2[0-3])\\.ffn_.*_exps.*=CPU')

    const cmd = page.getByTestId('rt-preview-command')
    for (const part of ['--ctx-size 18000', '-ngl 99', '-t 6', '-fa on', '-ctk q8_0', '-ctv q8_0', '--load-mode none', '--temp', '-ub 64', '-tb 12', '--no-op-offload', '-ot']) {
      await expect(cmd).toContainText(part)
    }

    await page.getByTestId('editor-save').click()
    await expect.poll(() => state.patches.length).toBe(1)
    expect(state.patches[0].patch.options).toContain('-ot:blk\\.([0-6]|1[89]|2[0-3])\\.ffn_.*_exps.*=CPU')
  })

  test('the placement picker and the MoE preset build their patterns on the server', async ({ page }) => {
    await open(page)
    await page.getByTestId('rt-ot-mode').selectOption('ranges')
    await page.getByTestId('rt-ot-to-0').fill('6')
    await page.getByTestId('rt-ot-add').click()
    await page.getByTestId('rt-ot-from-1').fill('18')
    await page.getByTestId('rt-ot-to-1').fill('23')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText('blk\\.([0-6]|1[89]|2[0-3])\\.ffn_.*_exps.*=CPU')
    await page.getByTestId('rt-ot-mode').selectOption('off')
    await page.getByTestId('rt-preset-bigMoe').click()
    await expect(page.getByTestId('rt-ot-mode')).toHaveValue('experts')
    await expect(page.getByTestId('rt-ot-regex')).toHaveText('\\.ffn_.*_exps.*=CPU')
    await expect(page.getByTestId('rt-preview-command')).toContainText('-ot')
  })
})
