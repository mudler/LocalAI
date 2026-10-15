// The numbers and rules behind the Placement section of the model editor: what
// `gpu_layers`, `tensor_split` and `main_gpu` mean, which devices a host has,
// how a choice sits in their memory, and how to search for the largest layer
// count that fits. Pure functions, so the rules can be read and tested without
// rendering anything.
//
// What the backend does with the three keys, as of this writing:
//  - gpu_layers unset: core/backend/options.go substitutes
//    config.DefaultNGPULayers, which is "offload every layer". The llama.cpp
//    backend then clamps that to what fits (its fit_params option is on unless
//    the model turns it off). So "Auto" means "all layers, trimmed to fit".
//  - gpu_layers 0: nothing is offloaded.
//  - gpu_layers N: N layers are requested.
//  - a negative value is read by llama.cpp as "all".
// The estimate endpoint takes a gpu_layers value too, but a zero (or absent)
// value means "all layers" there, so a CPU-only reading is never asked for.

import { FIT_LIMIT } from './modelLedger.js'

// The value LocalAI itself writes for "all layers" (config.DefaultNGPULayers).
export const ALL_LAYERS = 99999999

// How many layers to search through when the model's own layer count is not
// known. Past the real count the estimate stops changing, so a bound above any
// shipped model costs only a few more requests in the search.
export const LAYER_SEARCH_MAX = 256

export const CONTEXT_PRESETS = [2048, 4096, 8192, 16384, 32768, 65536, 131072]

export function contextLabel(value) {
  if (!Number.isFinite(value) || value <= 0) return ''
  if (value >= 1024 && value % 1024 === 0) return `${value / 1024}K`
  return String(value)
}

// 'auto' | 'cpu' | 'custom', from the value held in the editor.
export function placementMode(value) {
  if (value === undefined || value === null || value === '') return 'auto'
  const n = Number(value)
  if (!Number.isFinite(n)) return 'auto'
  if (n === 0) return 'cpu'
  return 'custom'
}

// A value that means every layer, however it was written.
export function isAllLayers(value) {
  const n = Number(value)
  return Number.isFinite(n) && (n < 0 || n >= ALL_LAYERS)
}

// The layer count to send to the estimate: null asks for all layers.
export function estimateLayers(mode, value) {
  if (mode !== 'custom') return null
  if (isAllLayers(value)) return null
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : null
}

// What a /api/resources reading says about the machine.
//
//   kind     'cluster' when the server schedules onto other nodes (the numbers
//            describe the controller, not where the model runs), 'gpu' or
//            'none'.
//   gpus     [{ index, name, total, used, free }]
//   ram      { total, free } in bytes, or null
export function devicesFrom(resources) {
  if (!resources) return { kind: 'unknown', gpus: [], ram: null }
  const gpus = (Array.isArray(resources.gpus) ? resources.gpus : []).map((g, i) => {
    const total = Number(g.total_vram) || 0
    const used = Math.min(total, Number(g.used_vram) || 0)
    const free = Number.isFinite(Number(g.free_vram)) && g.free_vram !== undefined
      ? Math.max(0, Number(g.free_vram))
      : Math.max(0, total - used)
    return { index: g.index ?? i, name: g.name || `GPU ${g.index ?? i}`, total, used, free }
  })
  const ramTotal = Number(resources.ram?.total) || 0
  const ramFree = Number(resources.ram?.available ?? resources.ram?.free) || 0
  const ram = ramTotal > 0 ? { total: ramTotal, free: Math.min(ramTotal, ramFree) } : null
  const kind = resources.cluster?.enabled ? 'cluster' : gpus.length > 0 ? 'gpu' : 'none'
  return { kind, gpus, ram }
}

// "3,1" to [3, 1]. Anything that is not a list of non-negative numbers is no
// split at all.
export function parseSplit(text) {
  if (typeof text !== 'string' || !text.trim()) return null
  const parts = text.split(',').map(s => Number(s.trim()))
  if (parts.length < 2 || parts.some(n => !Number.isFinite(n) || n < 0)) return null
  if (parts.every(n => n === 0)) return null
  return parts
}

// Shares that sum to one, from a split. With no split every device gets the
// same share, which is what llama.cpp does with its own default.
export function shares(count, split) {
  const parts = split && split.length === count ? split : new Array(count).fill(1)
  const sum = parts.reduce((a, b) => a + b, 0) || 1
  return parts.map(p => p / sum)
}

// The split that follows free memory, written as whole-number percentages.
// Fixed to sum to 100 so the YAML reads cleanly.
export function splitByFree(gpus) {
  const free = gpus.map(g => g.free)
  const sum = free.reduce((a, b) => a + b, 0)
  if (sum <= 0) return gpus.map(() => Math.round(100 / gpus.length))
  const raw = free.map(f => Math.round((f / sum) * 100))
  const drift = 100 - raw.reduce((a, b) => a + b, 0)
  raw[raw.indexOf(Math.max(...raw))] += drift
  return raw
}

export function formatSplit(parts) {
  return parts.map(p => String(Math.round(p))).join(',')
}

