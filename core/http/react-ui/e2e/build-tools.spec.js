import { test, expect } from './coverage-fixtures.js'
import { CPU_HOST, GPU_HOST, ftEvents, ftJob, mockFineTune, mockMachine, mockQuantize, qzJob } from './tools-fixtures.js'

// Fine-tune and Quantize share one pattern: set up, check before you start (the
// list redraws as the form changes), run with progress and a log, then a result
// with real next steps. Every number on these pages comes from the stubs below.

const MODEL = 'TinyLlama/TinyLlama-1.1B-Chat-v1.0'
const expectStep = (page, key, state) => expect(page.locator(`[data-testid="tool-steps"] [data-step="${key}"]`)).toHaveAttribute('data-state', state)
const check = (page, id) => page.locator(`[data-testid="tool-checks"] [data-check="${id}"]`)

async function boot(page, { host = GPU_HOST, ft = {}, qz = {} } = {}) {
  await mockMachine(page, { host })
  const ftState = { jobs: [], ...ft }
  const qzState = { jobs: [], ...qz }
  await mockFineTune(page, ftState)
  await mockQuantize(page, qzState)
  return { ftState, qzState }
}

async function fillFineTune(page) {
  await page.fill('#ft-model', MODEL)
  await page.fill('#ft-dataset', 'tatsu-lab/alpaca')
}

