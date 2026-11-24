// The rules of the Runtime tuning section of the model editor: the llama.cpp
// settings of a model, read from and written to the same flat value map the
// editor holds (dotted paths such as `parameters.temperature`, and the
// `options` list of "name:value" entries). Pure functions, so node tests can
// drive every rule without a browser.
//
// The settings themselves come from the server (GET
// /api/llama-cpp/settings-schema): each one is stored either under a YAML key
// (`key`) or as an `options:` entry (`option`). This file only knows how to
// read and write the two kinds, plus the few controls that combine several
// settings: the load mode (mmap, mlock, direct I/O) and the tensor placement
// (llama-server's `-ot`, kept as the passthrough entry "-ot:pattern=buffer").

import { ALL_LAYERS } from './placement.js'

// --- values --------------------------------------------------------------------

const TRUE_WORDS = new Set(['true', '1', 'yes', 'on', 'enabled'])
const FALSE_WORDS = new Set(['false', '0', 'no', 'off', 'disabled'])

// Option names the llama.cpp backend reads under more than one spelling. The
// first name of a group is the one this section writes.
const OPTION_ALIASES = [
  ['use_jinja', 'jinja'],
  ['parallel', 'n_parallel'],
  ['ubatch', 'n_ubatch'],
  ['threads_batch', 'n_threads_batch'],
  ['fit', 'fit_params'],
  ['direct_io', 'use_direct_io'],
  // -ot goes to llama-server's own parser; override_tensor is the older
  // backend option with the same value, read so an old file shows here.
  ['-ot', '--override-tensor', 'override_tensor', 'tensor_buft_overrides'],
  ['no_op_offload'],
]
const ALIAS_OF = new Map()
for (const group of OPTION_ALIASES) for (const name of group) ALIAS_OF.set(name, group)

/** All spellings of an option name, the canonical one first. */
export function optionNames(name) {
  return ALIAS_OF.get(name) || [name]
}

/** The canonical spelling of an option name. */
export function canonicalOption(name) {
  return optionNames(name)[0]
}

// The options the Placement section writes. They are not raw options here.
export const PLACEMENT_OPTIONS = ['cpu_moe', 'n_cpu_moe', 'moe_cache_mib']

export const TENSOR_OPTION = '-ot'

/** "name:value" to { name, value }. A bare "name" has the value null. */
export function splitEntry(entry) {
  const text = String(entry).trim()
  const at = text.indexOf(':')
  if (at < 0) return { name: text, value: null }
  return { name: text.slice(0, at).trim(), value: text.slice(at + 1).trim() }
}

const listOf = (options) => (Array.isArray(options) ? options.map(String) : [])

/** The raw value text of an option (null for a bare entry), or undefined when absent. */
export function readOption(options, name) {
  const names = optionNames(name)
  for (const entry of listOf(options)) {
    const { name: n, value } = splitEntry(entry)
    if (names.includes(n)) return value
  }
  return undefined
}

/** The whole entry of an option, as written, or undefined when absent. */
export function readEntry(options, name) {
  const names = optionNames(name)
  return listOf(options).find(e => names.includes(splitEntry(e).name))
}

/**
 * A new options list with the option set to `text` (an entry "name:text"), or
 * removed when `text` is undefined. The first entry of any spelling is replaced
 * in place, the others are dropped, and every other entry stays as it is.
 * Returns undefined for an empty list, so the key leaves the file.
 */
export function writeOption(options, name, text) {
  return writeEntry(options, name, text === undefined ? undefined : `${optionNames(name)[0]}:${text}`)
}

/** Like writeOption, with the whole entry given as written ("no_op_offload", "-ot:x=CPU"). */
export function writeEntry(options, name, entry) {
  const names = optionNames(name)
  const out = []
  let placed = false
  for (const e of listOf(options)) {
    if (!names.includes(splitEntry(e).name)) { out.push(e); continue }
    if (!placed && entry !== undefined) out.push(entry)
    placed = true
  }
  if (!placed && entry !== undefined) out.push(entry)
  return out.length ? out : undefined
}