// The part of an estimate that grows with context, and the rest.
//
// The estimate endpoint returns one number per request. Its context term is
// linear, so two readings at different contexts give that term exactly:
// kv(ctx) = slope * ctx. Returns null when the two readings do not support it
// (equal, or the wrong way round), so the caller shows one undivided figure.
export function splitByContext(low, high) {
  if (!low || !high) return null
  const span = high.ctx - low.ctx
  const grow = high.bytes - low.bytes
  if (!(span > 0) || !(grow > 0)) return null
  const slope = grow / span
  const kv = Math.min(low.bytes, slope * low.ctx)
  return { kv, base: low.bytes - kv, slope }
}

// How a choice sits in the memory of this host.
//
//   mode       'auto' | 'cpu' | 'custom'
//   gpuBytes   what the estimate says the GPU holds for this choice
//   allBytes   the estimate with every layer on the GPU (also the whole model
//              when it runs on the CPU)
//   devices    devicesFrom()
//
// Returns { state, gpuNeed, cpuNeed, gpuFree, ramFree } where state is one of
//   'fits'      everything the GPU is asked to hold fits.
//   'spill'     custom count that fits, with the rest left on the CPU.
//   'trimmed'   auto, and all layers do not fit: the engine lowers the count.
//   'spill-over', 'trimmed-over'
//               the same, and what is left for the CPU is more than free
//               system memory.
//   'toomany'   custom count that does not fit the GPU.
//   'cpu'       CPU only, and the model fits in free memory.
//   'cpu-over'  CPU only, and the model is more than free memory.
//   'nogpu'     the host has no GPU, so the model runs on the CPU.
//   'nogpu-over' same, and it is more than free memory.
//   'unknown'   no estimate to judge by.
export function placementFit({ mode, gpuBytes, allBytes, devices }) {
  const gpus = devices?.gpus || []
  const ramFree = devices?.ram?.free ?? null
  const gpuFree = gpus.reduce((sum, g) => sum + g.free, 0)
  const limit = gpuFree * FIT_LIMIT
  const ramLimit = ramFree === null ? null : ramFree * FIT_LIMIT
  const none = { gpuNeed: 0, cpuNeed: 0, gpuFree, ramFree }

  // A cluster controller's device list describes the controller, not the node
  // the model will land on, and an unread machine says nothing at all.
  if (!(allBytes > 0) || devices?.kind === 'cluster' || devices?.kind === 'unknown') return { state: 'unknown', ...none }

  if (mode === 'cpu' || gpus.length === 0) {
    const noGpu = gpus.length === 0
    const over = ramLimit !== null && allBytes > ramLimit
    return {
      state: noGpu ? (over ? 'nogpu-over' : 'nogpu') : (over ? 'cpu-over' : 'cpu'),
      gpuNeed: 0,
      cpuNeed: allBytes,
      gpuFree,
      ramFree,
    }
  }

  // What does not go to the GPU goes to system memory, which has to hold it.
  const spilled = (state, gpuNeed, cpuNeed) => ({
    state: ramLimit !== null && cpuNeed > ramLimit ? `${state}-over` : state,
    gpuNeed,
    cpuNeed,
    gpuFree,
    ramFree,
  })

  const need = gpuBytes > 0 ? gpuBytes : allBytes
  if (mode === 'auto') {
    return need <= limit
      ? { state: 'fits', gpuNeed: need, cpuNeed: 0, gpuFree, ramFree }
      : spilled('trimmed', Math.min(need, limit), need - Math.min(need, limit))
  }

  if (need > limit) return { state: 'toomany', gpuNeed: need, cpuNeed: 0, gpuFree, ramFree }
  const cpuNeed = Math.max(0, allBytes - need)
  return cpuNeed > 0
    ? spilled('spill', need, cpuNeed)
    : { state: 'fits', gpuNeed: need, cpuNeed: 0, gpuFree, ramFree }
}

// Searches for the largest layer count whose estimate fits `limit` bytes.
//
//   estimate  async (layers | null) => bytes | null. null layers asks for every
//             layer; a null result means the estimate is unavailable.
//
// The model's own layer count is not in the estimate, so the search runs over
// 1..max. Above the real count the estimate is flat, which is what makes a
// plain bisection correct: it finds the last count at or under the limit.
//
// Returns one of
//   { kind: 'all', bytes }          every layer fits, nothing to lower.
//   { kind: 'layers', layers, bytes } the largest count that fits.
//   { kind: 'none', bytes }         even one layer does not fit.
//   { kind: 'flat', bytes }         layers do not change this estimate, and it
//                                   does not fit.
//   { kind: 'unavailable' }         the estimate could not be read.
export async function searchLayers(estimate, limit, max = LAYER_SEARCH_MAX) {
  const all = await estimate(null)
  if (all === null || !(all > 0)) return { kind: 'unavailable' }
  if (all <= limit) return { kind: 'all', bytes: all }
  const one = await estimate(1)
  if (one === null) return { kind: 'unavailable' }
  if (one >= all) return { kind: 'flat', bytes: all }
  if (one > limit) return { kind: 'none', bytes: one }
  let lo = 1
  let hi = max
  let loBytes = one
  while (hi - lo > 1) {
    const mid = Math.floor((lo + hi) / 2)
    const bytes = await estimate(mid)
    if (bytes === null) return { kind: 'unavailable' }
    if (bytes <= limit) { lo = mid; loBytes = bytes } else { hi = mid }
  }
  return { kind: 'layers', layers: lo, bytes: loBytes }
}
