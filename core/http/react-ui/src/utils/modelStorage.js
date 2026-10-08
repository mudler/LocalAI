// What the models directory holds, per installed model, from
// GET /api/models/storage (admin only). Pure functions, so the rules about
// shared files sit in one place and can be tested without a page.
//
// The server reports each model's files, the models that reference each file,
// and referenced files that are not on disk. A file two models reference is
// stored once: removing one of them leaves it behind.

// Index a storage report for lookups. Returns null for anything that is not a
// report, so a bad answer reads as "no report" rather than as zero bytes.
export function indexStorage(report) {
  if (!report || typeof report !== 'object' || !Array.isArray(report.models)) return null
  const files = Array.isArray(report.files) ? report.files : []
  const byName = new Map()
  for (const m of report.models) {
    if (!m || typeof m.name !== 'string') continue
    byName.set(m.name, {
      name: m.name,
      size: Number(m.size_bytes) || 0,
      shared: Number(m.shared_bytes) || 0,
      files: Array.isArray(m.files) ? m.files : [],
      missing: Array.isArray(m.missing) ? m.missing : [],
    })
  }
  const byPath = new Map()
  for (const f of files) {
    if (!f || typeof f.path !== 'string') continue
    byPath.set(f.path, {
      path: f.path,
      size: Number(f.size_bytes) || 0,
      missing: !!f.missing,
      models: Array.isArray(f.models) ? f.models : [],
    })
  }
  return { byName, byPath, total: Number(report.total_bytes) || 0, errors: Array.isArray(report.errors) ? report.errors : [] }
}

// The report's entry for a model, or null when the report says nothing useful
// about it: absent, or a config that references no files at all. A caller then
// falls back to the gallery's estimate.
export function diskEntry(index, name) {
  const entry = index?.byName.get(name)
  return entry && (entry.files.length > 0 || entry.missing.length > 0) ? entry : null
}

// Bytes that removing this model alone gives back: its files, minus the ones
// another configuration references too.
export function ownBytes(entry) {
  return entry ? Math.max(0, entry.size - entry.shared) : null
}

// The files of one model, largest first, each with the other models that
// reference it. Missing references come after the files that exist.
export function filesOf(index, name) {
  const entry = index?.byName.get(name)
  if (!entry) return []
  const rows = []
  for (const path of entry.files) {
    const f = index.byPath.get(path)
    rows.push({ path, size: f ? f.size : 0, missing: false, others: (f?.models || []).filter(m => m !== name) })
  }
  for (const path of entry.missing) {
    const f = index.byPath.get(path)
    rows.push({ path, size: 0, missing: true, others: (f?.models || []).filter(m => m !== name) })
  }
  return rows.sort((a, b) => Number(a.missing) - Number(b.missing) || b.size - a.size || a.path.localeCompare(b.path))
}

// The other models this one shares files with, and how many bytes they share,
// largest first. Empty when nothing is shared.
export function sharedWith(index, name) {
  const bytes = new Map()
  for (const f of filesOf(index, name)) {
    if (f.missing) continue
    for (const other of f.others) bytes.set(other, (bytes.get(other) || 0) + f.size)
  }
  return [...bytes.entries()]
    .map(([model, size]) => ({ model, bytes: size }))
    .sort((a, b) => b.bytes - a.bytes || a.model.localeCompare(b.model))
}

// Bytes a batch of removals gives back. A file counts only when every model
// that references it is in the batch, so two models that share a file give it
// back together and neither does alone. Returns null when the report does not
// know one of the models.
export function freedBy(index, names) {
  if (!index) return null
  const batch = new Set(names)
  const seen = new Set()
  let bytes = 0
  for (const name of batch) {
    const entry = index.byName.get(name)
    if (!entry) return null
    for (const path of entry.files) {
      if (seen.has(path)) continue
      seen.add(path)
      const f = index.byPath.get(path)
      if (f && f.models.every(m => batch.has(m))) bytes += f.size
    }
  }
  return bytes
}
