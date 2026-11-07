// Setups: the model, the tool switches and the effort of a new chat, saved
// under a name in LocalAI/setups.json. Pure helpers, no browser globals at
// import time, so the node unit tests load them. Every function returns new
// objects and leaves its input as it was.

export const MAX_SETUPS = 50
export const MAX_NAME = 60
const MAX_ID = 40
const LISTS = ['builtins_off', 'builtins_on', 'addons_off', 'mcp_off', 'skills_off']

// The first MAX_NAME code points: a cut at 60 UTF-16 units could split an
// emoji, and the server stores a lone half as U+FFFD.
const clipName = (s, max = MAX_NAME) => Array.from(s).slice(0, max).join('')

// A name as the person typed it: trimmed, inner spaces collapsed, capped.
export function cleanName(name) {
  return clipName(String(name ?? '').trim().replace(/\s+/g, ' ')).trim()
}

// An id from a name: a lowercase slug, unique among taken (-2, -3, ...).
export function setupId(name, taken = []) {
  const slug = String(name ?? '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
  const base = slug.slice(0, MAX_ID - 4).replace(/-+$/g, '') || 'setup'
  let id = base
  for (let n = 2; taken.includes(id); n++) id = `${base}-${n}`
  return id
}

// The data of a setups document: tolerant of a missing or invalid one.
export function setupsData(doc) {
  const d = doc?.data
  const list = Array.isArray(d?.setups) ? d.setups : []
  const setups = list.filter((s) => s && typeof s.id === 'string' && s.id && typeof s.name === 'string')
  return { default: typeof d?.default === 'string' ? d.default : '', setups }
}

// A setup from the state of the composer. tools is what draftSwitches()
// returns (or null). Empty lists are left out.
export function captureSetup({ name, model, tools, effort }) {
  const kept = {}
  for (const k of LISTS) if (Array.isArray(tools?.[k]) && tools[k].length) kept[k] = [...tools[k]]
  return { id: '', name: cleanName(name), model, effort, tools: kept }
}

const sameSet = (a = [], b = []) => {
  const x = [...new Set(a)].sort()
  const y = [...new Set(b)].sort()
  return x.length === y.length && x.every((v, i) => v === y[i])
}

// Whether the composer differs from the setup it started from.
export function isEdited(setup, { model, tools, effort }) {
  // A setup saved before any model was known has no model to differ from.
  if (setup?.model && setup.model !== (model || '')) return true
  if ((setup?.effort || '') !== (effort || '')) return true
  return !LISTS.every((k) => sameSet(setup?.tools?.[k], tools?.[k]))
}

// The data with one more setup: gets an id and a name unique in the list.
export function withAdded(data, setup) {
  if (data.setups.length >= MAX_SETUPS) throw new RangeError('setups: limit')
  const names = new Set(data.setups.map((s) => s.name.toLowerCase()))
  const base = cleanName(setup.name) || 'Setup'
  let name = base
  for (let n = 2; names.has(name.toLowerCase()); n++) {
    const suffix = ` ${n}`
    name = `${clipName(base, MAX_NAME - suffix.length).trim()}${suffix}`
  }
  const id = setupId(name, data.setups.map((s) => s.id))
  return { ...data, setups: [...data.setups, { ...setup, id, name }] }
}

export function withReplaced(data, id, patch) {
  return { ...data, setups: data.setups.map((s) => (s.id === id ? { ...s, ...patch, id } : s)) }
}

export function withRemoved(data, id) {
  return { ...data, default: data.default === id ? '' : data.default, setups: data.setups.filter((s) => s.id !== id) }
}

// delta is -1 (up) or 1 (down); at an end the list stays as it is.
export function withMoved(data, id, delta) {
  const from = data.setups.findIndex((s) => s.id === id)
  const to = from + delta
  if (from < 0 || to < 0 || to >= data.setups.length) return data
  const setups = data.setups.slice()
  const [it] = setups.splice(from, 1)
  setups.splice(to, 0, it)
  return { ...data, setups }
}

// The default setup; an empty id or one that is not in the list clears it.
export function withDefault(data, id) {
  return { ...data, default: data.setups.some((s) => s.id === id) ? id : '' }
}

// The groups of the draft builtins list that are on, for the status line.
export function toolsOn(builtins) {
  return (builtins || []).filter((b) => !b.off).map((b) => b.group)
}
