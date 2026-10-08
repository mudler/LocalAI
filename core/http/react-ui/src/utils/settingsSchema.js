// What the Settings page knows about each setting.
//
// GET /api/settings returns values only: no defaults, no descriptions, no
// groups. Everything below is a client-side description of those real fields,
// and each fact in it comes from the code (cited per field) or from the page
// as it was before the rebuild.
//
//   group    the intent group the field is shown under. The groups are a
//            reorganisation of the fifteen sections the page used to have;
//            `was` keeps the old section so search still finds a setting by
//            the name people knew.
//   default  the built-in default, taken from the CLI flag defaults in
//            core/cli/run.go (and the registry in
//            core/config/runtime_settings_registry.go). A field with no
//            `default` key has no default we can ground, so the page draws no
//            "changed from default" marker for it. An environment variable or
//            CLI flag can change the value a server starts with; the marker
//            compares with the built-in default, not with the startup value.
//   apply    'live'    the save handler applies it at once
//                      (core/http/endpoints/localai/settings.go)
//             'restart' the setting is read at start (docs/content/features/
//                      runtime-settings.md, the page's own hint)
//             none      the code does not say, so the page says nothing
//   note     what applying does, only where the handler says it

export const GROUPS = [
  { id: 'memory', label: 'Memory and models', hint: 'When models stop, what gets evicted, GPU budget', icon: 'memory' },
  { id: 'speed', label: 'Speed and defaults', hint: 'Threads, context window, downloads', icon: 'gauge' },
  { id: 'backends', label: 'Backends and galleries', hint: 'Upgrades, gallery sources, start-up loading', icon: 'boxes' },
  { id: 'access', label: 'Access and security', hint: 'Cross-origin rules, CSRF, legacy API keys', icon: 'shield' },
  { id: 'debug', label: 'Debugging and traces', hint: 'Verbose logs, request traces, backend output', icon: 'bug' },
  { id: 'agents', label: 'Agents and responses', hint: 'Agent pool, job history, assistant, stored responses', icon: 'robot' },
  { id: 'swarm', label: 'Swarm and sharing', hint: 'Peer-to-peer network, scheduling checks', icon: 'network' },
  { id: 'look', label: 'Look and feel', hint: 'Instance name, tagline, logos', icon: 'palette' },
]

// The sections of the page before the rebuild, for "was Watchdog" and for
// searching by the old name. Labels are the old sidebar labels.
export const OLD_SECTIONS = {
  branding: 'Branding',
  watchdog: 'Watchdog',
  memory: 'Memory Reclaimer',
  backends: 'Backend Management',
  performance: 'Performance',
  tracing: 'Tracing',
  api: 'API & CORS',
  p2p: 'P2P Network',
  galleries: 'Galleries',
  apikeys: 'API Keys',
  agents: 'Agent Jobs',
  agentpool: 'Agent Pool',
  assistant: 'LocalAI Assistant',
  distributed: 'Distributed',
  responses: 'Open Responses',
}

const AGENT_RESTART = { apply: 'restart' }
const WATCHDOG_LIVE = { apply: 'live', note: 'The watchdog restarts with the new values.' }

