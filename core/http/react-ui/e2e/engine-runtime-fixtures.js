// Fixtures for the Runtime tuning section of the model editor: model files,
// the editor endpoints, and the /api/engines routes. The llama.cpp engine
// list, schema and parse answers are saved outputs of the real handlers
// (pkg/enginesettings), so the mocked specs see the shapes the server sends.
// TOY_ENGINE is a second engine with a table of its own and no capability, to
// show the section draws any engine from its schema.
import { readFileSync } from 'node:fs'

const json = (name) => JSON.parse(readFileSync(new URL(`./fixtures/${name}`, import.meta.url), 'utf8'))

export const SCHEMA = json('engine-llamacpp-schema.json')
export const PARSE_INKLING = json('engine-llamacpp-parse-inkling.json')
export const PARSE_MIXED = json('engine-llamacpp-parse-mixed.json')

// The summary of GET /api/engines for each engine.
const info = (s) => ({ id: s.id, name: s.name, binary: s.binary, backends: s.backends, capabilities: s.capabilities })
export const LLAMA_ENGINE = info(SCHEMA)

export const TOY_SCHEMA = {
  id: 'toy-engine',
  name: 'Toy Engine',
  backends: { families: ['toy'] },
  capabilities: [],
  groups: ['limits', 'runtime'],
  flags: [
    { key: 'max_model_len', target: 'config', path: 'context_size', type: 'int', min: 1, default: 'from the model', doc: 'Longest sequence in tokens.', group: 'limits' },
    { key: 'memory_fraction', target: 'option', path: 'options', option: 'gpu_memory_utilization', type: 'float', min: 0, max: 1, default: '0.9', doc: 'Share of GPU memory the engine may use.', group: 'limits' },
    { key: 'eager', target: 'option', path: 'options', option: 'enforce_eager', type: 'bool', default: 'false', doc: 'Run without graph capture.', group: 'runtime' },
  ],
  combined: [],
  presets: [],
  reserved_options: [],
}
export const TOY_ENGINE = info(TOY_SCHEMA)

export const INKLING_COMMAND = 'llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf --jinja --ctx-size 18000 --parallel 1 --cache-type-k q8_0 --cache-type-v q8_0 --temp 1.0 --top-p 1.0 --top-k 0 --min-p 0.0 -ub 64 -ngl 99 -fit off -fa on -t 6 -tb 12 --no-op-offload --load-mode none -ot "blk\\.([0-6]|1[89]|2[0-3])\\.ffn_.*_exps.*=CPU"'
export const MIXED_COMMAND = 'llama-server -m /models/x.gguf -c 4096 --host 0.0.0.0 --port 8080 --rope-freq-base 10000 --no-warmup -t abc'

export const ALL_EXPERTS = '-ot:\\.ffn_.*_exps.*=CPU'
export const INKLING_OT = PARSE_INKLING.options.find(o => o.startsWith('-ot:'))

export const MODEL = 'moe-lab'

export const CONFIGS = {
  [MODEL]: 'name: moe-lab\nbackend: llama-cpp\nparameters:\n  model: moe-lab-Q4_K_M.gguf\ncontext_size: 8192\noptions:\n  - context_shift:true\n',
  'moe-ot': `name: moe-ot\nbackend: llama-cpp\nparameters:\n  model: moe-ot.gguf\noptions:\n  - '${INKLING_OT}'\n  - 'override_tensor:token_embd=CPU'\n`,
  'vllm-model': 'name: vllm-model\nbackend: vllm\nparameters:\n  model: org/model\n',
  'toy-model': 'name: toy-model\nbackend: cuda12-toy\nparameters:\n  model: org/toy\noptions:\n  - enforce_eager:true\n  - seed:3\n',
  'no-backend': 'name: no-backend\nparameters:\n  model: plain.gguf\n',
}

export const METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'llm', label: 'LLM', icon: 'cpu', order: 10 },
  ],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string', section: 'general', label: 'Model Name', description: 'Unique identifier', component: 'input', order: 0 },
    { path: 'backend', yaml_key: 'backend', go_type: 'string', ui_type: 'string', section: 'general', label: 'Backend', description: 'Inference backend', component: 'input', order: 10 },
    { path: 'context_size', yaml_key: 'context_size', go_type: '*int', ui_type: 'int', section: 'llm', label: 'Context Size', description: 'Context window', component: 'number', order: 10 },
  ],
}

// A stand-in for the preview handler: the flags of the keys it gets, so a
// spec can see the line follow the form. The real line comes from the server.
export function previewOf(config) {
  const parts = ['llama-server']
  if (config.parameters?.model) parts.push('-m', config.parameters.model)
  if (config.context_size !== undefined) parts.push('--ctx-size', String(config.context_size))
  if (config.gpu_layers !== undefined) parts.push('-ngl', String(config.gpu_layers))
  if (config.threads !== undefined) parts.push('-t', String(config.threads))
  if (config.flash_attention !== undefined) parts.push('-fa', config.flash_attention)
  if (config.cache_type_k) parts.push('-ctk', config.cache_type_k)
  if (config.parameters?.temperature !== undefined) parts.push('--temp', String(config.parameters.temperature))
  for (const o of config.options || []) {
    if (o.startsWith('-ot:')) parts.push('-ot', `"${o.slice(4)}"`)
  }
  return parts.join(' ')
}

