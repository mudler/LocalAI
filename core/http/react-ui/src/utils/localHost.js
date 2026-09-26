// Adapters that let a single-node install reuse the Nodes page's fleet
// widgets. The gauges and summaries there are written against worker heartbeat
// fields (total_vram, available_ram, cpu_usage_percent...), and GET
// /api/resources reports the same readings for this host under other names.
// Translating the host into one healthy "node" keeps a single implementation of
// the capacity maths instead of a second copy that drifts.

function finite(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function hostAsNode(resources) {
  if (!resources || typeof resources !== 'object') return null
  const node = { id: 'local', name: 'This machine', status: 'healthy' }

  const gpus = Array.isArray(resources.gpus) ? resources.gpus : []
  if (resources.type === 'gpu' && gpus.length > 0) {
    node.total_vram = gpus.reduce((sum, gpu) => sum + (finite(gpu?.total_vram) ?? 0), 0)
    node.available_vram = gpus.reduce((sum, gpu) => sum + (finite(gpu?.free_vram) ?? 0), 0)
  }

  const ram = resources.ram
  if (ram && finite(ram.total) > 0) {
    node.total_ram = ram.total
    // `available` counts reclaimable page cache, `free` does not. The worker
    // heartbeat reports `available`, so the gauges agree across both modes.
    node.available_ram = finite(ram.available) ?? finite(ram.free)
  }

  const cpu = resources.cpu
  if (cpu && finite(cpu.logical_cores) > 0) {
    node.cpu_logical_cores = cpu.logical_cores
    node.cpu_usage_percent = cpu.usage_percent
    node.cpu_load_1 = cpu.load_1
  }

  const disk = resources.disk
  if (disk && finite(disk.total) > 0) {
    node.total_disk = disk.total
    node.available_disk = disk.available
  }

  return node
}

// One row per loaded model from GET /system. The process block is absent when
// the model has no local process, so every derived number stays nullable
// rather than defaulting to a 0 that would read as "idle" or "empty".
export function localModelRows(systemInfo) {
  const loaded = Array.isArray(systemInfo?.loaded_models) ? systemInfo.loaded_models : []
  return loaded
    .filter(model => typeof model?.id === 'string' && model.id.trim())
    .map(model => {
      const proc = model.process || null
      return {
        model_name: model.id,
        backend: model.backend || '',
        pid: finite(proc?.pid),
        rss_bytes: finite(proc?.rss_bytes),
        memory_percent: finite(proc?.memory_percent),
        cpu_percent: finite(proc?.cpu_percent),
        started_at: proc?.started_at || null,
      }
    })
}

export function filterLocalModels(rows, query) {
  const needle = String(query || '').trim().toLowerCase()
  if (!needle) return rows
  return rows.filter(row => row.model_name.toLowerCase().includes(needle) || row.backend.toLowerCase().includes(needle))
}

// Unknown values sort last in both directions: a model with no reading is not
// the smallest one, it is the one we know least about.
export function sortLocalModels(rows, { key, direction }) {
  const factor = direction === 'desc' ? -1 : 1
  const value = row => (key === 'started_at' ? (row.started_at ? Date.parse(row.started_at) : null) : row[key])
  return [...rows].sort((left, right) => {
    const a = value(left)
    const b = value(right)
    if (a == null && b == null) return left.model_name.localeCompare(right.model_name)
    if (a == null) return 1
    if (b == null) return -1
    if (typeof a === 'string') return a.localeCompare(b) * factor
    return (a - b) * factor
  })
}

export function uptime(startedAt, now = Date.now()) {
  const started = startedAt ? Date.parse(startedAt) : NaN
  if (!Number.isFinite(started)) return null
  const seconds = Math.max(0, Math.floor((now - started) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ${minutes % 60}m`
  return `${Math.floor(hours / 24)}d ${hours % 24}h`
}
