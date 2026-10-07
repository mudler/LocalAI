// Shared fixtures for the Models ledger specs: a believable gallery of 40+
// models, three machine profiles and every endpoint the Explore, Installed and
// cleanup views read, stubbed with Playwright route fulfilment.
//
// The listing stub does what the server does with the parameters the page
// sends (term, tag, backend, collapse_variants, sort, paging), so facet counts,
// search and pagination behave like the real thing without a real gallery.

export const GB = 1024 * 1024 * 1024

const CONTEXTS = [8192, 16384, 32768, 65536, 131072, 262144]

// bytes per weight for the quantisations the gallery uses
const BPW = { Q8_0: 1.06, Q4_K_M: 0.6, Q5_K_M: 0.7, IQ2_M: 0.33, F16: 2, MXFP4: 0.53 }

// name, backend, license, facets, params in billions, quantisation, extras
const ROWS = [
  ['qwen3-4b-instruct', 'llama-cpp', 'apache-2.0', ['chat'], 4, 'Q8_0'],
  ['qwen3-8b-instruct', 'llama-cpp', 'apache-2.0', ['chat'], 8, 'Q8_0'],
  ['qwen3-14b-instruct', 'llama-cpp', 'apache-2.0', ['chat'], 14, 'Q8_0', { variants: ['qwen3-14b-instruct', 'qwen3-14b-instruct-q4'], auto: 'qwen3-14b-instruct' }],
  ['qwen3-14b-instruct-q4', 'llama-cpp', 'apache-2.0', ['chat'], 14, 'Q4_K_M', { variantOf: 'qwen3-14b-instruct' }],
  ['qwen3-30b-a3b', 'llama-cpp', 'apache-2.0', ['chat'], 30, 'Q4_K_M'],
  ['qwen3-32b-instruct', 'llama-cpp', 'apache-2.0', ['chat'], 32, 'Q4_K_M'],
  ['qwen3-coder-30b-a3b', 'llama-cpp', 'apache-2.0', ['chat'], 30, 'Q4_K_M'],
  ['gemma-3-12b-it', 'llama-cpp', 'gemma', ['chat', 'vision', 'multimodal'], 12, 'Q8_0'],
  ['gemma-3-27b-it', 'llama-cpp', 'gemma', ['chat', 'vision', 'multimodal'], 27, 'Q4_K_M'],
  ['llama-3.3-70b-instruct-iq2', 'llama-cpp', 'llama3.3', ['chat'], 70, 'IQ2_M'],
  ['mistral-small-3.2-24b', 'llama-cpp', 'apache-2.0', ['chat'], 24, 'Q4_K_M'],
  ['phi-4-14b', 'llama-cpp', 'mit', ['chat'], 14, 'Q8_0'],
  ['phi-4-mini-3.8b', 'llama-cpp', 'mit', ['chat'], 3.8, 'Q8_0'],
  ['olmo-2-13b', 'llama-cpp', 'apache-2.0', ['chat'], 13, 'Q8_0'],
  ['smollm3-3b', 'llama-cpp', 'apache-2.0', ['chat'], 3, 'Q8_0'],
  ['gpt-oss-20b', 'llama-cpp', 'apache-2.0', ['chat'], 20, 'MXFP4'],
  ['gpt-oss-120b', 'llama-cpp', 'apache-2.0', ['chat'], 120, 'MXFP4'],
  ['deepseek-r1-distill-llama-8b', 'llama-cpp', 'mit', ['chat'], 8, 'Q8_0'],
  ['deepseek-r1-distill-qwen-32b', 'llama-cpp', 'mit', ['chat'], 32, 'Q4_K_M'],
  ['devstral-24b', 'llama-cpp', 'apache-2.0', ['chat'], 24, 'Q5_K_M'],
  ['command-r-32b', 'llama-cpp', 'cc-by-nc-4.0', ['chat'], 32, 'Q4_K_M'],
  ['glm-4-9b-chat', 'llama-cpp', 'glm-4', ['chat'], 9, 'Q8_0'],
  ['granite-3.3-8b', 'llama-cpp', 'apache-2.0', ['chat'], 8, 'Q8_0'],
  ['qwen2.5-vl-7b-instruct', 'llama-cpp', 'apache-2.0', ['chat', 'vision', 'multimodal'], 7, 'Q8_0'],
  ['minicpm-v-4', 'llama-cpp', 'apache-2.0', ['chat', 'vision', 'multimodal'], 4, 'Q8_0'],
  ['bge-m3', 'llama-cpp', 'mit', ['embeddings'], 0.57, 'F16', { kv: false }],
  ['nomic-embed-text-v1.5', 'llama-cpp', 'apache-2.0', ['embeddings'], 0.14, 'F16', { kv: false }],
  ['qwen3-embedding-4b', 'llama-cpp', 'apache-2.0', ['embeddings'], 4, 'Q8_0', { kv: false }],
  ['bge-reranker-v2-m3', 'llama-cpp', 'apache-2.0', ['rerank'], 0.57, 'Q8_0', { kv: false }],
  ['whisper-large-v3', 'whisper', 'mit', ['transcript'], 1.55, 'F16', { kv: false }],
  ['whisper-medium', 'whisper', 'mit', ['transcript'], 0.77, 'F16', { kv: false }],
  ['parakeet-tdt-0.6b', 'parakeet', 'cc-by-4.0', ['transcript'], 0.6, 'F16', { kv: false }],
  ['kokoro-82m', 'kokoro', 'apache-2.0', ['tts'], 0.082, 'F16', { kv: false }],
  ['piper-en-us-lessac', 'piper', 'mit', ['tts'], 0.06, 'F16', { kv: false }],
  ['vibevoice-1.5b', 'vibevoice', 'mit', ['tts'], 1.5, 'F16', { kv: false }],
  ['silero-vad', 'silero', 'mit', ['vad'], 0.002, 'F16', { kv: false }],
  ['pyannote-diarization-3.1', 'pyannote', 'mit', ['diarization'], 0.03, 'F16', { kv: false }],
  ['flux.1-schnell', 'stablediffusion-ggml', 'apache-2.0', ['image'], 12, 'Q8_0', { kv: false }],
  ['sdxl-turbo', 'diffusers', 'openrail++', ['image'], 3.5, 'F16', { kv: false }],
  ['stable-diffusion-3.5-medium', 'diffusers', 'stability', ['image'], 2.5, 'F16', { kv: false }],
  ['wan2.2-5b', 'diffusers', 'apache-2.0', ['video'], 5, 'Q8_0', { kv: false }],
  ['depth-anything-v2', 'depth-anything', 'apache-2.0', ['detection'], 0.3, 'F16', { kv: false }],
]