test.describe('Fine-tune: set up and check', () => {
  test('starts empty: Set up is current, Start is off, and the check says what is missing', async ({ page }) => {
    await boot(page)
    await page.goto('/app/fine-tune')
    await expect(page.getByTestId('fine-tune-form')).toBeVisible()
    await expectStep(page, 'setup', 'current')
    await expect(page.getByTestId('ft-start')).toBeDisabled()
    await expect(check(page, 'model')).toHaveAttribute('data-tone', 'fail')
    await expect(check(page, 'data')).toContainText('Add a dataset')
    await expect(page.getByTestId('ft-bar')).toContainText('Enter the model to train.')
    await expect(page.getByTestId('ft-recipe')).toContainText('Choose a model and a dataset')
  })

  test('a model and a dataset move to Check and switch Start on', async ({ page }) => {
    await boot(page)
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await expectStep(page, 'check', 'current')
    await expect(page.getByTestId('ft-start')).toBeEnabled()
    await expect(check(page, 'model')).toHaveAttribute('data-tone', 'ok')
    await expect(check(page, 'data')).toContainText('Data comes from tatsu-lab/alpaca')
    await expect(check(page, 'backend')).toContainText('trl is installed')
    await expect(check(page, 'memory')).toContainText('RTX 4070: 9.8 GB of 12 GB free')
    await expect(check(page, 'memory')).toContainText('LocalAI does not estimate it before the job starts')
    await expect(check(page, 'memory').getByRole('img')).toHaveAttribute('aria-label', /18 percent in use/)
    await expect(check(page, 'disk')).toContainText('118 GB free on the models disk')
    await expect(page.getByTestId('ft-recipe')).toContainText('Train a LoRA adapter of TinyLlama-1.1B-Chat-v1.0 with SFT, 3 epochs over tatsu-lab/alpaca.')
    await expect(page.getByTestId('ft-bar')).toContainText('Nothing blocks the start.')
  })

  test('the check list redraws as the form changes', async ({ page }) => {
    await boot(page)
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await expect(check(page, 'memory')).toContainText('Batch size 2')
    await page.fill('#ft-batch', '4')
    await expect(check(page, 'memory')).toContainText('Batch size 4')
    await page.fill('#ft-epochs', '5')
    await expect(page.getByTestId('ft-recipe')).toContainText('5 epochs')
    await page.getByRole('radio', { name: /Full model/ }).check()
    await expect(page.getByTestId('ft-recipe')).toContainText('full model')

    // GRPO needs a reward function: a warning that goes when one is chosen.
    await page.getByTestId('ft-more-toggle').click()
    await page.selectOption('#ft-method', 'grpo')
    await expect(check(page, 'reward')).toHaveAttribute('data-tone', 'warn')
    await expect(page.getByTestId('ft-bar')).toContainText('1 warning. You can still start.')
    await expect(page.getByTestId('ft-start')).toBeEnabled()
    await page.getByLabel('format_reward').check()
    await expect(check(page, 'reward')).toHaveCount(0)
    await expect(page.getByTestId('ft-bar')).toContainText('Nothing blocks the start.')
  })

  test('with no GPU the memory check warns and names the RAM', async ({ page }) => {
    await boot(page, { host: CPU_HOST })
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await expect(check(page, 'memory')).toHaveAttribute('data-tone', 'warn')
    await expect(check(page, 'memory')).toContainText('No GPU found. Training uses the CPU and 20 GB of 32 GB RAM is free.')
  })

  test('with no backend installed it warns, links to Backends, and still lets you start', async ({ page }) => {
    await boot(page, { ft: { backends: [] } })
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await expect(check(page, 'backend')).toHaveAttribute('data-tone', 'warn')
    await expect(check(page, 'backend')).toContainText('No fine-tuning backend is installed')
    await expect(check(page, 'backend').getByRole('link', { name: 'Open Backends' })).toHaveAttribute('href', '/app/backends')
    await expect(page.getByTestId('ft-start')).toBeEnabled()
  })

  test('a user who cannot read the machine still gets a check list', async ({ page }) => {
    await boot(page, { host: null })
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await expect(check(page, 'memory')).toContainText('This account cannot read the machine')
    await expect(check(page, 'disk')).toHaveCount(0)
  })

  test('Start sends the request the form describes and opens the job', async ({ page }) => {
    const { ftState } = await boot(page)
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await page.fill('#ft-split', 'train')
    await page.fill('#ft-lr', '5e-5')
    await page.getByTestId('ft-start').click()
    await expect(page.getByTestId('job-view')).toBeVisible()
    expect(ftState.started).toHaveLength(1)
    expect(ftState.started[0]).toMatchObject({
      model: MODEL, backend: 'trl', training_method: 'sft', training_type: 'lora', dataset_source: 'tatsu-lab/alpaca',
      dataset_split: 'train', num_epochs: 3, batch_size: 2, learning_rate: 0.00005, adapter_rank: 16,
    })
    expect(ftState.started[0].extra_options).toMatchObject({ max_seq_length: '2048', eval_strategy: 'no' })
    await expectStep(page, 'run', 'current')
  })

  test('an uploaded file is the dataset, and is sent to the upload endpoint first', async ({ page }) => {
    const { ftState } = await boot(page)
    await page.goto('/app/fine-tune')
    await page.fill('#ft-model', MODEL)
    await page.setInputFiles('#ft-dataset-file', { name: 'pairs.jsonl', mimeType: 'application/json', buffer: Buffer.from('{"text":"a"}\n') })
    await expect(check(page, 'data')).toContainText('pairs.jsonl')
    await page.getByTestId('ft-start').click()
    await expect(page.getByTestId('job-view')).toBeVisible()
    expect(ftState.started[0].dataset_source).toBe('/data/datasets/upload.jsonl')
  })

  test('a refused start shows the server message and keeps the form', async ({ page }) => {
    await boot(page, { ft: { startError: 'failed to load backend trl: not found' } })
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await page.getByTestId('ft-start').click()
    await expect(page.getByTestId('tool-error')).toContainText('failed to load backend trl: not found')
    await expect(page.getByTestId('fine-tune-form')).toBeVisible()
    await expect(page.locator('#ft-model')).toHaveValue(MODEL)
  })

  test('More options carries the method, adapter and evaluation settings', async ({ page }) => {
    await boot(page)
    await page.goto('/app/fine-tune')
    await expect(page.locator('#ft-method')).toHaveCount(0)
    await page.getByTestId('ft-more-toggle').click()
    for (const id of ['#ft-backend', '#ft-method', '#ft-type', '#ft-rank', '#ft-optimizer', '#ft-hf-token', '#ft-seq-len']) await expect(page.locator(id)).toBeVisible()
    await page.getByRole('switch', { name: 'Evaluate while training' }).click()
    await expect(page.locator('#ft-eval-strategy')).toBeVisible()
  })
})