// --- the schema ------------------------------------------------------------------

const TYPE_ALIASES = {
  int: 'int', integer: 'int', int32: 'int', int64: 'int', uint: 'int',
  float: 'float', float32: 'float', float64: 'float', number: 'float', double: 'float',
  bool: 'bool', boolean: 'bool',
  string: 'string', str: 'string', enum: 'enum',
}

// The groups the section draws, in order. A group the server names in other
// words is matched by its meaning; an unknown group keeps its own name.
// Placement holds only -ot, which has its own block.
export const GROUPS = ['memory', 'speed', 'sampling', 'template']

export function groupOf(raw) {
  const g = String(raw || '').toLowerCase()
  if (/sampl/.test(g)) return 'sampling'
  if (/templ|chat|jinja/.test(g)) return 'template'
  if (/place/.test(g)) return 'placement'
  if (/speed|perf|thread|batch/.test(g)) return 'speed'
  if (/mem|load|gpu|cpu|context/.test(g) || g === '') return 'memory'
  return g.replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '')
}

// The llama-server flag of each setting, for the hint under a field when the
// schema does not carry one. Keyed by the setting id (see itemId).
export const FLAG_HINTS = {
  context_size: '--ctx-size',
  gpu_layers: '--n-gpu-layers',
  'options.parallel': '--parallel',
  flash_attention: '--flash-attn',
  cache_type_k: '--cache-type-k',
  cache_type_v: '--cache-type-v',
  'options.ubatch': '--ubatch-size',
  'parameters.batch': '--batch-size',
  threads: '--threads',
  'options.threads_batch': '--threads-batch',
  'options.fit': '--fit',
  'options.no_op_offload': '--no-op-offload',
  'parameters.temperature': '--temp',
  'parameters.top_p': '--top-p',
  'parameters.top_k': '--top-k',
  'parameters.min_p': '--min-p',
  'options.use_jinja': '--jinja',
  mmap: '--mmap',
  mmlock: '--mlock',
  'options.direct_io': '--direct-io',
  'options.-ot': '-ot',
}

/** The id of a setting: its YAML path, or "options.<canonical name>". */
export function itemId(item) {
  if (item.key) return item.key
  return `options.${canonicalOption(item.option)}`
}

// The settings drawn by a combined control: the load mode and the tensor placement.
const COMPOSITE_IDS = new Set(['mmap', 'mmlock', 'mlock', 'options.direct_io', `options.${TENSOR_OPTION}`, 'options.override_tensor'])

/**
 * The settings-schema answer as a list of settings the section can draw:
 *   { id, key, option, type, enum, default, min, max, doc, group, flag }
 * The server answers { flags: [...] }, each flag with a `target` (field,
 * parameter or option), its YAML `path`, its `option` name and its `cli`
 * spellings. A plain list of { key, option, ... } is read too. Unknown types
 * become strings, and a setting with neither a key nor an option is dropped.
 */