// One row per real setting. `kind` picks the control:
//   bool, text, int, duration, percent, select, model, json, lines, token,
//   asset (the three branding images, saved at once by their own endpoint).
export const FIELDS = [
  // ---- Memory and models ---------------------------------------------------
  { key: 'watchdog_enabled', group: 'memory', was: 'watchdog', kind: 'bool', label: 'Watchdog', composite: true,
    desc: 'Automatically monitor and manage backend processes. Switches the idle and busy checks together.',
    default: false, ...WATCHDOG_LIVE },
  { key: 'watchdog_idle_enabled', group: 'memory', was: 'watchdog', kind: 'bool', label: 'Stop idle models',
    desc: 'Automatically stop backends that have been idle too long.',
    default: false, ...WATCHDOG_LIVE },
  { key: 'watchdog_idle_timeout', group: 'memory', was: 'watchdog', kind: 'duration', label: 'Idle timeout',
    desc: 'Time before an idle backend is stopped, for example 15m or 1h.',
    placeholder: '15m', default: '15m', ...WATCHDOG_LIVE, needs: 'watchdog_idle_enabled' },
  { key: 'watchdog_busy_enabled', group: 'memory', was: 'watchdog', kind: 'bool', label: 'Stop stuck models',
    desc: 'Stop busy processes that exceed the busy timeout.',
    default: false, ...WATCHDOG_LIVE },
  { key: 'watchdog_busy_timeout', group: 'memory', was: 'watchdog', kind: 'duration', label: 'Busy timeout',
    desc: 'Time before a busy backend is stopped, for example 5m.',
    placeholder: '5m', default: '5m', ...WATCHDOG_LIVE, needs: 'watchdog_busy_enabled' },
  { key: 'watchdog_interval', group: 'memory', was: 'watchdog', kind: 'duration', label: 'Check interval',
    desc: 'How often the watchdog checks backends, for example 2s.',
    placeholder: '500ms', default: '500ms', ...WATCHDOG_LIVE },
  { key: 'force_eviction_when_busy', group: 'memory', was: 'watchdog', kind: 'bool', label: 'Evict even when busy',
    desc: 'Allow model eviction during active API calls. This can interrupt requests in flight.',
    default: false, apply: 'live' },
  { key: 'size_aware_eviction', group: 'memory', was: 'watchdog', kind: 'bool', label: 'Evict the largest model first',
    desc: 'Evict the largest loaded model first instead of the least recently used one.',
    default: false, apply: 'live' },
  { key: 'lru_eviction_max_retries', group: 'memory', was: 'watchdog', kind: 'int', label: 'Eviction retries',
    desc: 'Maximum retries while waiting for busy models to become idle before eviction.',
    placeholder: '30', default: 30, min: 0, apply: 'live' },
  { key: 'lru_eviction_retry_interval', group: 'memory', was: 'watchdog', kind: 'duration', label: 'Eviction retry interval',
    desc: 'Wait between eviction retries, for example 1s.',
    placeholder: '1s', default: '1s', apply: 'live' },
  { key: 'memory_reclaimer_enabled', group: 'memory', was: 'memory', kind: 'bool', label: 'Free memory automatically',
    desc: 'Evict backends when memory use passes the threshold below.',
    default: false, ...WATCHDOG_LIVE },
  { key: 'memory_reclaimer_threshold', group: 'memory', was: 'memory', kind: 'percent', label: 'Memory threshold',
    desc: 'Eviction starts when usage passes this share of GPU memory (RAM on a machine without a GPU).',
    min: 50, max: 100, fallback: 0.8, default: 0.95, ...WATCHDOG_LIVE, needs: 'memory_reclaimer_enabled' },
  { key: 'max_active_backends', group: 'memory', was: 'backends', kind: 'int', label: 'Models kept loaded',
    desc: 'Maximum models to keep loaded at once. 0 means no limit, 1 keeps a single model.',
    placeholder: '0', default: 0, min: 0, ...WATCHDOG_LIVE },
  { key: 'vram_budget', group: 'memory', was: 'performance', kind: 'text', label: 'GPU memory budget',
    desc: 'Cap the VRAM used for model allocation on this node. A percentage (80%) or an amount (12GB). Empty uses all detected VRAM.',
    placeholder: 'e.g. 80% or 12GB', default: '', apply: 'live' },

  // ---- Speed and defaults --------------------------------------------------
  { key: 'threads', group: 'speed', was: 'performance', kind: 'int', label: 'Default threads',
    desc: 'CPU threads for inference. 0 means auto-detect.', placeholder: '0', min: 0 },
  { key: 'context_size', group: 'speed', was: 'performance', kind: 'int', label: 'Default context size',
    desc: 'Context window size for models that do not set their own.', placeholder: '2048', min: 0 },
  { key: 'artifact_download_concurrency', group: 'speed', was: 'performance', kind: 'int', label: 'Artifact Download Concurrency',
    desc: 'Maximum artifact files downloaded at once. 1 downloads one after the other.', default: 1, min: 1 },
  { key: 'f16', group: 'speed', was: 'performance', kind: 'bool', label: 'F16 precision',
    desc: 'Use 16-bit floating point for reduced memory use.', default: false },

  // ---- Backends and galleries ----------------------------------------------
  { key: 'auto_upgrade_backends', group: 'backends', was: 'backends', kind: 'bool', label: 'Upgrade backends automatically',
    desc: 'Upgrade backends when a new version is found.', default: false, apply: 'live' },
  { key: 'prefer_development_backends', group: 'backends', was: 'backends', kind: 'bool', label: 'Prefer development backends',
    desc: 'Show development backend versions first in the backends gallery.', default: false, apply: 'live' },
  { key: 'autoload_galleries', group: 'backends', was: 'galleries', kind: 'bool', label: 'Load and pre-warm galleries on boot',
    desc: 'Load model galleries and pre-warm their remote size and VRAM estimates when LocalAI starts.', default: true },
  { key: 'autoload_backend_galleries', group: 'backends', was: 'galleries', kind: 'bool', label: 'Autoload Backend Galleries',
    desc: 'Load backend galleries when LocalAI starts.', default: true },
  { key: 'vram_persistent_cache', group: 'backends', was: 'galleries', kind: 'bool', label: 'Persist remote VRAM estimates',
    desc: 'Reuse successful remote model metadata probes across restarts. Off when gallery autoload is off.', default: true },
  { key: 'galleries', group: 'backends', was: 'galleries', kind: 'json', label: 'Model galleries (JSON)',
    desc: 'A list of galleries: url, name and optional mirrors.', apply: 'live',
    placeholder: '[\n  { "url": "https://...", "name": "my-gallery", "mirrors": ["https://fallback/..."] }\n]' },
  { key: 'backend_galleries', group: 'backends', was: 'galleries', kind: 'json', label: 'Backend galleries (JSON)',
    desc: 'The same shape, for backends.', apply: 'live',
    placeholder: '[\n  { "url": "https://...", "name": "my-backends", "mirrors": ["https://fallback/..."] }\n]' },

  // ---- Access and security -------------------------------------------------
  { key: 'cors', group: 'access', was: 'api', kind: 'bool', label: 'Allow cross-origin requests',
    desc: 'Enable Cross-Origin Resource Sharing.', default: false },
  { key: 'cors_allow_origins', group: 'access', was: 'api', kind: 'text', label: 'Allowed origins',
    desc: 'Comma-separated list of origins that may call the API from a browser.', placeholder: '*', default: '', needs: 'cors' },
  // The wire field "csrf" carries DisableCSRF (core/config/runtime_settings_registry.go),
  // so the switch shows the inverse and writes the inverse.
  { key: 'csrf', group: 'access', was: 'api', kind: 'bool', label: 'CSRF protection', inverted: true,
    desc: 'Block forged cross-site browser requests. Requests that carry an API key are exempt.', default: true },
  { key: 'api_keys', group: 'access', was: 'apikeys', kind: 'lines', label: 'Shared API keys', sensitive: true, apply: 'live',
    desc: 'Keys that any client may use, one per line or separated by commas. Keys set in the environment stay and cannot be removed here. Per-user keys live under Users and keys.',
    placeholder: 'sk-key-1\nsk-key-2' },

  // ---- Debugging and traces ------------------------------------------------
  { key: 'debug', group: 'debug', was: 'performance', kind: 'bool', label: 'Verbose debug logging',
    desc: 'Write verbose debug logs.', default: false },
  { key: 'enable_tracing', group: 'debug', was: 'tracing', kind: 'bool', label: 'Record API traces',
    desc: 'Keep API requests, responses and backend operations for the Traces page.', default: false },
  { key: 'tracing_max_items', group: 'debug', was: 'tracing', kind: 'int', label: 'Traces kept',
    desc: 'Maximum number of trace items to keep. 0 means no limit.', placeholder: '1024', default: 1024, min: 0, needs: 'enable_tracing' },
  { key: 'tracing_max_body_bytes', group: 'debug', was: 'tracing', kind: 'int', label: 'Largest trace body (bytes)',
    desc: 'Per-field cap on captured request and response bodies and backend trace data. It keeps large chat histories or audio from locking the Traces page. 0 means no cap.',
    placeholder: '65536', default: 65536, min: 0, needs: 'enable_tracing' },
  { key: 'enable_backend_logging', group: 'debug', was: 'tracing', kind: 'bool', label: 'Enable Backend Logging',
    desc: 'Capture backend process output per model, without needing debug mode.', apply: 'live' },

  // ---- Agents and responses ------------------------------------------------
  { key: 'agent_job_retention_days', group: 'agents', was: 'agents', kind: 'int', label: 'Job history kept (days)',
    desc: 'Number of days to keep agent job history.', placeholder: '30', default: 30, min: 0, apply: 'live',
    note: 'The job service restarts.' },
  { key: 'agent_pool_enabled', group: 'agents', was: 'agentpool', kind: 'bool', label: 'Agent pool',
    desc: 'Turn the agent pool feature on or off.', default: true, ...AGENT_RESTART, fallback: true },
  { key: 'agent_pool_default_model', group: 'agents', was: 'agentpool', kind: 'model', label: 'Default agent model',
    desc: 'Default language model for agents.', placeholder: 'e.g. gpt-4', default: '', ...AGENT_RESTART },
  { key: 'agent_pool_embedding_model', group: 'agents', was: 'agentpool', kind: 'model', label: 'Embedding model',
    desc: 'Model used for knowledge base embeddings.', placeholder: 'granite-embedding-107m-multilingual',
    default: 'granite-embedding-107m-multilingual', ...AGENT_RESTART },
  { key: 'agent_pool_max_chunking_size', group: 'agents', was: 'agentpool', kind: 'int', label: 'Max Chunking Size',
    desc: 'Maximum chunk size for knowledge base documents.', default: 400, min: 0, fallback: 400, ...AGENT_RESTART },
  { key: 'agent_pool_chunk_overlap', group: 'agents', was: 'agentpool', kind: 'int', label: 'Chunk Overlap',
    desc: 'Overlap between chunks of knowledge base documents.', default: 0, min: 0, fallback: 0, ...AGENT_RESTART },
  { key: 'agent_pool_enable_logs', group: 'agents', was: 'agentpool', kind: 'bool', label: 'Agent logs',
    desc: 'Write agent logs.', default: false, fallback: false, ...AGENT_RESTART },
  { key: 'agent_pool_collection_db_path', group: 'agents', was: 'agentpool', kind: 'text', label: 'Collection DB Path',
    desc: 'Database path for agent collections. Empty uses the default.', placeholder: 'Leave empty for default', default: '', ...AGENT_RESTART },
  { key: 'agent_pool_vector_engine', group: 'agents', was: 'agentpool', kind: 'select', label: 'Vector Engine',
    desc: 'Store for collection embeddings. chromem is in memory; postgres uses pgvector and needs a database URL.',
    options: ['chromem', 'postgres'], default: 'chromem', fallback: 'chromem', ...AGENT_RESTART },
  { key: 'agent_pool_database_url', group: 'agents', was: 'agentpool', kind: 'text', label: 'Database URL', sensitive: true,
    desc: 'PostgreSQL connection string used when the vector engine is postgres.', placeholder: 'postgres://...', default: '',
    ...AGENT_RESTART, needsValue: ['agent_pool_vector_engine', 'postgres'] },
  { key: 'agent_pool_agent_hub_url', group: 'agents', was: 'agentpool', kind: 'text', label: 'Agent Hub URL',
    desc: 'Override the default agent hub address for a custom or self-hosted hub.', placeholder: 'https://agenthub.localai.io',
    default: 'https://agenthub.localai.io', ...AGENT_RESTART },
  { key: 'localai_assistant_enabled', group: 'agents', was: 'assistant', kind: 'bool', label: 'LocalAI Assistant',
    desc: 'Let admins opt chat sessions into the in-process admin tools. Turning it off refuses new requests that ask for them.',
    default: true, fallback: true, apply: 'live' },
  { key: 'open_responses_store_ttl', group: 'agents', was: 'responses', kind: 'duration', label: 'Response Store TTL', allowZero: true,
    desc: 'How long stored responses are kept, for example 1h or 30m. 0 means they never expire.', placeholder: '1h', default: '0', apply: 'live' },

  // ---- Swarm and sharing ---------------------------------------------------
  { key: 'p2p_token', group: 'swarm', was: 'p2p', kind: 'token', label: 'P2P Token', sensitive: true, apply: 'live', default: '',
    desc: 'Generate a new token or paste an existing one to join a network. An empty token stops P2P.',
    note: 'The peer-to-peer stack restarts.' },
  { key: 'p2p_network_id', group: 'swarm', was: 'p2p', kind: 'text', label: 'P2P Network ID',
    desc: 'Network identifier for grouping instances.', placeholder: 'Network ID', default: '', apply: 'live',
    note: 'The peer-to-peer stack restarts.' },
  { key: 'federated', group: 'swarm', was: 'p2p', kind: 'bool', label: 'Federated Mode',
    desc: 'Enable federated instance mode for load balancing.', default: false, apply: 'live',
    note: 'The peer-to-peer stack restarts.' },
  { key: 'distributed_disk_headroom_check', group: 'swarm', was: 'distributed', kind: 'bool', label: 'Disk headroom check',
    desc: 'Reject worker nodes that lack free space for the model, at scheduling time instead of partway through staging. Free space is measured on each worker and compared with the model size plus a small margin. Off restores selection that ignores free disk; the check still runs and warns when it would have rejected every node.',
    default: true, fallback: true, apply: 'live' },

  // ---- Look and feel -------------------------------------------------------
  { key: 'instance_name', group: 'look', was: 'branding', kind: 'text', label: 'Instance Name',
    desc: 'Replaces "LocalAI" in the sidebar, footer and browser tab. Shown on the sign-in screen.', placeholder: 'LocalAI' },
  { key: 'instance_tagline', group: 'look', was: 'branding', kind: 'text', label: 'Tagline',
    desc: 'Optional short subtitle under the instance name.', placeholder: '(none)' },
  { key: 'asset:logo', group: 'look', was: 'branding', kind: 'asset', asset: 'logo', label: 'Square Logo', immediate: true,
    desc: 'Used as the icon-sized logo in the sidebar and on small screens.' },
  { key: 'asset:logo_horizontal', group: 'look', was: 'branding', kind: 'asset', asset: 'logo_horizontal', label: 'Horizontal Logo', immediate: true,
    desc: 'Wide logo shown in the sidebar header on desktop.' },
  { key: 'asset:favicon', group: 'look', was: 'branding', kind: 'asset', asset: 'favicon', label: 'Favicon', immediate: true,
    desc: 'Browser tab icon: PNG, SVG or ICO. Browsers cache it, so a hard reload may be needed.' },
]