test.describe('Fine-tune: run, fail, finish', () => {
  const open = async (page) => {
    await page.getByTestId('ft-jobs').getByRole('button', { name: MODEL }).click()
    await expect(page.getByTestId('job-view')).toBeVisible()
  }

  test('a running job shows progress, stages, chart and log from its events', async ({ page }) => {
    await boot(page, { ft: { jobs: [ftJob('job-run', 'training')], events: { 'job-run': ftEvents(540) } } })
    await page.goto('/app/fine-tune')
    await open(page)
    await expectStep(page, 'run', 'current')
    const view = page.getByTestId('job-view')
    await expect(view.locator('.bt-job__percent')).toContainText('60')
    await expect(view).toContainText('step 540 of 900')
    await expect(view).toContainText('epoch 1.8 of 3')
    await expect(view).toContainText('about 12 min left')
    await expect(view).toContainText('312 tokens/s')
    await expect(view.getByRole('progressbar', { name: 'Progress' })).toHaveAttribute('aria-valuenow', '60')
    await expect(page.locator('[data-testid="job-stages"] [data-state="current"]')).toHaveText('Training')
    // The chart: a line, the evaluation loss as hollow dots, the last value.
    await expect(page.getByTestId('job-chart').locator('.bt-chart__line')).toBeVisible()
    await expect(page.getByTestId('job-chart').locator('.bt-chart__eval')).toHaveCount(5)
    await expect(page.getByTestId('job-chart')).toContainText('Hollow dots are the evaluation loss')
    await page.getByRole('tab', { name: 'Learning rate' }).click()
    await expect(page.getByTestId('job-chart').getByRole('heading', { name: 'Learning rate' })).toBeVisible()
    await expect(page.getByTestId('job-chart').locator('.bt-chart__eval')).toHaveCount(0)
    // The log is what the events said, and the filter shows warnings only.
    await expect(page.getByTestId('job-log')).toContainText('step 530/900 loss')
    await page.getByTestId('job-log-filter').check()
    await expect(page.getByTestId('job-log')).toContainText('No warnings or errors.')
  })

  test('Stop asks whether to keep a checkpoint and sends the answer', async ({ page }) => {
    const { ftState } = await boot(page, { ft: { jobs: [ftJob('job-run', 'training')], events: { 'job-run': ftEvents(100) } } })
    await page.goto('/app/fine-tune')
    await open(page)
    await page.getByTestId('job-stop').click()
    await expect(page.getByTestId('stop-dialog')).toContainText('Stop training?')
    await page.getByTestId('stop-keep').click()
    await expect(page.getByTestId('stop-dialog')).toHaveCount(0)
    expect(ftState.stopped).toEqual([{ id: 'job-run', save: 'true' }])
    await page.getByTestId('job-stop').click()
    await page.getByTestId('stop-discard').click()
    expect(ftState.stopped[1]).toEqual({ id: 'job-run', save: 'false' })
    await page.getByTestId('job-stop').click()
    await page.getByRole('button', { name: 'Keep training' }).click()
    expect(ftState.stopped).toHaveLength(2)
  })

  test('a failed job shows the server message, offers the fixes for a memory failure, and applies them to a copy', async ({ page }) => {
    const message = 'torch.OutOfMemoryError: CUDA out of memory. Tried to allocate 1.34 GiB.'
    await boot(page, {
      ft: {
        jobs: [ftJob('job-f', 'failed', { message })],
        checkpoints: { 'job-f': [{ path: '/data/finetune/job-f/checkpoint-200', step: 200, epoch: 0.66, loss: 1.4012, created_at: '2026-10-08 10:10' }] },
      },
    })
    await page.goto('/app/fine-tune')
    await open(page)
    await expectStep(page, 'run', 'failed')
    await expect(page.getByTestId('job-failed')).toHaveAttribute('role', 'alert')
    await expect(page.getByTestId('job-failed-message')).toHaveText(message)
    await expect(page.getByTestId('job-try')).toContainText('Lower the batch size to 1')
    await expect(page.getByRole('button', { name: 'Resume from step 200' })).toBeVisible()
    await page.getByTestId('job-retry').click()
    // Back on the form, with the setup restored and the two fixes applied.
    await expect(page.getByTestId('fine-tune-form')).toBeVisible()
    await expect(page.locator('#ft-model')).toHaveValue(MODEL)
    await expect(page.locator('#ft-batch')).toHaveValue('1')
    await page.getByTestId('ft-more-toggle').click()
    await expect(page.getByLabel(/Gradient checkpointing/)).toBeChecked()
  })

  test('a failure that is not about memory gets no invented advice', async ({ page }) => {
    await boot(page, { ft: { jobs: [ftJob('job-f', 'failed', { message: 'dataset not found: tatsu-lab/alpaca' })] } })
    await page.goto('/app/fine-tune')
    await open(page)
    await expect(page.getByTestId('job-failed-message')).toHaveText('dataset not found: tatsu-lab/alpaca')
    await expect(page.getByTestId('job-try')).not.toContainText('batch size')
    await expect(page.getByTestId('job-try')).toContainText('change what the message points at')
  })

  test('Resume from a checkpoint puts it in the form', async ({ page }) => {
    await boot(page, {
      ft: {
        jobs: [ftJob('job-f', 'failed', { message: 'stopped' })],
        checkpoints: { 'job-f': [{ path: '/data/finetune/job-f/checkpoint-200', step: 200, epoch: 0.66, loss: 1.4012, created_at: 'x' }] },
      },
    })
    await page.goto('/app/fine-tune')
    await open(page)
    await page.getByRole('button', { name: 'Resume from step 200' }).click()
    await expect(page.getByText('Resuming from checkpoint:')).toBeVisible()
    await expect(page.getByTestId('fine-tune-form')).toContainText('/data/finetune/job-f/checkpoint-200')
    await expect(page.getByTestId('ft-start')).toContainText('Resume training')
  })

  test('a finished job offers the export, and an exported model leads to Chat and Models', async ({ page }) => {
    const { ftState } = await boot(page, {
      ft: {
        jobs: [ftJob('job-d', 'completed')],
        checkpoints: { 'job-d': [{ path: '/data/finetune/job-d/checkpoint-500', step: 500, epoch: 1.66, loss: 0.8911, created_at: 'x' }] },
      },
    })
    await page.goto('/app/fine-tune')
    await open(page)
    await expectStep(page, 'result', 'current')
    await expect(page.getByTestId('job-finished')).toContainText('Training finished.')
    await expect(page.getByTestId('job-checkpoints')).toContainText('1.4012'.replace('1.4012', '0.8911'))
    await page.fill('#ft-export-name', 'tinyllama-ft')
    await page.selectOption('#ft-export-format', 'gguf')
    await page.fill('#ft-export-quant', 'q5_k_m')
    await page.getByRole('region', { name: 'Export as a model' }).getByRole('button', { name: 'Export', exact: true }).click()
    expect(ftState.exported[0]).toMatchObject({ name: 'tinyllama-ft', export_format: 'gguf', quantization_method: 'q5_k_m', checkpoint_path: '/data/finetune/job-d', model: MODEL })
  })

  test('an exported job links to Chat, Models and the archive', async ({ page }) => {
    await boot(page, { ft: { jobs: [ftJob('job-d', 'completed', { export_status: 'completed', export_model_name: 'tinyllama-ft' })] } })
    await page.goto('/app/fine-tune')
    await open(page)
    const next = page.getByTestId('export-next')
    await expect(next).toContainText('tinyllama-ft is a model now')
    await expect(next.getByRole('link', { name: 'Chat with tinyllama-ft' })).toHaveAttribute('href', '/app/chat/tinyllama-ft')
    await expect(next.getByRole('link', { name: 'Open in Models' })).toHaveAttribute('href', '/app/models?view=installed')
    await expect(next.getByRole('link', { name: 'Download archive' })).toHaveAttribute('href', /jobs\/job-d\/download/)
  })

  test('New job returns to the form', async ({ page }) => {
    await boot(page, { ft: { jobs: [ftJob('job-d', 'completed')] } })
    await page.goto('/app/fine-tune')
    await open(page)
    await page.getByTestId('job-back').click()
    await expect(page.getByTestId('fine-tune-form')).toBeVisible()
  })
})