export function normalizeSchema(raw) {
  const list = Array.isArray(raw) ? raw
    : Array.isArray(raw?.flags) ? raw.flags
    : Array.isArray(raw?.settings) ? raw.settings
    : Array.isArray(raw?.fields) ? raw.fields
    : []
  const out = []
  const seen = new Set()
  for (const f of list) {
    if (!f) continue
    // A flag of the server's table: the YAML path is the key, unless the flag
    // is an entry of the options list.
    const s = f.target
      ? { ...f, key: f.target === 'option' ? undefined : f.path, option: f.target === 'option' ? f.option : undefined, flag: Array.isArray(f.cli) ? f.cli[0] : f.flag }
      : f
    if (!s.key && !s.option) continue
    const key = s.key ? String(s.key) : undefined
    // `mlock` is the name llama-server uses; the model file says `mmlock`.
    const item = {
      key: key === 'mlock' ? 'mmlock' : key,
      option: s.option && !s.key ? String(s.option) : undefined,
      enum: Array.isArray(s.enum) && s.enum.length ? s.enum.map(String) : undefined,
      default: s.default,
      min: typeof s.min === 'number' ? s.min : undefined,
      max: typeof s.max === 'number' ? s.max : undefined,
      doc: s.doc ? String(s.doc) : '',
      group: groupOf(s.group),
    }
    item.type = item.enum ? 'enum' : (TYPE_ALIASES[String(s.type || '').toLowerCase()] || 'string')
    item.id = itemId(item)
    item.flag = s.flag ? String(s.flag) : FLAG_HINTS[item.id]
    if (seen.has(item.id)) continue
    seen.add(item.id)
    out.push(item)
  }
  return out
}

/** Whether a setting is drawn by a combined control (load mode, tensor placement). */
export function isComposite(item) {
  return COMPOSITE_IDS.has(item.id)
}

/** The settings of each group, in the order of GROUPS then any other group. */
export function groupItems(items) {
  const groups = new Map()
  for (const item of items) {
    if (isComposite(item)) continue
    if (!groups.has(item.group)) groups.set(item.group, [])
    groups.get(item.group).push(item)
  }
  const order = [...GROUPS, ...[...groups.keys()].filter(g => !GROUPS.includes(g))]
  // The load mode belongs to memory, so that group is drawn even when empty.
  return order.filter(g => groups.has(g) || g === 'memory').map(g => ({ id: g, items: groups.get(g) || [] }))
}

// --- reading and writing one setting ------------------------------------------------

function typed(item, text) {
  if (text === undefined) return undefined
  if (item.type === 'bool') {
    if (text === null || text === true) return true
    if (text === false) return false
    const w = String(text).toLowerCase()
    if (TRUE_WORDS.has(w)) return true
    if (FALSE_WORDS.has(w)) return false
    return undefined
  }
  if (item.type === 'int' || item.type === 'float') {
    if (text === null || text === '') return undefined
    const n = Number(text)
    return Number.isFinite(n) ? n : undefined
  }
  if (text === null) return undefined
  return String(text)
}

/** The value of a setting in a flat value map, or undefined when unset. */
export function readItem(values, item) {
  if (item.key) {
    const v = values[item.key]
    return v === null ? undefined : typed(item, v)
  }
  return typed(item, readOption(values.options, item.option))
}

/** Sets (or, with undefined, unsets) a setting in a draft value map. */
export function writeItem(draft, item, value) {
  if (item.key) {
    if (value === undefined || value === null || value === '') delete draft[item.key]
    else draft[item.key] = value
    return
  }
  const text = value === undefined || value === null || value === '' ? undefined : String(value)
  const next = writeOption(draft.options, item.option, text)
  if (next === undefined) delete draft.options
  else draft.options = next
}

/**
 * Checks the text typed for a setting. Returns { ok, value } or
 * { ok: false, error, params }; error is a key under runtime.errors. Empty text
 * is valid and means "not set".
 */
export function validate(item, text) {
  const s = String(text ?? '').trim()
  if (s === '') return { ok: true, value: undefined }
  if (item.type === 'int' || item.type === 'float') {
    const n = Number(s)
    if (!Number.isFinite(n) || (item.type === 'int' && !/^-?\d+$/.test(s))) {
      return { ok: false, error: item.type === 'int' ? 'notInteger' : 'notNumber' }
    }
    if (item.min !== undefined && n < item.min) {
      return item.max !== undefined ? { ok: false, error: 'range', params: { min: item.min, max: item.max } } : { ok: false, error: 'min', params: { min: item.min } }
    }
    if (item.max !== undefined && n > item.max) {
      return item.min !== undefined ? { ok: false, error: 'range', params: { min: item.min, max: item.max } } : { ok: false, error: 'max', params: { max: item.max } }
    }
    return { ok: true, value: n }
  }
  if (item.type === 'enum' && !item.enum.includes(s)) return { ok: false, error: 'enum', params: { values: item.enum.join(', ') } }
  if (item.option && s.includes(':') && item.type === 'string') return { ok: false, error: 'colon' }
  return { ok: true, value: s }
}

