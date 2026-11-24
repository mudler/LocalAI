// The rules of the Runtime tuning section of the model editor: the runtime
// settings of an engine, read from and written to the same flat value map the
// editor holds (dotted paths such as `parameters.temperature`, and the
// `options` list of "name:value" entries). Pure functions, so node tests can
// drive every rule without a browser.
//
// Nothing here knows an engine. Everything comes from the server: GET
// /api/engines names the engine of a model's backend, and GET
// /api/engines/{engine}/settings-schema lists its settings. Each setting is
// stored either under a YAML key or as an `options:` entry. The schema also
// carries the controls that write several settings at once (`combined`), the
// presets, the option names other parts of the editor own, and the
// capabilities: `command-line` (command preview and paste box) and
// `tensor-placement` (the placement block).

// --- values --------------------------------------------------------------------

const TRUE_WORDS = new Set(['true', '1', 'yes', 'on', 'enabled'])
const FALSE_WORDS = new Set(['false', '0', 'no', 'off', 'disabled'])

export const CAPABILITY_COMMAND_LINE = 'command-line'
export const CAPABILITY_TENSOR_PLACEMENT = 'tensor-placement'

// The model file key of every LocalAI model config. A pasted command names a
// file by its own path, so it is offered, not written.
export const MODEL_FILE_KEY = 'parameters.model'

/** "name:value" to { name, value }. A bare "name" has the value null. */
export function splitEntry(entry) {
  const text = String(entry).trim()
  const at = text.indexOf(':')
  if (at < 0) return { name: text, value: null }
  return { name: text.slice(0, at).trim(), value: text.slice(at + 1).trim() }
}

const listOf = (options) => (Array.isArray(options) ? options.map(String) : [])

/** All spellings of an option name in a schema, the canonical one first. */
export function optionNames(schema, name) {
  return schema?.aliases?.get(name) || [name]
}

/** The canonical spelling of an option name. */
export function canonicalOption(schema, name) {
  return optionNames(schema, name)[0]
}

/** The raw value text of an option (null for a bare entry), or undefined when absent. */
export function readOption(options, names) {
  for (const entry of listOf(options)) {
    const { name: n, value } = splitEntry(entry)
    if (names.includes(n)) return value
  }
  return undefined
}

/** The whole entry of an option, as written, or undefined when absent. */
export function readEntry(options, names) {
  return listOf(options).find(e => names.includes(splitEntry(e).name))
}

/**
 * A new options list with the option set to `text` (an entry "name:text"), or
 * removed when `text` is undefined. The first entry of any spelling is replaced
 * in place, the others are dropped, and every other entry stays as it is.
 * Returns undefined for an empty list, so the key leaves the file.
 */
export function writeOption(options, names, text) {
  return writeEntry(options, names, text === undefined ? undefined : `${names[0]}:${text}`)
}

/** Like writeOption, with the whole entry given as written ("no_op_offload", "-ot:x=CPU"). */
export function writeEntry(options, names, entry) {
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

const groupId = (raw) => String(raw || '').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '')

/** The id of a setting: its YAML path, or "options.<canonical name>". */
export function itemId(item) {
  if (item.key) return item.key
  return `options.${item.names ? item.names[0] : item.option}`
}

/**
 * The settings-schema answer as a model the section can draw:
 *
 *   { id, name, binary, example, capabilities, groups, items, combined,
 *     presets, reserved, placement, aliases }
 *
 * Each item is { id, name, key, option, names, type, enum, default, min, max,
 * doc, group, flag, composite }: `name` is the flag key of the server's table,
 * `key` the YAML path (or `option` and its spellings `names` for an options
 * entry), `flag` the first command-line spelling. A plain list of flags is read
 * too. Unknown types become strings, and a setting with neither a key nor an
 * option is dropped.
 */