export const FIELD_BY_KEY = Object.fromEntries(FIELDS.map(f => [f.key, f]))

// ---- Values ------------------------------------------------------------------
// `settings` is the object GET /api/settings returned, with the edits applied.
// get() and set() hide the places where the page's control and the wire field
// are not the same thing.

export function getValue(field, s) {
  switch (field.key) {
    case 'watchdog_enabled':
      return !!(s.watchdog_idle_enabled || s.watchdog_busy_enabled)
    case 'csrf':
      return !s.csrf
    case 'galleries':
      return s.galleries_json ?? (s.galleries ? JSON.stringify(s.galleries, null, 2) : '')
    case 'backend_galleries':
      return s.backend_galleries_json ?? (s.backend_galleries ? JSON.stringify(s.backend_galleries, null, 2) : '')
    case 'api_keys':
      return s.api_keys_text ?? (s.api_keys || []).join('\n')
    case 'memory_reclaimer_threshold':
      return s.memory_reclaimer_threshold || field.fallback
    default: {
      const v = s[field.key]
      if (v === undefined || v === null) return 'fallback' in field ? field.fallback : emptyOf(field)
      return v
    }
  }
}

function emptyOf(field) {
  if (field.kind === 'bool') return false
  if (field.kind === 'int' || field.kind === 'percent') return ''
  return ''
}