// --- load mode -------------------------------------------------------------------------

// The names of llama-server's --load-mode, plus 'default' (nothing set).
export const LOAD_MODES = ['default', 'none', 'mmap', 'mlock', 'mmap_mlock', 'direct_io']

const BOOL = (id, key, option) => ({ id, key, option, type: 'bool' })
const LOAD_ITEMS = {
  mmap: BOOL('mmap', 'mmap'),
  mlock: BOOL('mmlock', 'mmlock'),
  direct: BOOL('options.direct_io', undefined, 'direct_io'),
}

/** The load mode a value map says. Unset mmap means on (the LocalAI default). */
export function readLoadMode(values) {
  const mmap = readItem(values, LOAD_ITEMS.mmap)
  const mlock = readItem(values, LOAD_ITEMS.mlock)
  const direct = readItem(values, LOAD_ITEMS.direct)
  if (direct === true) return 'direct_io'
  if (mmap === undefined && mlock === undefined && direct === undefined) return 'default'
  const m = mmap ?? true
  const l = mlock ?? false
  if (m && l) return 'mmap_mlock'
  if (l) return 'mlock'
  if (m) return 'mmap'
  return 'none'
}

/**
 * Writes the three settings of a load mode into a draft, as the server's
 * command parser writes them: mlock only where the mode has it.
 */
export function writeLoadMode(draft, mode) {
  const set = (item, v) => writeItem(draft, item, v)
  switch (mode) {
    case 'none': set(LOAD_ITEMS.mmap, false); set(LOAD_ITEMS.mlock, undefined); set(LOAD_ITEMS.direct, undefined); break
    case 'mmap': set(LOAD_ITEMS.mmap, true); set(LOAD_ITEMS.mlock, undefined); set(LOAD_ITEMS.direct, undefined); break
    case 'mlock': set(LOAD_ITEMS.mmap, false); set(LOAD_ITEMS.mlock, true); set(LOAD_ITEMS.direct, undefined); break
    case 'mmap_mlock': set(LOAD_ITEMS.mmap, true); set(LOAD_ITEMS.mlock, true); set(LOAD_ITEMS.direct, undefined); break
    case 'direct_io': set(LOAD_ITEMS.mmap, undefined); set(LOAD_ITEMS.mlock, undefined); set(LOAD_ITEMS.direct, true); break
    default: set(LOAD_ITEMS.mmap, undefined); set(LOAD_ITEMS.mlock, undefined); set(LOAD_ITEMS.direct, undefined)
  }
}

// --- tensor placement (-ot) -----------------------------------------------------------

// The server builds and reads the -ot patterns (POST /api/llama-cpp/tensor-placement),
// so the regex rules live in one place. These helpers only shape the request
// and the answer, and check a raw value typed by hand.
//
// A choice on screen:
//   { mode: 'off' }                                        nothing written
//   { mode: 'experts' }                                    experts_cpu_all
//   { mode: 'ranges', ranges: [{ from, to }] }             experts_cpu_layers
//   { mode: 'rest', gpuLayers: N, totalLayers: '' | M }    first_layers_gpu
//   { mode: 'raw', raw }                                   written as typed

const SERVER_MODE = { experts: 'experts_cpu_all', ranges: 'experts_cpu_layers', rest: 'first_layers_gpu' }
const isCount = (v) => /^\d+$/.test(String(v ?? '').trim())

/**
 * The placement to send to the server for a choice, { placement } or
 * { error } (a key under runtime.errors) when the typed numbers do not
 * validate. 'off' and 'raw' need no request and give null.
 */