const DESCRIPTIONS = {
  'qwen3-32b-instruct': 'Dense 32B chat model with a thinking mode and strong tool use.',
  'qwen3-14b-instruct': 'General chat model, 14B parameters, strong at tool use.',
  'bge-m3': 'Multilingual embeddings for search and retrieval.',
  'whisper-large-v3': 'Speech to text in 99 languages.',
  'kokoro-82m': 'Small, fast text to speech with several voices.',
  'flux.1-schnell': 'Fast image generation in four steps.',
}

export function buildGallery() {
  return ROWS.map(([name, backend, license, facets, params, quant, extra = {}]) => {
    const size = params * (BPW[quant] || 1) * GB
    const hasKv = extra.kv !== false
    const kvPer1k = hasKv ? 0.02 * Math.sqrt(params) * GB : 0
    const estimates = {}
    for (const ctx of CONTEXTS) {
      const vram = size * (hasKv ? 1.05 : 1.2) + (hasKv ? 0.4 * GB : 0.2 * GB) + (kvPer1k * ctx) / 1024
      estimates[ctx] = { vramBytes: Math.round(vram), vramDisplay: `${(vram / GB).toFixed(2)} GB` }
    }
    return {
      name,
      backend,
      license,
      facets,
      quant,
      variantOf: extra.variantOf || null,
      variants: extra.variants || null,
      auto: extra.auto || null,
      description: DESCRIPTIONS[name] || `${name} from the gallery.`,
      tags: [...facets, backend],
      urls: [`https://huggingface.co/example/${name}`],
      estimate: {
        sizeBytes: Math.round(size),
        sizeDisplay: `${(size / GB).toFixed(2)} GB`,
        estimates,
        ...(hasKv ? { modelMaxContext: 131072 } : {}),
      },
    }
  })
}

export const GALLERY = buildGallery()

