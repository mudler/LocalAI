import assert from 'node:assert/strict'
import test from 'node:test'

import {
  ALL_LAYERS, devicesFrom, estimateLayers, formatSplit, isAllLayers, parseSplit, placementFit, placementMode,
  searchLayers, shares, splitByContext, splitByFree,
} from './placement.js'

const GB = 1024 * 1024 * 1024

const gpu = (total, used = 0) => ({ total_vram: total * GB, used_vram: used * GB, free_vram: (total - used) * GB })
const host = (gpus, ram = { total: 64 * GB, free: 48 * GB }) => devicesFrom({ gpus, ram })

test('the mode comes from gpu_layers: unset is auto, zero is CPU only, anything else is custom', () => {
  assert.equal(placementMode(undefined), 'auto')
  assert.equal(placementMode(null), 'auto')
  assert.equal(placementMode(''), 'auto')
  assert.equal(placementMode(0), 'cpu')
  assert.equal(placementMode('0'), 'cpu')
  assert.equal(placementMode(12), 'custom')
  assert.equal(placementMode(ALL_LAYERS), 'custom')
  assert.equal(placementMode(-1), 'custom')
})

test('all layers is the value LocalAI uses by default, and a negative count', () => {
  assert.equal(ALL_LAYERS, 99999999)
  assert.equal(isAllLayers(99999999), true)
  assert.equal(isAllLayers(-1), true)
  assert.equal(isAllLayers(40), false)
  assert.equal(isAllLayers(undefined), false)
})

test('the estimate is asked for every layer unless a partial count is set', () => {
  assert.equal(estimateLayers('auto', undefined), null)
  assert.equal(estimateLayers('cpu', 0), null)
  assert.equal(estimateLayers('custom', ALL_LAYERS), null)
  assert.equal(estimateLayers('custom', 12), 12)
  assert.equal(estimateLayers('custom', '12'), 12)
})

test('devices come from the resources reading, with free memory taken as reported', () => {
  const devices = host([{ index: 0, name: 'A', ...gpu(24, 2.6) }, { index: 1, name: 'B', ...gpu(12, 0.4) }])
  assert.equal(devices.kind, 'gpu')
  assert.equal(devices.gpus.length, 2)
  assert.equal(Math.round(devices.gpus[0].free / GB * 10) / 10, 21.4)
  assert.equal(devices.ram.free, 48 * GB)
})

test('no GPU, a cluster and an unread machine are told apart', () => {
  assert.equal(host([]).kind, 'none')
  assert.equal(devicesFrom(null).kind, 'unknown')
  assert.equal(devicesFrom({ gpus: [gpu(24)], cluster: { enabled: true } }).kind, 'cluster')
})

test('a split is a list of non-negative numbers, and an even split is no split at all', () => {
  assert.deepEqual(parseSplit('65,35'), [65, 35])
  assert.deepEqual(parseSplit('3, 1'), [3, 1])
  assert.equal(parseSplit('65'), null)
  assert.equal(parseSplit('a,b'), null)
  assert.equal(parseSplit('0,0'), null)
  assert.equal(parseSplit(undefined), null)
  assert.deepEqual(shares(2, null), [0.5, 0.5])
  assert.deepEqual(shares(2, [3, 1]), [0.75, 0.25])
  assert.deepEqual(shares(3, [1, 1]), [1 / 3, 1 / 3, 1 / 3])
  assert.equal(formatSplit([65, 35]), '65,35')
})

test('splitting by free memory gives whole percentages that sum to 100', () => {
  const devices = host([{ ...gpu(24, 2.6) }, { ...gpu(12, 0.4) }])
  const parts = splitByFree(devices.gpus)
  assert.deepEqual(parts, [65, 35])
  const three = splitByFree(host([gpu(10), gpu(10), gpu(10)]).gpus)
  assert.equal(three.reduce((a, b) => a + b, 0), 100)
})

test('the part of an estimate that grows with context is read from two lengths', () => {
  // weights 10 GB, 0.5 GB of KV cache per 1K tokens
  const at = ctx => 10 * GB + (0.5 * GB * ctx) / 1024
  const parts = splitByContext({ ctx: 8192, bytes: at(8192) }, { ctx: 16384, bytes: at(16384) })
  assert.equal(Math.round(parts.kv / GB * 100) / 100, 4)
  assert.equal(Math.round(parts.base / GB * 100) / 100, 10)
  assert.equal(splitByContext({ ctx: 8192, bytes: 5 }, { ctx: 16384, bytes: 5 }), null)
  assert.equal(splitByContext(null, { ctx: 8192, bytes: 5 }), null)
  assert.equal(splitByContext({ ctx: 16384, bytes: 5 }, { ctx: 8192, bytes: 9 }), null)
})

test('fit: everything the GPU holds fits, and the leftover is the CPU part of a custom count', () => {
  const devices = host([gpu(24)])
  assert.equal(placementFit({ mode: 'auto', gpuBytes: 10 * GB, allBytes: 10 * GB, devices }).state, 'fits')
  assert.equal(placementFit({ mode: 'custom', gpuBytes: 10 * GB, allBytes: 10 * GB, devices }).state, 'fits')
  const spill = placementFit({ mode: 'custom', gpuBytes: 10 * GB, allBytes: 16 * GB, devices })
  assert.equal(spill.state, 'spill')
  assert.equal(spill.cpuNeed, 6 * GB)
})

