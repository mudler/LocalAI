// Fixtures for the Operate pages: Status, This machine, Runtime (Backends,
// Activity, Logs). Plain data and one function that routes every call those
// pages make, so a spec says only what is different about its case.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export const GB = 1024 ** 3

// GET /api/resources on a host with one GPU.
export function gpuHost({ usedGB = 18, totalGB = 24, diskFreeGB = 588, diskTotalGB = 1000 } = {}) {
  const total = totalGB * GB
  const used = usedGB * GB
  return {
    type: 'gpu',
    available: true,
    gpus: [{
      index: 0, name: 'NVIDIA RTX 4090', vendor: 'NVIDIA',
      total_vram: total, used_vram: used, free_vram: total - used, usage_percent: (used / total) * 100,
    }],
    aggregate: { total_memory: total, used_memory: used, free_memory: total - used, usage_percent: (used / total) * 100, gpu_count: 1 },
    ram: { total: 64 * GB, used: 31 * GB, free: 8 * GB, available: 33 * GB },
    cpu: { logical_cores: 16, usage_percent: 41, load_1: 5.2 },
    disk: { total: diskTotalGB * GB, used: (diskTotalGB - diskFreeGB) * GB, available: diskFreeGB * GB },
    storage_size: 38 * GB,
    reclaimer_enabled: false,
  }
}

// A loaded model as GET /system reports it. `rss` is in GB.
export function loadedModel(id, backend, rss, cpu) {
  return {
    id,
    backend,
    process: {
      pid: 4100 + Math.round(rss * 10),
      rss_bytes: rss * GB,
      memory_percent: (rss / 64) * 100,
      ...(cpu == null ? {} : { cpu_percent: cpu }),
      started_at: new Date(Date.now() - 2 * 3600_000).toISOString(),
    },
  }
}

export const LOADED = [
  loadedModel('qwen3-8b-instruct', 'llama-cpp', 9.2, 12.5),
  loadedModel('gemma-3-12b-it', 'llama-cpp', 3.5, 0.4),
  loadedModel('whisper-large-v3', 'whisper', 3.4, 0.1),
]

export const TRACES_QUIET = { total: 1204, errors: 0, p95_ms: 1200, window_hours: 24, buckets: [] }
export const TRACES_ERRORS = { total: 18402, errors: 37, p95_ms: 842, window_hours: 24, buckets: [] }

export const UPGRADE_LLAMA = {
  'llama-cpp': { backend_name: 'llama-cpp', installed_version: '0.9.4', available_version: '0.9.7' },
}

export const FAILED_OP = {
  id: 'gemma-3-27b', name: 'gemma-3-27b', fullName: 'localai@gemma-3-27b', jobID: 'job-gemma27',
  progress: 61, taskType: 'installation', isDeletion: false, isBackend: false, isQueued: false,
  cancellable: false, error: 'Connection reset by the registry',
}

export const RUNNING_OP = {
  id: 'sglang', name: 'sglang', fullName: 'localai@sglang', jobID: 'job-sglang',
  progress: 38, taskType: 'installation', isDeletion: false, isBackend: true, isQueued: false,
  cancellable: true, message: 'downloading', phase: 'downloading',
  currentBytes: 1.4 * GB, totalBytes: 3.8 * GB,
}

export const NODES = [
  { id: 'n1', name: 'gpu-box-1', status: 'healthy', healthy: true, node_type: 'backend', total_vram: 24 * GB, available_vram: 6 * GB, total_ram: 64 * GB, available_ram: 30 * GB },
  { id: 'n2', name: 'gpu-box-2', status: 'healthy', healthy: true, node_type: 'backend', total_vram: 24 * GB, available_vram: 12 * GB, total_ram: 64 * GB, available_ram: 40 * GB },
  { id: 'n3', name: 'gpu-box-3', status: 'healthy', healthy: true, node_type: 'backend', total_vram: 24 * GB, available_vram: 2 * GB, total_ram: 64 * GB, available_ram: 20 * GB },
  { id: 'n4', name: 'gpu-box-4', status: 'healthy', healthy: true, node_type: 'backend', total_vram: 24 * GB, available_vram: 18 * GB, total_ram: 64 * GB, available_ram: 44 * GB },
]

export function catalogBackend(name, extra = {}) {
  return {
    id: name, name, description: `${name} backend`, installed: false, version: '', processing: false,
    isMeta: false, isAlias: false, isDevelopment: false, license: 'MIT', gallery: 'localai', tags: [], urls: [], nodes: [],
    ...extra,
  }
}

