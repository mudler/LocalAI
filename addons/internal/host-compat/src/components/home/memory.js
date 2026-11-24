// Memory figures for the Home status strip.

const K = 1024
const UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

function trim(value) {
  return String(parseFloat(value.toFixed(1)))
}

// "8.8 / 24 GB": both numbers in the unit of the total, so the pair reads at a
// glance and the unit is written once.
export function memoryFigure(used, total) {
  if (!(total > 0)) return null
  const i = Math.min(UNITS.length - 1, Math.max(0, Math.floor(Math.log(total) / Math.log(K))))
  const scale = Math.pow(K, i)
  return { used: trim((used || 0) / scale), total: trim(total / scale), unit: UNITS[i] }
}

export function percent(used, total) {
  if (!(total > 0)) return 0
  return Math.max(0, Math.min(100, (used / total) * 100))
}

// CSS custom property for a bar's fill, so the width lives in the stylesheet.
export function fillStyle(pct) {
  return { '--home-fill': `${pct}%` }
}

// What the strip reads for this host: the unified resources reading, which is
// GPU memory when a GPU is present and system RAM otherwise.
export function hostMemory(resources) {
  if (!resources) return null
  const aggregate = resources.aggregate || {}
  const gpus = Array.isArray(resources.gpus) ? resources.gpus : []
  const isGpu = resources.type === 'gpu' && gpus.length > 0
  const total = aggregate.total_memory || (isGpu ? 0 : resources.ram?.total) || 0
  const used = aggregate.used_memory ?? (isGpu ? 0 : resources.ram?.used) ?? 0
  if (!(total > 0)) return null
  const ram = resources.ram && resources.ram.total > 0 ? { used: resources.ram.used || 0, total: resources.ram.total } : null
  return {
    isGpu,
    used,
    total,
    pct: aggregate.usage_percent != null ? aggregate.usage_percent : percent(used, total),
    gpuCount: gpus.length,
    device: isGpu ? (gpus[0]?.name || '') : '',
    ram,
  }
}

// CSS custom property for the undo toast's countdown bar.
export function undoStyle(ms) {
  return { '--dk-undo': `${ms / 1000}s` }
}