export function setValue(field, s, value) {
  switch (field.key) {
    case 'watchdog_enabled':
      return { ...s, watchdog_idle_enabled: value, watchdog_busy_enabled: value, watchdog_enabled: value }
    case 'csrf':
      return { ...s, csrf: !value }
    case 'galleries':
      return { ...s, galleries_json: value }
    case 'backend_galleries':
      return { ...s, backend_galleries_json: value }
    case 'api_keys':
      return { ...s, api_keys_text: value }
    default:
      return { ...s, [field.key]: value }
  }
}

// ---- Durations ---------------------------------------------------------------
// Go's time.ParseDuration, which is what the server runs on every duration it
// is sent: "1h30m", "500ms", "1.5s", "15m0s". Returns milliseconds, or null
// when Go would reject it.
const UNIT_MS = { ns: 1e-6, us: 1e-3, 'µs': 1e-3, 'μs': 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 }

export function parseGoDuration(text) {
  const s = String(text ?? '').trim()
  if (!s) return null
  if (s === '0') return 0
  let rest = s
  let sign = 1
  if (rest[0] === '-' || rest[0] === '+') { if (rest[0] === '-') sign = -1; rest = rest.slice(1) }
  if (!rest) return null
  let total = 0
  const re = /^(\d+\.?\d*|\.\d+)(ns|us|µs|μs|ms|s|m|h)/
  while (rest) {
    const m = re.exec(rest)
    if (!m) return null
    total += parseFloat(m[1]) * UNIT_MS[m[2]]
    rest = rest.slice(m[0].length)
  }
  return sign * total
}

