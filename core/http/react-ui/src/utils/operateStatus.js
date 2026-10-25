// What the Operate Status page says, worked out from what the API returns.
// Pure functions, so the rules can be read and tested without a page.
//
// Nothing here invents a reading. LocalAI keeps no memory history and no
// per-model GPU figure, so the chart is drawn from samples the page took
// itself (see appendSample), and a sample that cannot be read is skipped, not
// filled in.

import { diskState } from './modelLedger.js'

// A memory pool this full is worth a look. Below it the capacity row stays
// closed, like every row with nothing to say.
export const MEMORY_WARN = 0.9
export const MEMORY_ERROR = 0.97

// How many samples the page keeps. One sample comes with each summary poll
// (every 15 s), so this is about an hour. It is a bound, not a promise.
export const MAX_SAMPLES = 240

function positive(value) {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

// The pool the page measures: GPU memory on a host with a GPU, system memory
// otherwise, and the sum of the workers' GPU (or system) memory on a cluster.
// Returns null when no reading is available, so nothing gets drawn at zero.
export function capacityOf({ resources, nodes = [], distributed = false } = {}) {
  if (distributed) {
    // Workers that run models, and that are answering: a node that is down
    // reports a stale heartbeat, and its memory is not memory anyone can use.
    const readable = (Array.isArray(nodes) ? nodes : [])
      .filter(node => !node?.node_type || node.node_type === 'backend')
      .filter(node => !['unhealthy', 'offline', 'pending'].includes(String(node?.status || '').toLowerCase()))
    const sum = (totalField, freeField) => readable.reduce((acc, node) => {
      const total = positive(node?.[totalField])
      if (!total) return acc
      const free = Math.min(total, Math.max(0, Number(node?.[freeField]) || 0))
      return { total: acc.total + total, used: acc.used + (total - free), nodes: acc.nodes + 1 }
    }, { total: 0, used: 0, nodes: 0 })
    const gpu = sum('total_vram', 'available_vram')
    if (gpu.total > 0) return { kind: 'cluster-gpu', used: gpu.used, total: gpu.total, pct: (gpu.used / gpu.total) * 100, nodes: gpu.nodes }
    const ram = sum('total_ram', 'available_ram')
    if (ram.total > 0) return { kind: 'cluster-ram', used: ram.used, total: ram.total, pct: (ram.used / ram.total) * 100, nodes: ram.nodes }
    // No worker reports memory (they have not heartbeated yet), so the reading
    // of the machine that answered is the only one there is.
  }
  if (!resources || typeof resources !== 'object') return null
  const aggregate = resources.aggregate || {}
  const gpus = Array.isArray(resources.gpus) ? resources.gpus : []
  const isGpu = resources.type === 'gpu' && gpus.length > 0
  // The aggregate block is the server's own sum. Older readings carry only the
  // per-device figures, so those are summed here rather than reported as empty.
  const gpuTotal = gpus.reduce((sum, gpu) => sum + positive(gpu?.total_vram), 0)
  const gpuUsed = gpus.reduce((sum, gpu) => sum + positive(gpu?.used_vram), 0)
  const total = positive(aggregate.total_memory) || (isGpu ? gpuTotal : positive(resources.ram?.total))
  if (!total) return null
  const usedRaw = positive(aggregate.total_memory)
    ? (aggregate.used_memory ?? 0)
    : (isGpu ? gpuUsed : (resources.ram?.used ?? 0))
  const used = Math.max(0, Math.min(total, Number(usedRaw) || 0))
  return {
    kind: isGpu ? 'gpu' : 'ram',
    used,
    total,
    pct: (used / total) * 100,
    device: isGpu ? (gpus[0]?.name || '') : '',
    gpuCount: gpus.length,
  }
}

// Which pools are too full. `memory` is the pool from capacityOf, `disk` the
// models volume (single node only: a cluster controller's own volume says
// nothing about where an install lands).
export function capacityProblems({ resources, nodes = [], distributed = false } = {}) {
  const out = []
  const memory = capacityOf({ resources, nodes, distributed })
  if (memory && memory.pct >= MEMORY_WARN * 100) {
    out.push({ key: 'memory', level: memory.pct >= MEMORY_ERROR * 100 ? 'error' : 'warn', pct: memory.pct, memory })
  }
  if (!distributed) {
    const disk = diskState(resources)
    if (disk?.low) out.push({ key: 'disk', level: 'warn', disk })
  }
  return out
}

// Add a reading to the rolling buffer. Bounded, oldest first out. A reading
// with no total is dropped, and a sample taken at the same instant as the last
// one replaces it, so a double poll cannot draw a vertical line.
export function appendSample(samples, memory, now = Date.now(), max = MAX_SAMPLES) {
  if (!memory || !(memory.total > 0)) return samples
  const next = { t: now, used: memory.used, total: memory.total, kind: memory.kind }
  const last = samples[samples.length - 1]
  const base = last && last.t >= now ? samples.slice(0, -1) : samples
  const grown = [...base, next]
  return grown.length > max ? grown.slice(grown.length - max) : grown
}

// The line for the capacity chart. The y axis runs from zero to the capacity,
// or to the highest reading when that is above it, so the line is never
// flattered by a cut-off axis. The x axis is the time the samples cover.
export function chartGeometry(samples, { width = 640, height = 190, left = 40, right = 116, top = 16, bottom = 26 } = {}) {
  if (!Array.isArray(samples) || samples.length < 2) return null
  const first = samples[0].t
  const last = samples[samples.length - 1].t
  const span = last - first
  if (!(span > 0)) return null
  const capacity = samples[samples.length - 1].total
  const peak = Math.max(capacity, ...samples.map(s => s.used))
  const plotW = width - left - right
  const plotH = height - top - bottom
  const x = t => left + ((t - first) / span) * plotW
  const y = bytes => top + plotH - (Math.max(0, bytes) / peak) * plotH
  const points = samples.map(s => ({ x: x(s.t), y: y(s.used), t: s.t, used: s.used }))
  const path = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ')
  // Fewer labels when the plot is narrow, so they never touch.
  const steps = plotW > 380 ? 3 : 2
  const xTicks = Array.from({ length: steps + 1 }, (_, i) => {
    const t = first + (span * i) / steps
    return { x: x(t), t }
  })
  return {
    width,
    height,
    left,
    right,
    top,
    plotW,
    plotH,
    baseline: top + plotH,
    capacityY: y(capacity),
    capacity,
    peak,
    midY: y(peak / 2),
    mid: peak / 2,
    points,
    path,
    end: points[points.length - 1],
    xTicks,
    spanMs: span,
  }
}

// The words above the ledger. `needs` counts what a person has to act on:
// failed operations, backend updates, unhealthy nodes. `looking` is for a page
// where nothing needs an action but another row (memory, failed requests) has
// something to read.
export function statusHeadline({ needs = 0, looking = 0, firstRun = false } = {}) {
  if (firstRun) return { kind: 'firstRun', count: 0 }
  if (needs > 0) return { kind: 'needs', count: needs }
  if (looking > 0) return { kind: 'looking', count: looking }
  return { kind: 'ok', count: 0 }
}

// A first run: no backend, no model, nothing loaded, nothing in flight. Every
// input has to be known (not null) before this says so, so a page still
// waiting on an answer never claims to be empty.
export function isFirstRun({ backends, models, operations = [], loaded = 0 } = {}) {
  if (backends == null || models == null) return false
  return backends === 0 && models === 0 && operations.length === 0 && !(loaded > 0)
}

// How each row of the ledger stands. `open` is only ever true for a problem:
// a row with nothing to say stays one line.
export function ledgerRows({ attention = [], memoryProblems = [], requestErrors = 0, running = 0, loaded = 0 } = {}) {
  const hasError = attention.some(item => item.kind === 'operation-failed' || item.kind === 'node-unhealthy')
  const needsLevel = attention.length === 0 ? 'ok' : (hasError ? 'error' : 'warn')
  const capacityLevel = memoryProblems.length === 0
    ? 'ok'
    : (memoryProblems.some(p => p.level === 'error') ? 'error' : 'warn')
  const failuresLevel = requestErrors > 0 ? 'warn' : 'ok'
  return [
    { id: 'needs', level: needsLevel, open: needsLevel !== 'ok' },
    { id: 'capacity', level: capacityLevel, open: capacityLevel !== 'ok' },
    // Work in flight is not a problem, so this row never opens by itself.
    { id: 'running', level: running > 0 || loaded > 0 ? 'active' : 'idle', open: false },
    { id: 'failures', level: failuresLevel, open: failuresLevel !== 'ok' },
  ]
}

// What removing a backend would leave without a runtime. The installed list
// says which meta backend points at which concrete one
// (Metadata.meta_backend_for), and each configured model names the backend it
// asks for, so both can be read without a new call.
//
//   models      [{ id, backend }] from /api/models/capabilities
//   installed   the list from GET /backends
//
// Returns the model ids and the meta backends that would stop resolving.
export function backendDependents(name, { models = [], installed = [] } = {}) {
  if (!name) return { models: [], metas: [] }
  const metas = installed
    .filter(b => b?.Name && b.Name !== name && b.Metadata?.meta_backend_for === name)
    .map(b => b.Name)
  const asked = new Set([name, ...metas])
  const using = models
    .filter(m => m?.id && m.backend && asked.has(m.backend))
    .map(m => m.id)
  return { models: using.sort((a, b) => a.localeCompare(b)), metas: metas.sort((a, b) => a.localeCompare(b)) }
}

// The backend the first-run screen suggests. llama-cpp runs the most models, so
// it is named when the gallery lists it; the gallery's first entry otherwise.
export function recommendedBackend(catalog = []) {
  const open = catalog.filter(b => !b.installed)
  return open.find(b => (b.name || b.id) === 'llama-cpp') || open.find(b => b.isMeta && !b.isDevelopment) || open[0] || null
}
