// Capability levels for the agent editor. A level is a starting point: it sets
// fields every saved agent already has (the switches of the built-in groups
// and the list of grants) and nothing else. No React, so a node test drives it.
import { builtinsAfter } from './agentTools.js'
import { CONNECTOR_GRANTS, MEMORY_GRANT } from './chatAgents.js'
import { SAVED_AGENTS_GROUP } from './toolOptions.js'

const list = (x) => (Array.isArray(x) ? x : [])

// The built-in groups each level turns on; every other listed group is turned
// off. `all` means every group the server lists. Files and commands stay off
// below Full: the files group can change and delete files.
export const CAPABILITY_PRESETS = [
  { id: 'readonly', groups: ['web', 'media', 'collections', 'memory'], grants: 'none' },
  { id: 'assistant', groups: ['web', 'media', 'collections', 'memory', 'subagents', SAVED_AGENTS_GROUP, 'triggers', 'changes'], grants: 'connector' },
  { id: 'full', groups: 'all', grants: 'plain' },
]
export const PRESET_IDS = CAPABILITY_PRESETS.map((p) => p.id)

// The fields a level may change.
export const PRESET_KEYS = ['builtins_off', 'builtins_on', 'builtins']

const same = (a, b) => JSON.stringify([...new Set(list(a))].sort()) === JSON.stringify([...new Set(list(b))].sort())

// The fields of the form after a level is applied. `groups` are the built-in
// groups as the server lists them, `catalog` the grants it declares. A level
// never grants a sensitive right, and it keeps the grant of the memory choice
// and any grant this server does not declare (the person can still take those
// away by hand). Returns null while the lists are not known.
export function presetPatch(presetId, form, groups, catalog) {
  const preset = CAPABILITY_PRESETS.find((p) => p.id === presetId)
  const known = list(groups)
  if (!preset || known.length === 0) return null
  const names = known.map((g) => g.group)
  const on = preset.groups === 'all' ? names : names.filter((n) => preset.groups.includes(n))
  const off = names.filter((n) => !on.includes(n))
  let next = { ...form, ...builtinsAfter(form, known, on, true) }
  next = { ...next, ...builtinsAfter(next, known, off, false) }

  const declared = new Set(list(catalog).map((g) => g.name))
  const kept = list(form.builtins).filter((g) => g === MEMORY_GRANT || !declared.has(g))
  let grants = []
  if (preset.grants === 'connector') grants = CONNECTOR_GRANTS
  else if (preset.grants === 'plain') grants = list(catalog).filter((g) => !g.sensitive).map((g) => g.name)
  grants = [...new Set([...kept, ...grants.filter((g) => declared.has(g) || list(catalog).length === 0)])]
  // The saved-agents switch and its grant move together.
  if (!on.includes(SAVED_AGENTS_GROUP) && names.includes(SAVED_AGENTS_GROUP)) grants = grants.filter((g) => g !== SAVED_AGENTS_GROUP)
  return { builtins_off: next.builtins_off, builtins_on: next.builtins_on, builtins: grants }
}

// The level the form is at, or null for a custom mix.
export function activePreset(form, groups, catalog) {
  for (const p of CAPABILITY_PRESETS) {
    const patch = presetPatch(p.id, form, groups, catalog)
    if (patch && PRESET_KEYS.every((k) => same(patch[k], form[k]))) return p.id
  }
  return null
}

// The part of the form to keep so an applied level or template can be undone.
export function snapshot(form, keys) {
  return Object.fromEntries(keys.map((k) => [k, Array.isArray(form[k]) ? [...form[k]] : form[k]]))
}

// The fields a template fills, and the level it starts at (if any).
export const TEMPLATE_TEXT_KEYS = ['name', 'description', 'instructions']