// "15m0s" (how Go prints a duration) as "15m", so a field reads like the
// placeholder and the diff shows what a person would type.
export function compactDuration(text) {
  const ms = parseGoDuration(text)
  if (ms === null || ms < 0) return text
  if (ms === 0) return '0'
  const h = Math.floor(ms / 3600000)
  let rest = ms - h * 3600000
  const m = Math.floor(rest / 60000)
  rest -= m * 60000
  let out = ''
  if (h) out += `${h}h`
  if (m) out += `${m}m`
  if (rest > 0) {
    if (rest % 1000 === 0) out += `${rest / 1000}s`
    else out += (h || m || rest >= 1000) ? `${Number((rest / 1000).toFixed(3))}s` : `${rest}ms`
  }
  return out
}

// The settings as the page holds them: durations in their short form. The
// server accepts both forms, and a value that already equals its short form
// is sent unchanged.
export function normalizeLoaded(data) {
  const out = { ...data }
  for (const field of FIELDS) {
    if (field.kind === 'duration' && typeof out[field.key] === 'string') out[field.key] = compactDuration(out[field.key])
  }
  return out
}

// ---- VRAM budget -------------------------------------------------------------
// Mirrors pkg/vrambudget.Parse: empty (no cap), "80%", "0.8", "12GB", "12GiB",
// or a byte count. Returns an error string, or '' when the server would
// accept it.
const SIZE_SUFFIXES = ['KIB', 'MIB', 'GIB', 'TIB', 'KB', 'MB', 'GB', 'TB', 'B']