export function placementRequest(choice) {
  const mode = SERVER_MODE[choice?.mode]
  if (!mode) return null
  if (choice.mode === 'experts') return { placement: { mode } }
  if (choice.mode === 'ranges') {
    const ranges = choice.ranges || []
    if (!ranges.length || ranges.some(r => !isCount(r.from) || !isCount(r.to) || Number(r.to) < Number(r.from))) {
      return { error: 'layerRange' }
    }
    return { placement: { mode, layers: ranges.map(r => ({ from: Number(r.from), to: Number(r.to) })) } }
  }
  if (!isCount(choice.gpuLayers)) return { error: 'notInteger' }
  const placement = { mode, first_gpu_layers: Number(choice.gpuLayers) }
  const total = String(choice.totalLayers ?? '').trim()
  if (total !== '') {
    if (!isCount(total)) return { error: 'notInteger' }
    placement.total_layers = Number(total)
  }
  return { placement }
}

/** The choice that shows a placement the server read from an -ot entry. */
export function choiceFromPlacement(placement, raw) {
  switch (placement?.mode) {
    case 'experts_cpu_all': return { mode: 'experts' }
    case 'experts_cpu_layers': return { mode: 'ranges', ranges: (placement.layers || []).map(r => ({ from: r.from, to: r.to })) }
    case 'first_layers_gpu': return { mode: 'rest', gpuLayers: placement.first_gpu_layers ?? 0, totalLayers: placement.total_layers ? placement.total_layers : '' }
    default: return raw ? { mode: 'raw', raw } : { mode: 'off' }
  }
}

/**
 * Checks an -ot value typed by hand: comma separated "pattern=buffer"
 * entries, each pattern a valid regex. Returns null when valid, else
 * { error, params }.
 */
export function validateOverride(value) {
  const raw = String(value ?? '').trim()
  if (raw === '') return null
  for (const entry of raw.split(',')) {
    const at = entry.lastIndexOf('=')
    const pattern = entry.slice(0, at).trim()
    const target = entry.slice(at + 1).trim()
    if (at < 0 || !pattern || !target) return { error: 'overrideShape', params: { entry } }
    if (!/^[A-Za-z0-9_]+$/.test(target)) return { error: 'overrideTarget', params: { target } }
    try { new RegExp(pattern) } catch { return { error: 'overrideRegex', params: { entry: pattern } } }
  }
  return null
}

/** The pattern of an "-ot:pattern" entry (the server answers the whole entry). */
export function patternOf(option) {
  if (option === undefined || option === null) return undefined
  const { name, value } = splitEntry(option)
  return optionNames(TENSOR_OPTION).includes(name) ? (value ?? '') : String(option)
}

export const TENSOR_ITEM = { id: `options.${TENSOR_OPTION}`, option: TENSOR_OPTION, type: 'string' }

// --- raw options -----------------------------------------------------------------------

/** The option names (every spelling) the form owns, so they are not raw options. */
export function ownedOptionNames(items) {
  const out = new Set(PLACEMENT_OPTIONS)
  const add = (name) => { for (const n of optionNames(name)) out.add(n) }
  for (const item of items) if (item.option) add(item.option)
  add('direct_io')
  add(TENSOR_OPTION)
  return out
}

/** The options entries the form does not own, as text. */
export function rawOptions(options, owned) {
  return listOf(options).filter(e => !owned.has(splitEntry(e).name))
}

/**
 * The options list with its raw entries replaced by `raw`, keeping the owned
 * entries where they are. Returns undefined for an empty list.
 */
export function replaceRawOptions(options, owned, raw) {
  const queue = raw.map(String)
  const out = []
  for (const entry of listOf(options)) {
    if (owned.has(splitEntry(entry).name)) out.push(entry)
    else if (queue.length) out.push(queue.shift())
  }
  out.push(...queue)
  return out.length ? out : undefined
}