test.describe('Fine-tune: earlier jobs', () => {
  test('lists jobs with kind and status, reuses a setup and deletes after a confirmation', async ({ page }) => {
    const { ftState } = await boot(page, {
      ft: { jobs: [ftJob('job-1', 'completed', { model: 'Qwen2.5-0.5B', training_method: 'dpo', config: { model: 'Qwen2.5-0.5B', dataset_source: 'x/y', training_method: 'dpo', batch_size: 3 } }), ftJob('job-2', 'failed', { model: 'gemma-2-2b', message: 'unsupported' })] },
    })
    await page.goto('/app/fine-tune')
    const jobs = page.getByTestId('ft-jobs')
    await expect(jobs).toContainText('2 kept on this server')
    await expect(jobs.locator('tbody tr')).toHaveCount(2)
    await expect(jobs.locator('tbody tr').first()).toContainText('lora, dpo')
    await expect(jobs.locator('tbody tr').nth(1)).toContainText('unsupported')
    await jobs.locator('tbody tr').first().getByRole('button', { name: 'Reuse' }).click()
    await expect(page.locator('#ft-model')).toHaveValue('Qwen2.5-0.5B')
    await expect(page.locator('#ft-dataset')).toHaveValue('x/y')
    await expect(page.locator('#ft-batch')).toHaveValue('3')
    await jobs.locator('tbody tr').nth(1).getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByRole('alertdialog')).toContainText('Delete this job?')
    await page.getByRole('button', { name: 'Cancel' }).click()
    expect(ftState.deleted).toEqual([])
    await jobs.locator('tbody tr').nth(1).getByRole('button', { name: 'Delete' }).click()
    await page.getByTestId('delete-confirm').click()
    await expect.poll(() => ftState.deleted).toEqual(['job-2'])
  })

  test('with no jobs it names the next step', async ({ page }) => {
    await boot(page)
    await page.goto('/app/fine-tune')
    await expect(page.getByTestId('ft-jobs').locator('.dk-empty-title')).toHaveText('No fine-tuning jobs yet')
  })
})