export function vramBudgetError(text) {
  const s = String(text ?? '').trim()
  if (!s) return ''
  const upper = s.toUpperCase()
  if (upper.endsWith('%')) {
    const n = Number(upper.slice(0, -1).trim())
    if (!Number.isFinite(n) || upper.slice(0, -1).trim() === '') return 'Use a number before the percent sign, for example 80%.'
    if (n < 0 || n > 100) return 'A percentage must be between 0 and 100.'
    return ''
  }
  for (const suffix of SIZE_SUFFIXES) {
    if (upper.endsWith(suffix)) {
      const raw = upper.slice(0, -suffix.length).trim()
      const n = Number(raw)
      if (raw === '' || !Number.isFinite(n) || n < 0) return 'Use a size such as 12GB.'
      return ''
    }
  }
  const n = Number(s)
  if (!Number.isFinite(n) || n < 0) return 'Use a percentage (80%) or a size (12GB).'
  if (n > 1 && n !== Math.trunc(n)) return 'A number above 1 is a byte count and must be whole.'
  return ''
}

// ---- Comparing ---------------------------------------------------------------

function sameValue(field, a, b) {
  switch (field.kind) {
    case 'duration': {
      const x = parseGoDuration(a)
      const y = parseGoDuration(b)
      if (x !== null && y !== null) return x === y
      return String(a ?? '').trim() === String(b ?? '').trim()
    }
    case 'int':
      return Number(a === '' ? 0 : a) === Number(b === '' ? 0 : b)
    case 'percent':
      return Math.abs(Number(a) - Number(b)) < 0.0005
    case 'bool':
      return !!a === !!b
    case 'json':
      return normalJson(a) === normalJson(b)
    default:
      return String(a ?? '').trim() === String(b ?? '').trim()
  }
}

function normalJson(text) {
  const t = String(text ?? '').trim()
  if (!t) return ''
  try { return JSON.stringify(JSON.parse(t)) } catch { return t }
}

export function valuesEqual(field, a, b) { return sameValue(field, a, b) }

export function hasDefault(field) { return Object.prototype.hasOwnProperty.call(field, 'default') }

export function isChanged(field, s) {
  if (!hasDefault(field)) return false
  return !sameValue(field, getValue(field, s), field.default)
}

// ---- Search ------------------------------------------------------------------

export function groupOf(id) { return GROUPS.find(g => g.id === id) }

// Fields matching every word of the query, by name, description, wire key, the
// group, the old section name, and the current value of a short text.
export function searchFields(query, settings) {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return []
  return FIELDS.filter(field => {
    const wire = field.asset ? `asset ${field.asset}` : field.key
    const value = field.kind === 'text' || field.kind === 'duration' || field.kind === 'int'
      ? String(getValue(field, settings) ?? '') : ''
    const hay = [
      field.label, field.desc, wire, wire.replace(/_/g, ' '),
      groupOf(field.group)?.label, OLD_SECTIONS[field.was], value,
    ].join(' ').toLowerCase()
    return words.every(w => hay.includes(w))
  })
}