export const CATALOG = [
  catalogBackend('llama-cpp', { description: 'llama.cpp inference: chat, embeddings, vision', installed: true, version: '0.9.4', isMeta: true, tags: ['chat', 'vision'], urls: ['https://github.com/ggml-org/llama.cpp'] }),
  catalogBackend('whisper', { description: 'Speech to text', installed: true, version: '1.7.2', tags: ['transcript'] }),
  catalogBackend('kokoro', { description: 'Text to speech', installed: true, version: '0.3.1', tags: ['tts'] }),
  catalogBackend('vllm', { description: 'GPU serving with high throughput', isMeta: true, tags: ['chat'] }),
  catalogBackend('parakeet-cpp', { description: 'Fast speech recognition with speaker ids', tags: ['transcript'] }),
  catalogBackend('diffusers', { description: 'Image and video with PyTorch', tags: ['image', 'video'] }),
]

export function installedBackend(name, version, extra = {}) {
  return { Name: name, IsSystem: false, IsMeta: false, Metadata: { version, installed_at: '2026-09-02T09:12:00Z', uri: `oci://quay.io/go-skynet/local-ai-backends:${version}-${name}`, digest: 'sha256:9c1e04b7d2' }, ...extra }
}

export const INSTALLED = [
  installedBackend('llama-cpp', '0.9.4'),
  installedBackend('whisper', '1.7.2'),
  installedBackend('kokoro', '0.3.1'),
]

export const MODEL_CAPS = {
  data: [
    { id: 'qwen3-8b-instruct', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'gemma-3-12b-it', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'whisper-large-v3', backend: 'whisper', capabilities: ['FLAG_TRANSCRIPT'] },
  ],
}

export const LOG_LINES = [
  { timestamp: '2026-10-08T14:28:01Z', stream: 'stdout', text: 'llama-cpp gRPC server listening on 127.0.0.1:41871' },
  { timestamp: '2026-10-08T14:28:01Z', stream: 'stdout', text: 'loading model qwen3-8b-instruct' },
  { timestamp: '2026-10-08T14:28:02Z', stream: 'stdout', text: 'backend ready, pid 4118' },
  { timestamp: '2026-10-08T14:31:52Z', stream: 'stderr', text: 'load model: out of memory (needs 12.4 GB, 5.6 GB free)' },
  { timestamp: '2026-10-08T14:31:53Z', stream: 'stdout', text: 'backend is not running. The next request tries the load again.' },
]

// Route every call the Operate pages make. Later routes win, so a spec can call
// this first and override one endpoint after.
//
//   distributed  features.distributed and the node list
//   nodes        the cluster API's list
//   resources    GET /api/resources (null answers 503)
//   loaded       GET /system loaded_models
//   upgrades     GET /api/backends/upgrades
//   operations   GET /api/operations
//   history      GET /api/operations/history
//   traces       GET /api/traces/summary
//   catalog      GET /api/backends
//   installed    GET /backends
//   models       GET /api/models/capabilities
//   delayMs      holds every answer, for a loading state
export async function mockOperate(page, {
  distributed = false,
  nodes = NODES,
  resources = gpuHost(),
  loaded = LOADED,
  upgrades = {},
  operations = [],
  history = [],
  traces = TRACES_QUIET,
  catalog = CATALOG,
  installed = INSTALLED,
  models = MODEL_CAPS,
  delayMs = 0,
} = {}) {
  const answer = async (route, body, status = 200) => {
    if (delayMs) await new Promise(resolve => setTimeout(resolve, delayMs))
    return route.fulfill({ status, json: body })
  }
  // First, so the more specific /api/backends/... routes below win over it.
  await page.route('**/api/backends?*', route => answer(route, { backends: catalog, preferDevelopmentBackends: false }))
  await page.route('**/api/features', route => route.fulfill({ json: { distributed, agents: true, mcp: true } }))
  await page.route('**/api/nodes', route => (distributed
    ? answer(route, nodes)
    : route.fulfill({ status: 404, json: { message: 'Not Found' } })))
  await page.route('**/api/resources', route => (resources
    ? answer(route, resources)
    : answer(route, { error: 'resource monitor disabled' }, 503)))
  await page.route('**/system', route => answer(route, { backends: [], loaded_models: loaded }))
  await page.route('**/api/backends/upgrades', route => answer(route, upgrades))
  await page.route('**/api/operations', route => answer(route, { operations }))
  await page.route('**/api/operations/history', route => answer(route, { operations: history }))
  await page.route('**/api/traces/summary', route => answer(route, traces))
  await page.route('**/backends', route => {
    if (new URL(route.request().url()).pathname === '/backends') return answer(route, installed)
    return route.continue()
  })
  await page.route('**/api/models/capabilities', route => answer(route, models))
  await page.route('**/api/backend-logs', route => answer(route, ['qwen3-8b-instruct', 'whisper-large-v3']))
}