test.describe('Quantize', () => {
  test('setup: a model is needed, the type can be custom, and the check lists the facts', async ({ page }) => {
    await boot(page)
    await page.goto('/app/quantize')
    await expectStep(page, 'setup', 'current')
    await expect(page.getByTestId('qz-start')).toBeDisabled()
    await page.fill('#qz-model', 'meta-llama/Llama-3.2-1B')
    await expectStep(page, 'check', 'current')
    await expect(page.getByTestId('qz-start')).toContainText('Quantize (q4_k_m)')
    await expect(check(page, 'backend')).toContainText('llama-cpp-quantization is installed')
    await expect(check(page, 'memory')).toContainText('20 GB of 32 GB RAM is free. Conversion runs on the CPU.')
    await expect(check(page, 'disk')).toContainText('118 GB free on the models disk')
    await expect(page.getByTestId('qz-recipe')).toContainText('Convert Llama-3.2-1B to GGUF at q4_k_m.')
    await page.selectOption('#qz-type', 'q8_0')
    await expect(page.getByTestId('qz-recipe')).toContainText('at q8_0')
    await page.selectOption('#qz-type', '__custom__')
    await expect(check(page, 'type')).toHaveAttribute('data-tone', 'fail')
    await expect(page.getByTestId('qz-start')).toBeDisabled()
    await page.fill('#qz-custom', 'iq4_nl')
    await expect(page.getByTestId('qz-start')).toContainText('Quantize (iq4_nl)')
    await expect(page.getByTestId('qz-start')).toBeEnabled()
  })

  test('no backend: a warning and a link, and the start stays possible', async ({ page }) => {
    await boot(page, { qz: { backends: [] } })
    await page.goto('/app/quantize')
    await page.fill('#qz-model', 'meta-llama/Llama-3.2-1B')
    await expect(check(page, 'backend')).toHaveAttribute('data-tone', 'warn')
    await expect(check(page, 'backend').getByRole('link', { name: 'Open Backends' })).toBeVisible()
    await expect(page.getByTestId('qz-start')).toBeEnabled()
  })

  test('Start sends the request, with the token under extra options, and opens the job', async ({ page }) => {
    const { qzState } = await boot(page)
    await page.goto('/app/quantize')
    await page.fill('#qz-model', 'meta-llama/Llama-3.2-1B')
    await page.getByRole('button', { name: /More options/ }).click()
    await page.fill('#qz-token', 'hf_secret')
    await page.getByTestId('qz-start').click()
    await expect(page.getByTestId('job-view')).toBeVisible()
    expect(qzState.started[0]).toEqual({ model: 'meta-llama/Llama-3.2-1B', backend: 'llama-cpp-quantization', quantization_type: 'q4_k_m', extra_options: { hf_token: 'hf_secret' } })
    await expectStep(page, 'run', 'current')
  })

  test('a running job shows the stage, progress and log, and Stop sends the stop', async ({ page }) => {
    const events = [{ job_id: 'qz1', status: 'downloading', progress_percent: 10, message: 'Downloading model files' }, { job_id: 'qz1', status: 'converting', progress_percent: 42, message: 'converting tensor 84 of 201' }]
    const { qzState } = await boot(page, { qz: { jobs: [qzJob('qz1', 'converting')], events: { qz1: events } } })
    await page.goto('/app/quantize')
    await page.getByTestId('qz-jobs').getByRole('button', { name: 'meta-llama/Llama-3.2-1B' }).click()
    const view = page.getByTestId('job-view')
    await expect(view.locator('.bt-job__percent')).toContainText('42')
    await expect(view.getByRole('progressbar', { name: 'Progress' })).toHaveAttribute('aria-valuenow', '42')
    await expect(view).toContainText('converting tensor 84 of 201')
    await expect(page.locator('[data-testid="job-stages"] [data-state="current"]')).toHaveText('Converting')
    await expect(page.locator('[data-testid="job-stages"] [data-state="done"]')).toHaveCount(2)
    await expect(page.getByTestId('job-log')).toContainText('Downloading model files')
    await page.getByTestId('job-stop').click()
    expect(qzState.stopped).toEqual(['qz1'])
  })

  test('a failed job shows the server message and reuses its setup', async ({ page }) => {
    await boot(page, { qz: { jobs: [qzJob('qz9', 'failed', { model: 'acme/odd-model', quantization_type: 'q5_k_m', message: 'unsupported architecture: OddForCausalLM' })] } })
    await page.goto('/app/quantize')
    await page.getByTestId('qz-jobs').getByRole('button', { name: 'acme/odd-model' }).click()
    await expectStep(page, 'run', 'failed')
    await expect(page.getByTestId('job-failed-message')).toHaveText('unsupported architecture: OddForCausalLM')
    await page.getByTestId('job-retry').click()
    await expect(page.locator('#qz-model')).toHaveValue('acme/odd-model')
    await expect(page.locator('#qz-type')).toHaveValue('q5_k_m')
  })

  test('a finished job imports under a name and then leads to Chat and Models', async ({ page }) => {
    const { qzState } = await boot(page, { qz: { jobs: [qzJob('qz2', 'completed', { output_file: '/data/quant/qz2/model-q4_k_m.gguf' })] } })
    await page.goto('/app/quantize')
    await page.getByTestId('qz-jobs').getByRole('button', { name: 'meta-llama/Llama-3.2-1B' }).click()
    await expectStep(page, 'result', 'current')
    await expect(page.getByTestId('job-finished')).toContainText('Quantization finished: q4_k_m.')
    await expect(page.getByTestId('quantize-output')).toContainText('/data/quant/qz2/model-q4_k_m.gguf')
    await expect(page.getByRole('link', { name: 'Download GGUF' })).toHaveAttribute('href', /jobs\/qz2\/download/)
    await page.fill('#qz-import-name', 'llama-1b-q4')
    await page.getByTestId('quantize-import').click()
    const next = page.getByTestId('quantize-next')
    await expect(next).toContainText('llama-1b-q4 is a model now')
    expect(qzState.imported).toEqual([{ name: 'llama-1b-q4' }])
    await expect(next.getByRole('link', { name: 'Chat with llama-1b-q4' })).toHaveAttribute('href', '/app/chat/llama-1b-q4')
    await expect(next.getByRole('link', { name: 'Open in Models' })).toHaveAttribute('href', '/app/models?view=installed')
  })

  test('earlier jobs: imported mark, failure line, delete after a confirmation', async ({ page }) => {
    const { qzState } = await boot(page, {
      qz: { jobs: [qzJob('qz0', 'completed', { import_status: 'completed', import_model_name: 'llama-3.2-1b-q4_k_m' }), qzJob('qz9', 'failed', { model: 'acme/odd-model', message: 'unsupported architecture' })] },
    })
    await page.goto('/app/quantize')
    const jobs = page.getByTestId('qz-jobs')
    await expect(jobs.locator('tbody tr').first()).toContainText('imported as llama-3.2-1b-q4_k_m')
    await expect(jobs.locator('tbody tr').nth(1)).toContainText('unsupported architecture')
    await jobs.locator('tbody tr').nth(1).getByRole('button', { name: 'Delete' }).click()
    await page.getByTestId('delete-confirm').click()
    await expect.poll(() => qzState.deleted).toEqual(['qz9'])
  })

  test('with no jobs it names the next step', async ({ page }) => {
    await boot(page)
    await page.goto('/app/quantize')
    await expect(page.getByTestId('qz-jobs').locator('.dk-empty-title')).toHaveText('No quantization jobs yet')
  })
})