// ---- machine profiles (what GET /api/resources returns) ----

const DISK = { total: 931 * GB, used: 760 * GB, available: 171 * GB }

export const PROFILES = {
  // One 24 GB card, 48 GB of free RAM.
  gpu24: {
    type: 'gpu',
    available: true,
    gpus: [{ index: 0, name: 'RTX 4090', vendor: 'nvidia', total_vram: 24 * GB, used_vram: 2.6 * GB, free_vram: 21.4 * GB, usage_percent: 10.8 }],
    ram: { total: 64 * GB, used: 16 * GB, free: 48 * GB, available: 48 * GB, usage_percent: 25 },
    aggregate: { total_memory: 24 * GB, used_memory: 2.6 * GB, free_memory: 21.4 * GB, usage_percent: 10.8, gpu_count: 1 },
    disk: DISK,
    storage_size: 107.6 * GB,
  },
  // An 8 GB laptop: 6 GB of GPU memory and 10 GB of RAM to spill into.
  laptop: {
    type: 'gpu',
    available: true,
    gpus: [{ index: 0, name: 'RTX 4050 Laptop', vendor: 'nvidia', total_vram: 6 * GB, used_vram: 0.6 * GB, free_vram: 5.4 * GB, usage_percent: 10 }],
    ram: { total: 16 * GB, used: 6 * GB, free: 10 * GB, available: 10 * GB, usage_percent: 37 },
    aggregate: { total_memory: 6 * GB, used_memory: 0.6 * GB, free_memory: 5.4 * GB, usage_percent: 10, gpu_count: 1 },
    disk: { total: 238 * GB, used: 200 * GB, available: 38 * GB },
    storage_size: 41 * GB,
  },
  // No GPU: system RAM is the budget.
  nogpu: {
    type: 'ram',
    available: true,
    gpus: [],
    ram: { total: 32 * GB, used: 9 * GB, free: 23 * GB, available: 23 * GB, usage_percent: 28 },
    aggregate: { total_memory: 32 * GB, used_memory: 9 * GB, free_memory: 23 * GB, usage_percent: 28, gpu_count: 0 },
    disk: { total: 500 * GB, used: 404 * GB, available: 96 * GB },
    storage_size: 52 * GB,
  },
}

// A models disk under the low-disk rule (under 10 percent free, or under 20 GB).
export function withDisk(profile, disk, storage) {
  return { ...profile, disk, storage_size: storage ?? profile.storage_size }
}

// ---- installed models (what the capabilities and system endpoints return) ----