export function normalizeSchema(raw) {
  const list = Array.isArray(raw) ? raw
    : Array.isArray(raw?.flags) ? raw.flags
    : []
  const aliases = new Map()
  const items = []
  const seen = new Set()
  const byName = new Map()
  for (const f of list) {
    if (!f) continue
    const isOption = f.target ? f.target === 'option' : Boolean(f.option && !f.key)
    const key = isOption ? undefined : (f.target ? f.path : f.key)
    const option = isOption ? f.option : undefined
    if (!key && !option) continue
    const names = option ? [String(option), ...(Array.isArray(f.option_aliases) ? f.option_aliases.map(String) : [])] : undefined
    const item = {
      name: f.key !== undefined && f.target ? String(f.key) : (key || option),
      key: key ? String(key) : undefined,
      option: option ? String(option) : undefined,
      names,
      enum: Array.isArray(f.enum) && f.enum.length ? f.enum.map(String) : undefined,
      default: f.default,
      min: typeof f.min === 'number' ? f.min : undefined,
      max: typeof f.max === 'number' ? f.max : undefined,
      doc: f.doc ? String(f.doc) : '',
      group: groupId(f.group) || 'general',
      flag: Array.isArray(f.cli) && f.cli.length ? String(f.cli[0]) : (f.flag ? String(f.flag) : undefined),
      cli: Array.isArray(f.cli) ? f.cli.map(String) : [],
    }
    item.type = item.enum ? 'enum' : (TYPE_ALIASES[String(f.type || '').toLowerCase()] || 'string')
    item.id = itemId(item)
    if (seen.has(item.id)) continue
    seen.add(item.id)
    if (names) for (const n of names) aliases.set(n, names)
    byName.set(item.name, item)
    items.push(item)
  }

  const idOf = (name) => byName.get(name)?.id
  const mapKeys = (obj) => {
    const out = {}
    for (const [k, v] of Object.entries(obj || {})) if (idOf(k)) out[idOf(k)] = v
    return out
  }

  const capabilities = Array.isArray(raw?.capabilities) ? raw.capabilities.map(String) : []
  let placement = null
  const ps = raw?.placement
  if (ps?.option && capabilities.includes(CAPABILITY_TENSOR_PLACEMENT)) {
    const names = [String(ps.option), ...(Array.isArray(ps.aliases) ? ps.aliases.map(String) : [])]
    const flagItem = items.find(i => i.option === ps.option)
    for (const n of names) aliases.set(n, names)
    placement = {
      option: String(ps.option),
      names,
      // The spellings the server reads back; any other spelling is raw text.
      recognised: [...new Set([String(ps.option), ...(flagItem?.cli || [])])],
      group: groupId(ps.group) || 'placement',
      modes: Array.isArray(ps.modes) ? ps.modes.map(String) : [],
      itemId: `options.${ps.option}`,
    }
    if (flagItem) flagItem.composite = true
  }

  const combined = (Array.isArray(raw?.combined) ? raw.combined : []).filter(c => c?.id && Array.isArray(c.keys)).map(c => {
    for (const k of c.keys) if (byName.get(k)) byName.get(k).composite = true
    return {
      id: String(c.id),
      flag: c.flag ? String(c.flag) : undefined,
      group: groupId(c.group) || 'general',
      doc: c.doc ? String(c.doc) : '',
      keys: c.keys.map(idOf).filter(Boolean),
      unset: mapKeys(c.unset),
      choices: (Array.isArray(c.choices) ? c.choices : []).map(ch => ({ value: String(ch.value), label: ch.label ? String(ch.label) : String(ch.value), set: mapKeys(ch.set) })),
    }
  })

  const presets = (Array.isArray(raw?.presets) ? raw.presets : []).filter(p => p?.id).map(p => ({
    id: String(p.id),
    name: p.name ? String(p.name) : String(p.id),
    description: p.description ? String(p.description) : '',
    settings: mapKeys(p.settings),
    placement: placement && p.placement ? p.placement : undefined,
  }))

  const groups = Array.isArray(raw?.groups) ? raw.groups.map(groupId).filter(Boolean) : []
  for (const g of [...items.map(i => i.group), ...combined.map(c => c.group)]) if (!groups.includes(g)) groups.push(g)

  return {
    id: raw?.id ? String(raw.id) : '',
    name: raw?.name ? String(raw.name) : '',
    binary: raw?.binary ? String(raw.binary) : '',
    example: raw?.example ? String(raw.example) : '',
    capabilities,
    groups,
    items,
    combined,
    presets,
    reserved: new Set(Array.isArray(raw?.reserved_options) ? raw.reserved_options.map(String) : []),
    placement,
    aliases,
  }
}

/** Whether the schema lists a capability. */
export function hasCapability(schema, capability) {
  return Boolean(schema?.capabilities?.includes(capability))
}

/** Whether a setting is drawn by a combined control or by the placement block. */
export function isComposite(item) {
  return Boolean(item.composite)
}

/**
 * The blocks of the section, in the schema's group order:
 *   { id, items, combined, placement }
 * A group is drawn when it has a setting of its own or a combined control;
 * the placement group is drawn only for an engine with that capability.
 */