test.describe('Disabled: the account lacks the permission', () => {
  const member = (permissions) => (page) => page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'u1', name: 'Sam', role: 'user', permissions } },
  }))

  test('Fine-tune says the account cannot use it, who can change that, and where to go', async ({ page }) => {
    await boot(page)
    await member({ chat: true })(page)
    await page.goto('/app/fine-tune')
    const off = page.getByTestId('tool-off')
    await expect(off).toHaveAttribute('data-cause', 'account')
    await expect(off).toContainText('Your account cannot fine-tune')
    await expect(off).toContainText('Ask an administrator')
    await expect(off.getByRole('link', { name: 'Go to Home' })).toHaveAttribute('href', '/app')
    await expect(page.getByTestId('fine-tune-form')).toHaveCount(0)
  })

  test('Quantize has its own words', async ({ page }) => {
    await boot(page)
    await member({ chat: true, fine_tuning: true })(page)
    await page.goto('/app/quantize')
    await expect(page.getByTestId('tool-off')).toContainText('Your account cannot quantize')
    await page.goto('/app/fine-tune')
    await expect(page.getByTestId('fine-tune-form')).toBeVisible()
  })

  test('an administrator is never shown the page', async ({ page }) => {
    await boot(page)
    await page.goto('/app/fine-tune')
    await expect(page.getByTestId('fine-tune-form')).toBeVisible()
    await expect(page.getByTestId('tool-off')).toHaveCount(0)
  })
})

