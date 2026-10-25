// Stubs for the Build tool specs: the machine, the backends, and jobs for
// Fine-tune and Quantize, with progress streams, checkpoints and the calls the
// pages make. Pure of Playwright imports so a script that is not a test can use
// it too.

export const GB = 1024 ** 3

export const GPU_HOST = {
  type: 'gpu',
  available: true,
  gpus: [{ name: 'RTX 4070', vendor: 'nvidia', total_vram: 12 * GB, used_vram: 2.2 * GB, free_vram: 9.8 * GB, usage_percent: 18 }],
  ram: { total: 32 * GB, used: 12 * GB, free: 18 * GB, available: 20 * GB, usage_percent: 37 },
  aggregate: { total_memory: 12 * GB, used_memory: 2.2 * GB, free_memory: 9.8 * GB, usage_percent: 18, gpu_count: 1 },
  disk: { total: 500 * GB, used: 382 * GB, available: 118 * GB },
}

export const CPU_HOST = {
  type: 'ram',
  available: true,
  gpus: null,
  ram: { total: 32 * GB, used: 12 * GB, free: 18 * GB, available: 20 * GB, usage_percent: 37 },
  aggregate: { total_memory: 32 * GB, used_memory: 12 * GB, free_memory: 20 * GB, usage_percent: 37, gpu_count: 0 },
  disk: { total: 500 * GB, used: 382 * GB, available: 118 * GB },
}

export const FT_BACKENDS = [{ name: 'trl', description: 'TRL fine-tuning', tags: ['fine-tuning'] }]
export const QZ_BACKENDS = [{ name: 'llama-cpp-quantization', description: 'GGUF quantization', tags: ['quantization'] }]

const iso = (offsetMs) => new Date(Date.now() + offsetMs).toISOString()
const MIN = 60_000
const HOUR = 3_600_000
const DAY = 86_400_000

export function ftJob(id, status, extra = {}) {
  return {
    id, model: 'TinyLlama/TinyLlama-1.1B-Chat-v1.0', backend: 'trl', training_type: 'lora', training_method: 'sft',
    status, message: '', output_dir: `/data/finetune/${id}`, created_at: iso(-20 * MIN),
    config: { model: 'TinyLlama/TinyLlama-1.1B-Chat-v1.0', backend: 'trl', training_type: 'lora', training_method: 'sft', dataset_source: 'tatsu-lab/alpaca', num_epochs: 3, batch_size: 2, learning_rate: 0.0002, gradient_checkpointing: false, max_seq_length: 2048 },
    ...extra,
  }
}

export function qzJob(id, status, extra = {}) {
  return {
    id, model: 'meta-llama/Llama-3.2-1B', backend: 'llama-cpp-quantization', quantization_type: 'q4_k_m',
    status, message: '', output_dir: `/data/quant/${id}`, created_at: iso(-12 * MIN), ...extra,
  }
}

// Progress events for a fine-tuning run, one per ten steps, with an eval loss
// every fifty.
export function ftEvents(upTo = 540, total = 900, status = 'training') {
  const out = []
  for (let step = 10; step <= upTo; step += 10) {
    out.push({
      job_id: 'job-run', status, current_step: step, total_steps: total, current_epoch: (step / total) * 3, total_epochs: 3,
      loss: 2.4 * Math.exp(-step / 260) + 0.7 + Math.sin(step) * 0.02,
      eval_loss: step % 100 === 0 ? 2.5 * Math.exp(-step / 300) + 0.85 : 0,
      learning_rate: 0.0002 * (1 - step / (total * 1.1)), grad_norm: 0.8 + Math.cos(step / 40) * 0.2,
      eta_seconds: Math.round(((total - step) / total) * 1800), progress_percent: (step / total) * 100,
      message: step === 10 ? 'Training started' : '', extra_metrics: { tokens_per_second: 312 },
    })
  }
  return out
}

export const sse = (events) => events.map(e => `data: ${JSON.stringify(e)}\n\n`).join('')