test('fit: auto that does not fit is trimmed by the engine, custom that does not fit is too many', () => {
  const devices = host([gpu(8)])
  const auto = placementFit({ mode: 'auto', gpuBytes: 12 * GB, allBytes: 12 * GB, devices })
  assert.equal(auto.state, 'trimmed')
  assert.ok(auto.cpuNeed > 0)
  assert.equal(placementFit({ mode: 'custom', gpuBytes: 12 * GB, allBytes: 12 * GB, devices }).state, 'toomany')
})

test('fit: what is left for the CPU has to fit in free system memory too', () => {
  const tight = host([gpu(8)], { total: 16 * GB, free: 4 * GB })
  // 12 GB asked, 7.6 usable on the GPU: 4.4 GB left, 3.8 usable in memory
  const auto = placementFit({ mode: 'auto', gpuBytes: 12 * GB, allBytes: 12 * GB, devices: tight })
  assert.equal(auto.state, 'trimmed-over')
  const spill = placementFit({ mode: 'custom', gpuBytes: 6 * GB, allBytes: 12 * GB, devices: tight })
  assert.equal(spill.state, 'spill-over')
  const roomy = host([gpu(8)], { total: 64 * GB, free: 48 * GB })
  assert.equal(placementFit({ mode: 'custom', gpuBytes: 6 * GB, allBytes: 12 * GB, devices: roomy }).state, 'spill')
  assert.equal(placementFit({ mode: 'auto', gpuBytes: 12 * GB, allBytes: 12 * GB, devices: roomy }).state, 'trimmed')
})

test('fit: the limit is 95 percent of what is free, not of the card', () => {
  const devices = host([gpu(24, 14)])
  // 10 GB free, 9.5 usable
  assert.equal(placementFit({ mode: 'custom', gpuBytes: 9.4 * GB, allBytes: 9.4 * GB, devices }).state, 'fits')
  assert.equal(placementFit({ mode: 'custom', gpuBytes: 9.6 * GB, allBytes: 9.6 * GB, devices }).state, 'toomany')
})

test('fit: CPU only is judged against free system memory, with or without a GPU', () => {
  const small = host([gpu(24)], { total: 32 * GB, free: 8 * GB })
  assert.equal(placementFit({ mode: 'cpu', gpuBytes: 0, allBytes: 6 * GB, devices: small }).state, 'cpu')
  assert.equal(placementFit({ mode: 'cpu', gpuBytes: 0, allBytes: 9 * GB, devices: small }).state, 'cpu-over')
  const none = host([], { total: 32 * GB, free: 8 * GB })
  assert.equal(placementFit({ mode: 'auto', gpuBytes: 6 * GB, allBytes: 6 * GB, devices: none }).state, 'nogpu')
  assert.equal(placementFit({ mode: 'auto', gpuBytes: 9 * GB, allBytes: 9 * GB, devices: none }).state, 'nogpu-over')
})

test('fit says nothing without an estimate, on a cluster or on an unread machine', () => {
  assert.equal(placementFit({ mode: 'auto', gpuBytes: 0, allBytes: 0, devices: host([gpu(24)]) }).state, 'unknown')
  assert.equal(placementFit({ mode: 'auto', gpuBytes: GB, allBytes: GB, devices: devicesFrom({ gpus: [gpu(24)], cluster: { enabled: true } }) }).state, 'unknown')
  assert.equal(placementFit({ mode: 'auto', gpuBytes: GB, allBytes: GB, devices: devicesFrom(null) }).state, 'unknown')
})

// An estimate like the server's: weights scale with the layers on the GPU, up
// to the model's own count, and the context term is fixed.
const estimator = (layerCount, weights, kv, calls = []) => async (layers) => {
  calls.push(layers)
  const share = layers === null || layers >= layerCount ? 1 : layers / layerCount
  return Math.round(weights * share + kv)
}

test('the layer search finds the largest count under the limit', async () => {
  const calls = []
  const result = await searchLayers(estimator(40, 16 * GB, 1 * GB, calls), 9 * GB)
  // 16 * n / 40 + 1 <= 9  =>  n <= 20
  assert.deepEqual({ kind: result.kind, layers: result.layers }, { kind: 'layers', layers: 20 })
  assert.ok(result.bytes <= 9 * GB)
  assert.ok(calls.length < 14, `asked ${calls.length} times`)
})

test('the layer search stops at once when every layer fits', async () => {
  const calls = []
  const result = await searchLayers(estimator(40, 16 * GB, 1 * GB, calls), 30 * GB)
  assert.equal(result.kind, 'all')
  assert.equal(calls.length, 1)
})

test('the layer search says so when not even one layer fits', async () => {
  const result = await searchLayers(estimator(40, 16 * GB, 12 * GB), 9 * GB)
  assert.equal(result.kind, 'none')
})

test('the layer search says so when layers do not change the estimate', async () => {
  const result = await searchLayers(async () => 20 * GB, 9 * GB)
  assert.equal(result.kind, 'flat')
})

test('the layer search gives up when the estimate cannot be read', async () => {
  assert.equal((await searchLayers(async () => null, 9 * GB)).kind, 'unavailable')
  let first = true
  const flaky = async () => { if (first) { first = false; return 20 * GB } return null }
  assert.equal((await searchLayers(flaky, 9 * GB)).kind, 'unavailable')
})

test('the layer search works for a model with more layers than the search bound would suggest', async () => {
  const result = await searchLayers(estimator(200, 100 * GB, 0), 50 * GB)
  assert.equal(result.layers, 100)
})