export function groupItems(schema) {
  const out = []
  for (const g of schema.groups) {
    if (schema.placement && g === schema.placement.group) {
      out.push({ id: g, items: [], combined: [], placement: true })
      continue
    }
    const items = schema.items.filter(i => i.group === g && !isComposite(i))
    const combined = schema.combined.filter(c => c.group === g)
    if (items.length || combined.length) out.push({ id: g, items, combined, placement: false })
  }
  return out
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

const namesOf = (item) => item.names || [item.option]

/** The value of a setting in a flat value map, or undefined when unset. */
export function readItem(values, item) {
  if (item.key) {
    const v = values[item.key]
    return v === null ? undefined : typed(item, v)
  }
  return typed(item, readOption(values.options, namesOf(item)))
}

/** Sets (or, with undefined, unsets) a setting in a draft value map. */
export function writeItem(draft, item, value) {
  if (item.key) {
    if (value === undefined || value === null || value === '') delete draft[item.key]
    else draft[item.key] = value
    return
  }
  const text = value === undefined || value === null || value === '' ? undefined : String(value)
  const next = writeOption(draft.options, namesOf(item), text)
  if (next === undefined) delete draft.options
  else draft.options = next
}

/**
 * Checks the text typed for a setting. Returns { ok, value } or
 * { ok: false, error, params }; error is a key under engineSettings.errors.
 * Empty text is valid and means "not set".
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

// --- combined controls ---------------------------------------------------------------

const itemById = (schema, id) => schema.items.find(i => i.id === id)
const isUnsetChoice = (choice) => Object.values(choice.set).every(v => v === null || v === undefined)

/**
 * The choice of a combined control that a value map holds. With every key
 * unset, the choice that sets nothing. Else the last choice whose non-null
 * values all equal the values in the map (the control's `unset` value for a
 * missing key).
 */
export function readCombined(schema, values, combined) {
  const read = (id) => {
    const item = itemById(schema, id)
    return item ? readItem(values, item) : undefined
  }
  const blank = combined.choices.find(isUnsetChoice)
  if (combined.keys.every(id => read(id) === undefined)) return blank?.value ?? combined.choices[0]?.value
  for (let i = combined.choices.length - 1; i >= 0; i--) {
    const ch = combined.choices[i]
    if (isUnsetChoice(ch)) continue
    const fits = Object.entries(ch.set).every(([id, v]) => v === null || v === undefined || (read(id) ?? combined.unset[id]) === v)
    if (fits) return ch.value
  }
  return blank?.value ?? combined.choices[0]?.value
}

/** Writes the settings of a choice of a combined control into a draft. */
export function writeCombined(schema, draft, combined, value) {
  const ch = combined.choices.find(c => c.value === value) || combined.choices.find(isUnsetChoice)
  if (!ch) return
  for (const id of combined.keys) {
    const item = itemById(schema, id)
    if (!item) continue
    const v = ch.set[id]
    writeItem(draft, item, v === null ? undefined : v)
  }
}

// --- tensor placement -----------------------------------------------------------------

// The server builds and reads the placement entries (POST
// /api/engines/{engine}/placement), so the pattern rules live in one place.
// These helpers only shape the request and the answer, and check a raw value
// typed by hand.
//
// A choice on screen:
//   { mode: 'off' }                                        nothing written
//   { mode: 'experts' }                                    experts_cpu_all
//   { mode: 'ranges', ranges: [{ from, to }] }             experts_cpu_layers
//   { mode: 'rest', gpuLayers: N, totalLayers: '' | M }    first_layers_gpu
//   { mode: 'raw', raw }                                   written as typed

export const SERVER_MODE = { experts: 'experts_cpu_all', ranges: 'experts_cpu_layers', rest: 'first_layers_gpu' }
const isCount = (v) => /^\d+$/.test(String(v ?? '').trim())

/** The screen modes an engine offers: off, its server modes, and raw. */
export function placementModes(placement) {
  const server = Object.keys(SERVER_MODE).filter(m => !placement?.modes?.length || placement.modes.includes(SERVER_MODE[m]))
  const order = ['off', 'experts', 'ranges', 'rest', 'raw']
  return order.filter(m => m === 'off' || m === 'raw' || server.includes(m))
}

/**
 * The placement to send to the server for a choice, { placement } or
 * { error } (a key under engineSettings.errors) when the typed numbers do not
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

/** The choice that shows a placement the server read from an entry. */
export function choiceFromPlacement(placement, raw) {
  switch (placement?.mode) {
    case 'experts_cpu_all': return { mode: 'experts' }
    case 'experts_cpu_layers': return { mode: 'ranges', ranges: (placement.layers || []).map(r => ({ from: r.from, to: r.to })) }
    case 'first_layers_gpu': return { mode: 'rest', gpuLayers: placement.first_gpu_layers ?? 0, totalLayers: placement.total_layers ? placement.total_layers : '' }
    default: return raw ? { mode: 'raw', raw } : { mode: 'off' }
  }
}

/**
 * Checks a placement value typed by hand: comma separated "pattern=buffer"
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

/** The value of a placement entry "<option>:pattern" (the server answers the whole entry). */
export function patternOf(placement, option) {
  if (option === undefined || option === null) return undefined
  const { name, value } = splitEntry(option)
  return placement.names.includes(name) ? (value ?? '') : String(option)
}

// --- raw options -----------------------------------------------------------------------

/** The option names (every spelling) the form owns, so they are not raw options. */
export function ownedOptionNames(schema) {
  const out = new Set(schema.reserved)
  for (const item of schema.items) if (item.option) for (const n of namesOf(item)) out.add(n)
  if (schema.placement) for (const n of schema.placement.names) out.add(n)
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

/** The raw option that keeps an unmapped command-line flag: "--flag:value" or "--flag". */
export function rawOptionFor({ flag, value }) {
  let f = String(flag || '').trim()
  if (!f.startsWith('-')) f = `--${f}`
  const v = value === undefined || value === null ? '' : String(value).trim()
  return v === '' ? f : `${f}:${v}`
}

// --- whole-setup presets ------------------------------------------------------------------

/**
 * Writes a preset into a draft. `placementEntry` is the entry the server
 * built for the preset's placement, when it has one. A setting the schema
 * does not have is skipped.
 */
export function applyPreset(schema, draft, preset, placementEntry) {
  for (const [id, value] of Object.entries(preset.settings)) {
    const item = itemById(schema, id)
    if (item) writeItem(draft, item, value)
  }
  if (preset.placement && placementEntry && schema.placement) {
    const next = writeEntry(draft.options, schema.placement.names, placementEntry)
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

/**
 * Writes the answer of POST /api/engines/{engine}/parse-command into a draft.
 * `fields` is a partial model config with YAML keys; `options` are entries,
 * each replacing an entry of the same name. The model file is not written: a
 * command names a file by its own path, so it is returned for the person to
 * confirm when it differs from the model's file.
 */
export function applyParsed(schema, draft, parsed) {
  const byKey = new Map(schema.items.filter(i => i.key).map(i => [i.key, i]))
  let modelFile
  for (const [path, value] of Object.entries(flatten(parsed?.fields))) {
    if (path === MODEL_FILE_KEY) { modelFile = value; continue }
    if (value === undefined || value === null) continue
    const item = byKey.get(path)
    if (item) writeItem(draft, item, value)
    else draft[path] = value
  }
  // Each entry replaces the entry of the same name (any spelling), in place,
  // as the server wrote it: "ubatch:64", "no_op_offload", "-ot:...=CPU".
  for (const entry of listOf(parsed?.options)) {
    const { name } = splitEntry(entry)
    if (!name) continue
    const next = writeEntry(draft.options, optionNames(schema, name), entry)
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
export function accessors(schema, before, after) {
  const list = []
  for (const item of schema.items) {
    if (isComposite(item)) continue
    list.push({ id: item.id, item, read: v => readItem(v, item), write: (d, x) => writeItem(d, item, x) })
  }
  for (const c of schema.combined) {
    list.push({ id: c.id, combined: c, read: v => readCombined(schema, v, c), write: (d, x) => writeCombined(schema, d, c, x) })
  }
  if (schema.placement) {
    const names = schema.placement.names
    list.push({
      id: schema.placement.itemId,
      placement: true,
      read: v => readEntry(v.options, names),
      write: (d, x) => { const next = writeEntry(d.options, names, x); if (next) d.options = next; else delete d.options },
    })
  }
  const owned = ownedOptionNames(schema)
  const rawNames = new Set()
  for (const v of [before, after]) for (const e of rawOptions(v.options, owned)) rawNames.add(splitEntry(e).name)
  for (const name of rawNames) {
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
  const covered = new Set(schema.items.filter(i => i.key).map(i => i.key))
  covered.add('options')
  for (const path of new Set([...Object.keys(before), ...Object.keys(after)])) {
    if (covered.has(path) || same(before[path], after[path])) continue
    list.push({ id: path, read: v => v[path], write: (d, x) => { if (x === undefined) delete d[path]; else d[path] = x } })
  }
  return list
}

/** The changes between two value maps: [{ id, item, combined, from, to, read, write }]. */
export function describeChanges(schema, before, after) {
  return accessors(schema, before, after)
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
