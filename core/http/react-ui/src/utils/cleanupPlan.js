// The cleanup review: which installed models are safe to remove, ranked by what
// the API really tells the page.
//
// The API records no last-used time and no use count per model, so nothing here
// is ranked by use. A model is judged on structure only:
//   - is it loaded, pinned, or named by an agent, a task, a failover chain or
//     an alias? Then it is protected and never suggested.
//   - is another build of the same gallery model installed too? Then the build
//     LocalAI would not pick on this host is a duplicate.
//   - is it disabled? Then its owner already turned it off.
//   - does the gallery list it? Then it can be downloaded again.
// Every function is pure so the rules can be tested without a page.

// How long a removal waits before anything is deleted. Long enough to notice a
// mistake after reading the toast, short enough that the disk is not held for
// minutes. The server has no hold, so this wait is the whole undo.
export const UNDO_MS = 30_000

// What can name a model in an agent's configuration.
const AGENT_MODEL_FIELDS = [
  'model', 'multimodal_model', 'transcription_model', 'tts_model',
  'embedding_model', 'plan_reviewer_model',
]

export function agentModels(config) {
  if (!config || typeof config !== 'object') return []
  const out = []
  for (const field of AGENT_MODEL_FIELDS) {
    const value = config[field]
    if (typeof value === 'string' && value.trim()) out.push(value.trim())
  }
  return [...new Set(out)]
}

// Everything that names a model, keyed by model id.
//   agents   [{ name, models: [id] }]
//   tasks    [{ name, model }]
//   chains   [{ name, targets: [{ model }] }]
//   aliases  { aliasName: targetId }
export function collectReferences({ agents = [], tasks = [], chains = [], aliases = {} } = {}) {
  const refs = new Map()
  const add = (model, ref) => {
    if (!model) return
    if (!refs.has(model)) refs.set(model, [])
    refs.get(model).push(ref)
  }
  for (const agent of agents) for (const model of agent.models || []) add(model, { kind: 'agent', name: agent.name })
  for (const task of tasks) add(task.model, { kind: 'task', name: task.name })
  for (const chain of chains) for (const target of chain.targets || []) add(target.model, { kind: 'chain', name: chain.name })
  for (const [alias, target] of Object.entries(aliases)) add(target, { kind: 'alias', name: alias })
  return refs
}

// Builds of one gallery model that are installed together.
//   installedIds     ids of every installed model
//   variantsByEntry  { entryId: { variants: [{ model }], auto_selected } } from
//                    the variants endpoint, for entries that declare variants
//
// Of a set installed together, the build the gallery would auto-select on this
// host stays; the rest are duplicates. When the auto-selected build is not one
// of them, the first listed stays, so exactly one build of a set is ever kept.
export function findDuplicates(installedIds, variantsByEntry = {}) {
  const installed = new Set(installedIds)
  const dup = new Map()
  for (const [entry, description] of Object.entries(variantsByEntry)) {
    const names = [entry, ...((description?.variants) || []).map(v => v.model)]
    const present = [...new Set(names)].filter(name => installed.has(name))
    if (present.length < 2) continue
    const auto = description?.auto_selected
    const keep = present.includes(auto) ? auto : present[0]
    for (const name of present) {
      if (name !== keep && !dup.has(name)) dup.set(name, { keep })
    }
  }
  return dup
}

// models      [{ id, disabled, pinned, source, running }]
// sizes       Map or object, id -> bytes. Only what the gallery reports.
// references  from collectReferences
// duplicates  from findDuplicates
// galleryIds  Set of ids the gallery lists, so the model can be downloaded again
// verified    false when the agent or task lookup failed. Then nothing may be
//             called safe, because something unseen might use it.
export function buildCleanupPlan({
  models,
  sizes = {},
  references = new Map(),
  duplicates = new Map(),
  galleryIds = new Set(),
  verified = true,
}) {
  const sizeOf = id => {
    const value = sizes instanceof Map ? sizes.get(id) : sizes[id]
    return typeof value === 'number' && value > 0 ? value : null
  }
  const groups = { safe: [], probably: [], call: [] }
  const protectedItems = []

  for (const model of models) {
    // A model only the cluster knows has no files on this host to remove.
    if (model.source === 'registry-only') continue
    const refs = references.get(model.id) || []
    const base = { id: model.id, backend: model.backend || '', size: sizeOf(model.id) }

    const why = []
    if (model.running) why.push({ key: 'running' })
    if (model.pinned) why.push({ key: 'pinned' })
    if (refs.length > 0) why.push({ key: 'usedBy', refs })
    if (why.length > 0) {
      protectedItems.push({ ...base, reasons: why })
      continue
    }

    const inGallery = galleryIds.has(model.id)
    const dup = duplicates.get(model.id)
    let tier
    let reason
    if (dup) {
      tier = 'safe'
      reason = { key: 'duplicate', keep: dup.keep }
    } else if (model.disabled) {
      tier = inGallery ? 'probably' : 'call'
      reason = { key: inGallery ? 'disabledGallery' : 'disabledLocal' }
    } else {
      tier = 'call'
      reason = { key: inGallery ? 'idleGallery' : 'idleLocal' }
    }
    // A lookup that failed leaves open that an agent or task uses the model.
    if (!verified && tier !== 'call') {
      tier = 'call'
      reason = { ...reason, unverified: true }
    } else if (!verified) {
      reason = { ...reason, unverified: true }
    }
    groups[tier].push({ ...base, reason })
  }

  const bySize = (a, b) => (b.size || 0) - (a.size || 0) || a.id.localeCompare(b.id)
  for (const tier of Object.keys(groups)) groups[tier].sort(bySize)
  protectedItems.sort(bySize)
  return { groups, protected: protectedItems }
}

// Sum of the known sizes of a list of items, and whether any size was unknown.
export function totalSize(items) {
  let bytes = 0
  let unknown = 0
  for (const item of items) {
    if (item.size) bytes += item.size
    else unknown += 1
  }
  return { bytes, unknown }
}