/**
 * Checks a raw option entry. Returns null when valid, else { error, params }:
 * empty text, or a name the form already sets (it would be set twice).
 */
export function validateRawOption(entry, owned) {
  const text = String(entry ?? '').trim()
  if (text === '') return { error: 'empty' }
  const { name } = splitEntry(text)
  if (!name) return { error: 'empty' }
  if (owned.has(name)) return { error: 'owned', params: { name } }
  return null
}

/** The raw option that keeps an unmapped llama-server flag: "--flag:value" or "--flag". */
export function rawOptionFor({ flag, value }) {
  let f = String(flag || '').trim()
  if (!f.startsWith('-')) f = `--${f}`
  const v = value === undefined || value === null ? '' : String(value).trim()
  return v === '' ? f : `${f}:${v}`
}

// --- whole-setup presets ------------------------------------------------------------------

// Each preset sets settings by id and, optionally, the tensor placement. A
// setting the server's schema does not have is skipped.
export const SETUP_PRESETS = [
  {
    id: 'bigMoe',
    settings: {
      gpu_layers: ALL_LAYERS,
      flash_attention: 'on',
      cache_type_k: 'q8_0',
      cache_type_v: 'q8_0',
      'options.parallel': 1,
      'options.no_op_offload': true,
    },
    tensor: { mode: 'experts' },
  },
  {
    id: 'maxContext',
    settings: {
      context_size: 131072,
      flash_attention: 'on',
      cache_type_k: 'q8_0',
      cache_type_v: 'q8_0',
      'options.parallel': 1,
    },
  },
  {
    id: 'lowVram',
    settings: {
      context_size: 4096,
      flash_attention: 'on',
      cache_type_k: 'q4_0',
      cache_type_v: 'q4_0',
      'options.ubatch': 128,
      'options.parallel': 1,
    },
  },
]

/**
 * Writes a preset into a draft. `tensorEntry` is the -ot entry the server
 * built for the preset's placement ("-ot:pattern"), when it has one.
 */
export function applyPreset(draft, items, preset, tensorEntry) {
  const byId = new Map(items.map(i => [i.id, i]))
  for (const [id, value] of Object.entries(preset.settings)) {
    const item = byId.get(id)
    if (item) writeItem(draft, item, value)
  }
  if (preset.tensor && tensorEntry) {
    const next = writeEntry(draft.options, TENSOR_OPTION, tensorEntry)
    if (next) draft.options = next
  }
}

// --- a parsed command --------------------------------------------------------------------

function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj || {})) {
    const path = prefix ? `${prefix}.${k}` : k
    if (v !== null && typeof v === 'object' && !Array.isArray(v)) flatten(v, path, out)
    else out[path] = v
  }
  return out
}

const FIELD_ALIASES = { mlock: 'mmlock', mmlock: 'mmlock' }

/**
 * Writes the answer of POST /api/llama-cpp/parse-command into a draft.
 * `fields` is a partial model config with YAML keys; `options` are entries,
 * each replacing an entry of the same name. The model file is not written: a
 * command names a file by its own path, so it is returned for the person to
 * confirm when it differs from the model's file.
 */
export function applyParsed(draft, items, parsed) {
  const byKey = new Map(items.filter(i => i.key).map(i => [i.key, i]))
  const byOption = new Map(items.filter(i => i.option).map(i => [canonicalOption(i.option), i]))
  let modelFile
  for (const [rawPath, value] of Object.entries(flatten(parsed?.fields))) {
    const path = FIELD_ALIASES[rawPath] || rawPath
    if (path === 'parameters.model') { modelFile = value; continue }
    if (value === undefined || value === null) continue
    const item = byKey.get(path) || byOption.get(canonicalOption(path))
      || (path === 'direct_io' ? LOAD_ITEMS.direct : null)
      || (path === 'mmap' ? LOAD_ITEMS.mmap : null)
      || (path === 'mmlock' ? LOAD_ITEMS.mlock : null)
    if (item) writeItem(draft, item, value)
    else draft[path] = value
  }
  // Each entry replaces the entry of the same name (any spelling), in place,
  // as the server wrote it: "ubatch:64", "no_op_offload", "-ot:...=CPU".
  for (const entry of listOf(parsed?.options)) {
    const { name } = splitEntry(entry)
    if (!name) continue
    const next = writeEntry(draft.options, name, entry)
    if (next) draft.options = next
  }
  return { modelFile: modelFile === undefined || modelFile === '' ? undefined : String(modelFile) }
}