export function installedSeed() {
  return [
    { id: 'bge-m3', backend: 'llama-cpp', capabilities: ['FLAG_EMBEDDINGS'] },
    { id: 'flux.1-schnell', backend: 'stablediffusion-ggml', capabilities: ['FLAG_IMAGE'] },
    { id: 'gemma-3-12b-it', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'kokoro-82m', backend: 'kokoro', capabilities: ['FLAG_TTS'] },
    { id: 'llama-3.3-70b-instruct-iq2', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'mistral-small-3.2-24b', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'my-finetune-q4', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'qwen3-14b-instruct', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'qwen3-14b-instruct-q4', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'qwen3-8b-instruct', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'], pinned: true },
    { id: 'sdxl-turbo', backend: 'diffusers', capabilities: ['FLAG_IMAGE'] },
    { id: 'whisper-large-v3', backend: 'whisper', capabilities: ['FLAG_TRANSCRIPT'] },
    { id: 'whisper-medium', backend: 'whisper', capabilities: ['FLAG_TRANSCRIPT'], disabled: true },
  ]
}

// Facet key to the gallery entries it matches, as the server decides it.
const matchesFacet = (entry, key) => (key === 'multimodal' ? entry.facets.includes('multimodal') : entry.facets.includes(key))

// Stub every endpoint the Models page reads. Returns a handle the spec can
// read and change: what was installed or deleted, and the live lists.
export async function mockLedger(page, options = {}) {
  const {
    profile = 'gpu24',
    resources,
    gallery = GALLERY,
    installed = installedSeed(),
    loaded = ['qwen3-8b-instruct', 'whisper-large-v3'],
    agents = { 'research-helper': { model: 'qwen3-14b-instruct' } },
    tasks = [{ id: 't1', name: 'nightly-digest', model: 'gemma-3-12b-it', cron: '0 6 * * *', enabled: true }],
    agentsStatus = 200,
    chains = [],
    aliases = [{ name: 'default-chat', target: 'qwen3-8b-instruct' }],
    listingDelay = 0,
    listingStatus = 200,
    operations = [],
    failResources = false,
    deleteFails = [],
  } = options

  const state = {
    installed: [...installed],
    loaded: [...loaded],
    operations: [...operations],
    installs: [],
    deletes: [],
    listingRequests: [],
    resources: resources || PROFILES[profile],
    // Changeable while a spec runs, to stand for the server changing under the
    // page: a gallery that goes away, an agent that starts using a model.
    listingStatus,
    agents: { ...agents },
    tasks: [...tasks],
    agentsStatus,
    dismissed: [],
  }
  const installedIds = () => new Set(state.installed.map(m => m.id))

  const entryJSON = (entry) => ({
    id: entry.name,
    name: entry.name,
    description: entry.description,
    license: entry.license,
    urls: entry.urls,
    tags: entry.tags,
    gallery: 'localai',
    installed: installedIds().has(entry.name),
    processing: false,
    backend: entry.backend,
    additionalFiles: [{ filename: `${entry.name}.gguf`, uri: `https://huggingface.co/example/${entry.name}/resolve/main/${entry.name}.gguf`, sha256: 'a'.repeat(64) }],
    ...(entry.variants ? { has_variants: true } : {}),
  })

  await page.route('**/api/models?*', async route => {
    const url = new URL(route.request().url())
    state.listingRequests.push(url.search)
    if (listingDelay) await new Promise(r => setTimeout(r, listingDelay))
    if (state.listingStatus !== 200) {
      return route.fulfill({ status: state.listingStatus, contentType: 'application/json', body: JSON.stringify({ error: 'gallery unreachable' }) })
    }
    const q = url.searchParams
    const collapse = q.get('collapse_variants') === 'true'
    let list = gallery.filter(e => !(collapse && e.variantOf))
    const term = (q.get('term') || '').toLowerCase()
    if (term) list = list.filter(e => e.name.includes(term) || e.description.toLowerCase().includes(term) || e.backend.includes(term))
    const tag = q.get('tag')
    if (tag) {
      const keys = tag.split(',').filter(Boolean)
      list = list.filter(e => keys.some(key => matchesFacet(e, key)))
    }
    const backend = q.get('backend')
    if (backend) list = list.filter(e => e.backend === backend)
    if (q.get('sort') === 'name') list = [...list].sort((a, b) => a.name.localeCompare(b.name) * (q.get('order') === 'desc' ? -1 : 1))
    if (q.get('sort') === 'status') list = [...list].sort((a, b) => (Number(installedIds().has(b.name)) - Number(installedIds().has(a.name))))
    const items = Number(q.get('items') || 9)
    const page_ = Number(q.get('page') || 1)
    const total = list.length
    const paged = list.slice((page_ - 1) * items, page_ * items)
    return route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        models: paged.map(entryJSON),
        allBackends: [...new Set(gallery.map(e => e.backend))].sort(),
        allTags: [],
        availableModels: total,
        installedModels: state.installed.length,
        totalPages: Math.max(1, Math.ceil(total / items)),
        currentPage: page_,
      }),
    })
  })
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: state.installed } }))
  await page.route('**/api/models/estimate/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    const entry = gallery.find(e => e.name === name)
    return entry ? route.fulfill({ json: entry.estimate }) : route.fulfill({ status: 404, json: { error: 'model not found' } })
  })
  await page.route('**/api/models/variants/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    const entry = gallery.find(e => e.name === name)
    if (!entry?.variants) return route.fulfill({ json: {} })
    return route.fulfill({
      json: {
        auto_selected: entry.auto,
        variants: entry.variants.map(model => {
          const v = gallery.find(e => e.name === model)
          return { model, backend: v.backend, quantization: v.quant, memory_bytes: v.estimate.sizeBytes, fits: true, is_base: model === name }
        }),
      },
    })
  })
  await page.route('**/api/backends/usecases', route => route.fulfill({
    json: {
      'llama-cpp': ['chat', 'embeddings', 'vision', 'rerank', 'token_classify'],
      whisper: ['transcript'],
      kokoro: ['tts'],
      diffusers: ['image', 'video'],
    },
  }))
  await page.route('**/api/resources', route => (failResources
    ? route.fulfill({ status: 500, json: { error: 'unavailable' } })
    : route.fulfill({ json: state.resources })))
  await page.route('**/system', route => route.fulfill({ json: { loaded_models: state.loaded.map(id => ({ id })) } }))
  await page.route('**/api/aliases', route => route.fulfill({ json: aliases }))
  await page.route('**/api/nodes', route => route.fulfill({ status: 404, json: { error: 'not distributed' } }))
  await page.route('**/api/operations', route => route.fulfill({ json: { operations: state.operations } }))
  await page.route('**/api/failover', route => route.fulfill({ json: { chains } }))
  await page.route('**/api/failover/events', route => route.abort())
  await page.route('**/api/agents*', route => (state.agentsStatus !== 200
    ? route.fulfill({ status: state.agentsStatus, json: { error: 'agent pool unavailable' } })
    : route.fulfill({ json: { agents: Object.keys(state.agents), agentCount: Object.keys(state.agents).length } })))
  await page.route('**/api/agents/*/config', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').slice(-2)[0])
    return route.fulfill({ json: state.agents[name] || {} })
  })
  await page.route('**/api/agent/tasks*', route => route.fulfill({ json: state.tasks }))
  await page.route('**/api/operations/*/dismiss', route => {
    state.dismissed.push(new URL(route.request().url()).pathname.split('/').slice(-2)[0])
    state.operations = state.operations.filter(op => op.jobID !== state.dismissed[state.dismissed.length - 1])
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/models/install/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    state.installs.push({ name, search: new URL(route.request().url()).search })
    return route.fulfill({ json: { jobID: `job-${state.installs.length}`, message: 'Installation started' } })
  })
  await page.route('**/models/delete/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    if (deleteFails.includes(name)) {
      return route.fulfill({ status: 500, json: { error: { message: 'model is busy' } } })
    }
    state.deletes.push(name)
    state.installed = state.installed.filter(m => m.id !== name)
    state.loaded = state.loaded.filter(id => id !== name)
    return route.fulfill({ json: {} })
  })
  await page.route('**/models/toggle-state/**', route => route.fulfill({ json: {} }))
  await page.route('**/models/toggle-pinned/**', route => route.fulfill({ json: {} }))
  await page.route('**/api/models/job/*', route => route.fulfill({ json: { processed: true } }))
  await page.route('**/api/recommended*', route => route.fulfill({ json: { models: [] } }))
  return state
}

