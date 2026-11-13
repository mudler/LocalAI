import test from 'node:test'
import assert from 'node:assert/strict'
import { cacheLabel, hasMoeOptions, isLlamaCppBackend, parseMoeOptions, setMoeOptions } from '../../src/utils/moePlacement.js'

const others = ['context_shift:true', 'parallel:2', 'devices:CUDA1,CUDA2']

test('moe options: a list with no expert entry means experts on the GPU', () => {
  assert.deepEqual(parseMoeOptions(undefined), { experts: 'gpu', layers: null, cacheMib: 0, elsewhere: [] })
  assert.deepEqual(parseMoeOptions(others), { experts: 'gpu', layers: null, cacheMib: 0, elsewhere: [] })
  assert.equal(hasMoeOptions(others), false)
})

test('moe options: cpu_moe is read in every spelling the backend accepts', () => {
  for (const entry of ['cpu_moe', 'cpu_moe:true', 'cpu_moe:1', 'cpu_moe:yes', 'cpu_moe:ON', 'cpu_moe:enabled']) {
    assert.equal(parseMoeOptions([entry]).experts, 'all', entry)
  }
  for (const entry of ['cpu_moe:false', 'cpu_moe:0', 'cpu_moe:off']) {
    assert.equal(parseMoeOptions([entry]).experts, 'gpu', entry)
  }
})

test('moe options: n_cpu_moe and moe_cache_mib are read as whole numbers', () => {
  assert.deepEqual(parseMoeOptions(['n_cpu_moe:12', 'moe_cache_mib:4096']), { experts: 'some', layers: 12, cacheMib: 4096, elsewhere: [] })
  assert.equal(hasMoeOptions(['moe_cache_mib:2048']), true)
})

test('moe options: entries this section cannot rewrite are reported as set elsewhere', () => {
  for (const list of [['--cpu-moe'], ['--n-cpu-moe:4'], ['n_cpu_moe:abc'], ['moe_cache_mib:1.5'], ['cpu_moe:maybe'],
    ['cpu_moe:true', 'n_cpu_moe:3'], ['n_cpu_moe:2', 'n_cpu_moe:3']]) {
    const parsed = parseMoeOptions(list)
    assert.equal(parsed.experts, 'other', JSON.stringify(list))
    assert.ok(parsed.elsewhere.length > 0)
  }
})

test('moe options: choosing all writes cpu_moe:true and keeps the other entries in order', () => {
  assert.deepEqual(setMoeOptions(others, { experts: 'all' }), [...others, 'cpu_moe:true'])
})

test('moe options: some layers writes n_cpu_moe:N and removes cpu_moe so the two never coexist', () => {
  const out = setMoeOptions(['parallel:2', 'cpu_moe:true', 'devices:CUDA1'], { experts: 'some', layers: 12 })
  assert.deepEqual(out, ['parallel:2', 'devices:CUDA1', 'n_cpu_moe:12'])
  const back = setMoeOptions(out, { experts: 'all' })
  assert.deepEqual(back, ['parallel:2', 'devices:CUDA1', 'cpu_moe:true'])
})

test('moe options: an entry that already says what is wanted is kept as written', () => {
  assert.deepEqual(setMoeOptions(['cpu_moe:1', 'parallel:2'], { experts: 'all', cacheMib: 4096 }), ['cpu_moe:1', 'parallel:2', 'moe_cache_mib:4096'])
})

test('moe options: a changed value is replaced in place, not moved', () => {
  assert.deepEqual(setMoeOptions(['n_cpu_moe:4', 'parallel:2'], { experts: 'some', layers: 9 }), ['n_cpu_moe:9', 'parallel:2'])
})

test('moe options: the cache is written for experts on the CPU and removed with Off', () => {
  assert.deepEqual(setMoeOptions(['cpu_moe:true'], { experts: 'all', cacheMib: 2 * 1024 }), ['cpu_moe:true', 'moe_cache_mib:2048'])
  assert.deepEqual(setMoeOptions(['cpu_moe:true', 'moe_cache_mib:2048', 'parallel:2'], { experts: 'all', cacheMib: 0 }), ['cpu_moe:true', 'parallel:2'])
})

test('moe options: moving experts back to the GPU removes the cache too, and an empty list is undefined', () => {
  assert.deepEqual(setMoeOptions(['parallel:2', 'cpu_moe:true', 'moe_cache_mib:4096'], { experts: 'gpu', cacheMib: 4096 }), ['parallel:2'])
  assert.equal(setMoeOptions(['cpu_moe:true', 'moe_cache_mib:4096'], { experts: 'gpu' }), undefined)
  assert.equal(setMoeOptions(undefined, { experts: 'gpu' }), undefined)
})

test('moe options: the GPU choice on a list without expert entries changes nothing', () => {
  assert.deepEqual(setMoeOptions(others, { experts: 'gpu' }), others)
})

test('moe options: cache sizes are labelled in GB when whole', () => {
  assert.equal(cacheLabel(4096), '4 GB')
  assert.equal(cacheLabel(1500), '1500 MiB')
})

test('backend: llama.cpp and its spellings and pinned variants', () => {
  for (const name of ['llama-cpp', 'llama.cpp', 'LLAMA-CPP', 'llama', 'go-llama',
    'cuda12-llama-cpp', 'vulkan-llama-cpp', 'metal-darwin-arm64-llama-cpp', 'cuda13-nvidia-l4t-arm64-llama-cpp',
    'llama-cpp-development', 'cpu-llama-cpp-quantization']) {
    assert.equal(isLlamaCppBackend(name), true, name)
  }
})

test('backend: unset resolves to llama.cpp for a GGUF file', () => {
  for (const unset of [undefined, null, '', '  ']) assert.equal(isLlamaCppBackend(unset), true)
})

test('backend: other engines, and the separate ik-llama-cpp, are not llama.cpp', () => {
  for (const name of ['vllm', 'mlx', 'whisper', 'transformers', 'stablediffusion-ggml', 'ik-llama-cpp',
    'cuda12-ik-llama-cpp', 'llama-cpp-rpc', 'cloud-proxy', 'llamafile']) {
    assert.equal(isLlamaCppBackend(name), false, name)
  }
})