// The engine a mocked server names for a backend, as the real rules do for
// these fixtures: llama.cpp for its family and the empty backend, the toy
// engine for its own family.
function engineFor(backend) {
  const b = String(backend ?? '').trim().toLowerCase().replace(/\./g, '-')
  if (b === '' || b === 'llama-cpp' || b.endsWith('-llama-cpp')) return LLAMA_ENGINE
  if (b === 'toy' || b.endsWith('-toy')) return TOY_ENGINE
  return null
}

// Stubs the editor endpoints and the /api/engines routes.
//
//   schema    'ok', 'error' (500) or 'slow' (answers after slowMs)
//   preview   'ok' or 'error'
//   engines   false leaves the /api/engines routes to the real server
export async function mockRuntime(page, options = {}) {
  const { configs = CONFIGS, schema = 'ok', preview = 'ok', engines = true, slowMs = 1500 } = options
  const state = { patches: [], edits: [], previews: [], parses: [], placements: [], toyCalls: [], configs: { ...configs }, schema }

  await page.route('**/api/models/config-metadata*', route => route.fulfill({ json: METADATA }))
  await page.route('**/api/models/config-metadata/autocomplete/*', route => route.fulfill({ json: { values: [] } }))
  await page.route('**/api/models/vram-estimate', route => route.fulfill({ json: { message: 'no weight files found for estimation' } }))
  await page.route('**/api/models/edit/*', route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    return state.configs[name] !== undefined
      ? route.fulfill({ json: { config: state.configs[name], name } })
      : route.fulfill({ status: 404, json: { error: 'not found' } })
  })
  await page.route('**/models/edit/*', route => {
    if (route.request().method() !== 'POST') return route.fallback()
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    const body = route.request().postData()
    state.edits.push({ name, body })
    state.configs[name] = body
    return route.fulfill({ json: { success: true } })
  })
  await page.route('**/api/models/config-json/*', route => {
    if (route.request().method() !== 'PATCH') return route.fulfill({ json: {} })
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    state.patches.push({ name, patch: route.request().postDataJSON() })
    return route.fulfill({ json: { success: true } })
  })

  if (!engines) return state

  await page.route(url => url.pathname === '/api/engines', route => {
    const backend = new URL(route.request().url()).searchParams.get('backend')
    const engine = engineFor(backend)
    return route.fulfill({ json: { engines: engine ? [engine] : [] } })
  })
  await page.route('**/api/engines/toy-engine/**', route => {
    const url = new URL(route.request().url()).pathname
    if (url.endsWith('/settings-schema')) return route.fulfill({ json: TOY_SCHEMA })
    // The toy engine has no capability; the section must not ask for more.
    state.toyCalls.push(url)
    return route.fulfill({ status: 400, json: { error: { message: 'the engine toy-engine does not support this' } } })
  })
  await page.route('**/api/engines/llama-cpp/settings-schema', async route => {
    if (state.schema === 'slow') await new Promise(r => setTimeout(r, slowMs))
    if (state.schema === 'error') return route.fulfill({ status: 500, json: { error: { message: 'settings table unavailable' } } })
    return route.fulfill({ json: SCHEMA })
  })
  await page.route('**/api/engines/llama-cpp/command-preview', route => {
    const body = route.request().postDataJSON()
    state.previews.push(body.config)
    if (preview === 'error') return route.fulfill({ status: 500, json: { error: { message: 'preview failed' } } })
    return route.fulfill({ json: { command: previewOf(body.config), warnings: [] } })
  })
  await page.route('**/api/engines/llama-cpp/parse-command', route => {
    const { command } = route.request().postDataJSON()
    state.parses.push(command)
    if (command.includes('unclosed "')) return route.fulfill({ status: 400, json: { error: { message: 'unclosed quote in the command' } } })
    return route.fulfill({ json: command.includes('Inkling') ? PARSE_INKLING : PARSE_MIXED })
  })
  await page.route('**/api/engines/llama-cpp/placement', route => {
    const body = route.request().postDataJSON()
    state.placements.push(body)
    if (body.option !== undefined) {
      if (body.option === INKLING_OT) return route.fulfill({ json: { option: body.option, placement: PARSE_INKLING.placements[0].placement, raw: false } })
      if (body.option === ALL_EXPERTS) return route.fulfill({ json: { option: body.option, placement: { mode: 'experts_cpu_all' }, raw: false } })
      return route.fulfill({ json: { option: body.option, placement: null, raw: true } })
    }
    const p = body.placement
    if (p.mode === 'experts_cpu_all') return route.fulfill({ json: { option: ALL_EXPERTS, placement: p, raw: false } })
    if (p.mode === 'experts_cpu_layers') {
      const same = JSON.stringify(p.layers) === JSON.stringify(PARSE_INKLING.placements[0].placement.layers)
      const option = same ? INKLING_OT : `-ot:blk\\.(${p.layers.map(r => `${r.from}-${r.to}`).join('|')})\\.ffn_.*_exps.*=CPU`
      return route.fulfill({ json: { option, placement: p, raw: false } })
    }
    if (p.first_gpu_layers > 999) return route.fulfill({ status: 400, json: { error: { message: 'first_gpu_layers must be between 0 and 999' } } })
    return route.fulfill({ json: { option: `-ot:blk\\.(from${p.first_gpu_layers})\\.ffn_.*_exps.*=CPU`, placement: p, raw: false } })
  })
  return state
}