test.describe('Build tools: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  const noOverflow = (page) => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)

  test('Fine-tune setup and check fit the screen', async ({ page }) => {
    await boot(page, { ft: { jobs: [ftJob('job-1', 'completed'), ftJob('job-2', 'failed', { message: 'x' })] } })
    await page.goto('/app/fine-tune')
    await fillFineTune(page)
    await page.getByTestId('ft-more-toggle').click()
    await expect(check(page, 'memory')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
    await expect(page.getByTestId('ft-start')).toBeVisible()
  })

  test('a running Fine-tune job fits the screen', async ({ page }) => {
    await boot(page, { ft: { jobs: [ftJob('job-run', 'training')], events: { 'job-run': ftEvents(540) } } })
    await page.goto('/app/fine-tune')
    await page.getByTestId('ft-jobs').getByRole('button', { name: MODEL }).click()
    await expect(page.getByTestId('job-chart').locator('.bt-chart__line')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
  })

  test('Quantize finished fits the screen', async ({ page }) => {
    await boot(page, { qz: { jobs: [qzJob('qz2', 'completed', { output_file: '/data/quant/qz2/a-very-long-file-name-q4_k_m.gguf' })] } })
    await page.goto('/app/quantize')
    await page.getByTestId('qz-jobs').getByRole('button', { name: 'meta-llama/Llama-3.2-1B' }).click()
    await expect(page.getByTestId('quantize-output')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
  })

  test('the disabled page fits the screen', async ({ page }) => {
    await boot(page)
    await page.route('**/api/auth/status', route => route.fulfill({
      json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user: { id: 'u1', name: 'Sam', role: 'user', permissions: { chat: true } } },
    }))
    await page.goto('/app/quantize')
    await expect(page.getByTestId('tool-off')).toBeVisible()
    expect(await noOverflow(page)).toBe(false)
  })
})

test.describe('Build tools: reduced motion', () => {
  test('the job card does not animate', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await boot(page, { ft: { jobs: [ftJob('job-run', 'training')], events: { 'job-run': ftEvents(100) } } })
    await page.goto('/app/fine-tune')
    await page.getByTestId('ft-jobs').getByRole('button', { name: MODEL }).click()
    const duration = await page.getByTestId('job-view').locator('.bt-job').evaluate(el => getComputedStyle(el).animationDuration)
    expect(parseFloat(duration)).toBeLessThan(0.01)
  })
})