// Answers every Fine-tune call. `state` records what the page sent.
//   state.jobs, state.backends, state.checkpoints, state.events (by job id),
//   state.started, state.stopped, state.deleted, state.exported, state.uploads
export async function mockFineTune(page, state) {
  state.started = state.started || []
  state.stopped = state.stopped || []
  state.deleted = state.deleted || []
  state.exported = state.exported || []
  await page.route('**/api/fine-tuning/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace(/^.*\/api\/fine-tuning/, '')
    const method = req.method()
    if (path === '/backends') return route.fulfill({ json: state.backends ?? FT_BACKENDS })
    if (path === '/jobs' && method === 'GET') return route.fulfill({ json: state.jobs ?? [] })
    if (path === '/jobs' && method === 'POST') {
      state.started.push(req.postDataJSON())
      if (state.startError) return route.fulfill({ status: 500, json: { error: state.startError } })
      return route.fulfill({ json: { id: 'job-new', status: 'queued', message: '' } })
    }
    if (path === '/datasets') return route.fulfill({ json: { path: '/data/datasets/upload.jsonl' } })
    const m = path.match(/^\/jobs\/([^/]+)(?:\/([a-z]+))?$/)
    if (m) {
      const [, id, action] = m
      if (action === 'progress') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse(state.events?.[id] ?? []) })
      if (action === 'checkpoints') return route.fulfill({ json: { checkpoints: state.checkpoints?.[id] ?? [] } })
      if (action === 'stop') { state.stopped.push({ id, save: url.searchParams.get('save_checkpoint') }); return route.fulfill({ json: { status: 'stopped' } }) }
      if (action === 'export') { state.exported.push(req.postDataJSON()); return route.fulfill({ json: { status: 'exporting' } }) }
      if (!action && method === 'DELETE') { state.deleted.push(id); return route.fulfill({ json: { status: 'deleted' } }) }
      if (!action) return route.fulfill({ json: (state.jobs ?? []).find(j => j.id === id) ?? {} })
    }
    return route.fulfill({ status: 404, json: { error: 'not stubbed' } })
  })
}

export async function mockQuantize(page, state) {
  state.started = state.started || []
  state.stopped = state.stopped || []
  state.deleted = state.deleted || []
  state.imported = state.imported || []
  await page.route('**/api/quantization/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace(/^.*\/api\/quantization/, '')
    const method = req.method()
    if (path === '/backends') return route.fulfill({ json: state.backends ?? QZ_BACKENDS })
    if (path === '/jobs' && method === 'GET') return route.fulfill({ json: state.jobs ?? [] })
    if (path === '/jobs' && method === 'POST') {
      state.started.push(req.postDataJSON())
      if (state.startError) return route.fulfill({ status: 500, json: { error: state.startError } })
      const created = state.created ?? qzJob('qz-new', 'queued', { model: req.postDataJSON().model, quantization_type: req.postDataJSON().quantization_type })
      state.jobs = [created, ...(state.jobs ?? [])]
      return route.fulfill({ json: { id: created.id, status: 'queued', message: '' } })
    }
    const m = path.match(/^\/jobs\/([^/]+)(?:\/([a-z]+))?$/)
    if (m) {
      const [, id, action] = m
      if (action === 'progress') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse(state.events?.[id] ?? []) })
      if (action === 'stop') { state.stopped.push(id); return route.fulfill({ json: { status: 'stopped' } }) }
      if (action === 'import') {
        state.imported.push(req.postDataJSON())
        const job = (state.jobs ?? []).find(j => j.id === id)
        if (job) Object.assign(job, { import_status: 'completed', import_model_name: req.postDataJSON().name || 'llama-3.2-1b-q4_k_m' })
        return route.fulfill({ json: { status: 'importing' } })
      }
      if (!action && method === 'DELETE') { state.deleted.push(id); state.jobs = (state.jobs ?? []).filter(j => j.id !== id); return route.fulfill({ json: { status: 'deleted' } }) }
      if (!action) return route.fulfill({ json: (state.jobs ?? []).find(j => j.id === id) ?? {} })
    }
    return route.fulfill({ status: 404, json: { error: 'not stubbed' } })
  })
}

// The machine and the feature flags. `host: null` makes /api/resources answer
// 403, which is what a user without the admin role gets.
export async function mockMachine(page, { host = GPU_HOST, features = { agents: true, mcp: true, fine_tuning: true, quantization: true, distributed: false } } = {}) {
  await page.route('**/api/features', route => route.fulfill({ json: features }))
  await page.route('**/api/resources', route => (host
    ? route.fulfill({ json: host })
    : route.fulfill({ status: 403, json: { error: 'admin only' } })))
}

export const NETWORKS = [
  { name: 'Home lab swarm', description: 'Four machines in one flat, mostly Qwen and Whisper.', token: 'ab12cdEFgh34ijKLmn56opQRst78uvWXyz90ABcd12EFgh34ijKLmn56opQRf90c', Clusters: [{ Type: 'federated', NetworkID: 'home-lab', Workers: ['w1', 'w2', 'w3', 'w4'] }], Failures: 0 },
  { name: 'Open weekend cluster', description: 'Community workers, open to anyone. Expect slow nodes.', token: '7c3eAAbbCCddEEffGGhhIIjjKKllMMnnOOppQQrrSSttUUvvWWxxYYzz00a1d4', Clusters: [{ Type: 'workers', NetworkID: 'weekend', Workers: ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i'] }], Failures: 0 },
  { name: 'Studio render pool', description: 'Image and video generation, GPU workers only.', token: 'e5a0ZZyyXXwwVVuuTTssRRqqPPooNNmmLLkkJJiiHHggFFeeDDccBBaa11339b', Clusters: [{ Type: 'workers', NetworkID: 'studio', Workers: ['x', 'y'] }], Failures: 0 },
]