// ---- the model page and the Placement section ----

// Two cards: 24 GB and 12 GB, with the first partly used by another program.
PROFILES.twogpu = {
  type: 'gpu',
  available: true,
  gpus: [
    { index: 0, name: 'RTX 4090', vendor: 'nvidia', total_vram: 24 * GB, used_vram: 2.6 * GB, free_vram: 21.4 * GB, usage_percent: 10.8 },
    { index: 1, name: 'RTX 3060', vendor: 'nvidia', total_vram: 12 * GB, used_vram: 0.4 * GB, free_vram: 11.6 * GB, usage_percent: 3.3 },
  ],
  ram: { total: 64 * GB, used: 16 * GB, free: 48 * GB, available: 48 * GB, usage_percent: 25 },
  aggregate: { total_memory: 36 * GB, used_memory: 3 * GB, free_memory: 33 * GB, usage_percent: 8, gpu_count: 2 },
  disk: DISK,
  storage_size: 107.6 * GB,
}

export const EDITOR_METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'llm', label: 'LLM', icon: 'cpu', order: 10 },
  ],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string', section: 'general', label: 'Model Name', description: 'Unique identifier for this model', component: 'input', order: 0 },
    { path: 'backend', yaml_key: 'backend', go_type: 'string', ui_type: 'string', section: 'general', label: 'Backend', description: 'Inference backend to use', component: 'input', order: 10 },
    { path: 'context_size', yaml_key: 'context_size', go_type: '*int', ui_type: 'int', section: 'llm', label: 'Context Size', description: 'Maximum context window in tokens', component: 'number', vram_impact: true, order: 10 },
    { path: 'gpu_layers', yaml_key: 'gpu_layers', go_type: '*int', ui_type: 'int', section: 'llm', label: 'GPU Layers', description: 'Number of layers to offload to GPU (-1 = all)', component: 'number', vram_impact: true, order: 11 },
  ],
}