// --- what changed --------------------------------------------------------------------------

const same = (a, b) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

/**
 * The accessors of everything the section can change, so a change can be
 * listed and undone one setting at a time: { id, read(values), write(draft, v) }.
 */
export function accessors(items, before, after) {
  const list = []
  for (const item of items) {
    if (isComposite(item)) continue
    list.push({ id: item.id, item, read: v => readItem(v, item), write: (d, x) => writeItem(d, item, x) })
  }
  list.push({ id: 'loadMode', read: readLoadMode, write: writeLoadMode })
  list.push({
    id: TENSOR_ITEM.id,
    read: v => readEntry(v.options, TENSOR_OPTION),
    write: (d, x) => { const next = writeEntry(d.options, TENSOR_OPTION, x); if (next) d.options = next; else delete d.options },
  })
  const owned = ownedOptionNames(items)
  const names = new Set()
  for (const v of [before, after]) for (const e of rawOptions(v.options, owned)) names.add(splitEntry(e).name)
  for (const name of names) {
    list.push({
      id: `options.${name}`,
      read: v => {
        const hit = listOf(v.options).find(e => splitEntry(e).name === name)
        return hit === undefined ? undefined : hit
      },
      write: (d, x) => {
        const rest = listOf(d.options).filter(e => splitEntry(e).name !== name)
        const next = x === undefined ? rest : [...rest, x]
        if (next.length) d.options = next
        else delete d.options
      },
    })
  }
  const covered = new Set(items.filter(i => i.key).map(i => i.key))
  for (const k of ['options', 'mmap', 'mmlock']) covered.add(k)
  for (const path of new Set([...Object.keys(before), ...Object.keys(after)])) {
    if (covered.has(path) || same(before[path], after[path])) continue
    list.push({ id: path, read: v => v[path], write: (d, x) => { if (x === undefined) delete d[path]; else d[path] = x } })
  }
  return list
}

/** The changes between two value maps: [{ id, item, from, to, read, write }]. */
export function describeChanges(items, before, after) {
  return accessors(items, before, after)
    .map(a => ({ ...a, from: a.read(before), to: a.read(after) }))
    .filter(c => !same(c.from, c.to))
}

/**
 * Undoes a list of changes on a draft. A setting edited since is left as it
 * is, so an undo never throws away a later edit.
 */
export function undoChanges(draft, changes) {
  for (const c of changes) {
    if (same(c.read(draft), c.to)) c.write(draft, c.from)
  }
}

/**
 * The paths whose value differs between two maps, with the new value
 * (undefined when the key is gone), for the editor's onChange.
 */
export function diffPaths(before, after) {
  const out = []
  for (const path of new Set([...Object.keys(before), ...Object.keys(after)])) {
    if (!same(before[path], after[path])) out.push([path, after[path]])
  }
  return out
}

/** A flat dotted map as a nested model config (for the command preview). */
export function unflatten(flat) {
  const out = {}
  for (const [path, value] of Object.entries(flat || {})) {
    if (value === undefined) continue
    const keys = path.split('.')
    let node = out
    for (let i = 0; i < keys.length - 1; i++) {
      if (!node[keys[i]] || typeof node[keys[i]] !== 'object') node[keys[i]] = {}
      node = node[keys[i]]
    }
    node[keys[keys.length - 1]] = value
  }
  return out
}