export const CONFIGS = {
  'qwen3-14b-instruct': 'name: qwen3-14b-instruct\nbackend: llama-cpp\nparameters:\n  model: qwen3-14b-instruct.gguf\ncontext_size: 8192\n',
  'qwen3-8b-instruct': 'name: qwen3-8b-instruct\nbackend: llama-cpp\nparameters:\n  model: qwen3-8b-instruct.gguf\ncontext_size: 8192\ngpu_layers: 99999999\n',
  'llama-3.3-70b-instruct-iq2': 'name: llama-3.3-70b-instruct-iq2\nbackend: llama-cpp\nparameters:\n  model: llama-3.3-70b.gguf\ncontext_size: 8192\ngpu_layers: 20\n',
  'gemma-3-12b-it': 'name: gemma-3-12b-it\nbackend: llama-cpp\nparameters:\n  model: gemma-3-12b-it.gguf\ncontext_size: 16384\ngpu_layers: 0\n',
}

// What the estimate endpoint would say for a gallery model at a context size
// and layer count, following the server's own arithmetic: the context term is
// linear, and a partial offload scales only the weights.
export function vramFor(entry, ctx, layers, layerCount = 40) {
  const e = entry.estimate.estimates
  const slope = (e[262144].vramBytes - e[8192].vramBytes) / (262144 - 8192)
  const kv = slope * ctx
  const weights = entry.estimate.sizeBytes * 1.05 + 0.4 * GB
  const share = layers != null && layers > 0 && layers < layerCount ? layers / layerCount : 1
  return Math.round(weights * share + kv)
}

// Stub the endpoints the Placement section and the model page's Configuration
// tab read and write, on top of mockLedger.
//
//   estimate    'ok', 'unavailable' (200 with only a message, as the server
//               answers for a model with no weight files), 'error' (500) or
//               'slow' (answers after a pause).
//   layerCount  when set, the estimate also reports block_count.
//   configs     the YAML each installed model's edit endpoint returns.
export async function mockPlacement(page, options = {}) {
  const { estimate = 'ok', layerCount = 0, configs = CONFIGS, gallery = GALLERY, slowMs = 1500, modelLayers = 40, metadata = EDITOR_METADATA } = options
  const state = { estimateCalls: [], patches: [], edits: [], configs: { ...configs }, estimate }
  await page.route('**/api/models/vram-estimate', async route => {
    const body = route.request().postDataJSON()
    state.estimateCalls.push(body)
    if (state.estimate === 'slow') await new Promise(r => setTimeout(r, slowMs))
    if (state.estimate === 'error') return route.fulfill({ status: 500, json: { error: 'estimate failed' } })
    const entry = gallery.find(e => e.name === body.model)
    if (state.estimate === 'unavailable' || !entry) return route.fulfill({ json: { message: 'no weight files found for estimation' } })
    const vram = vramFor(entry, body.context_size || 8192, body.gpu_layers ?? null, modelLayers)
    return route.fulfill({
      json: {
        size_bytes: entry.estimate.sizeBytes,
        size_display: entry.estimate.sizeDisplay,
        context_length: body.context_size || 8192,
        vram_bytes: vram,
        vram_display: `${(vram / GB).toFixed(2)} GB`,
        model_max_context: 131072,
        ...(layerCount ? { block_count: layerCount } : {}),
      },
    })
  })
  await page.route('**/api/models/config-metadata*', route => route.fulfill({ json: metadata }))
  await page.route('**/api/models/edit/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    if (route.request().method() === 'POST') {
      state.edits.push({ name, body: route.request().postData() })
      return route.fulfill({ json: { success: true } })
    }
    return state.configs[name] !== undefined
      ? route.fulfill({ json: { config: state.configs[name], name } })
      : route.fulfill({ status: 404, json: { error: 'not found' } })
  })
  await page.route('**/api/models/config-json/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    if (route.request().method() === 'PATCH') {
      state.patches.push({ name, patch: route.request().postDataJSON() })
      return route.fulfill({ json: { success: true } })
    }
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/models/config-metadata/autocomplete/*', route => route.fulfill({ json: { values: [] } }))
  return state
}
