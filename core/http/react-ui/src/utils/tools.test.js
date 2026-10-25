import assert from 'node:assert/strict'
import test from 'node:test'

import {
  appendLog, blocked, detectSource, fineTuneChecks, fitFor, importChecks, importRequest, jobToSurface,
  joinCommands, logLines, logLevel, LOG_LIMIT, looksLikeMemoryFailure, machineFacts, quantizeChecks,
  requestText, shortToken, sortNetworks, toolStatus, warned, workerCount,
} from './tools.js'

const GB = 1024 ** 3
const gpuHost = {
  type: 'gpu',
  gpus: [{ name: 'RTX 4070', total_vram: 12 * GB, free_vram: 9 * GB }],
  ram: { total: 32 * GB, available: 20 * GB, free: 10 * GB },
  disk: { total: 500 * GB, available: 118 * GB },
}
const cpuHost = { type: 'ram', gpus: null, ram: { total: 32 * GB, free: 12 * GB }, disk: { total: 500 * GB, available: 2 * GB } }

test('machineFacts reads the host and leaves unknowns null', () => {
  const f = machineFacts(gpuHost)
  assert.equal(f.hasGpu, true)
  assert.equal(f.gpuName, 'RTX 4070')
  assert.equal(f.vramFree, 9 * GB)
  assert.equal(f.ramFree, 20 * GB)
  assert.equal(f.diskFree, 118 * GB)
  const c = machineFacts(cpuHost)
  assert.equal(c.hasGpu, false)
  assert.equal(c.vramFree, null)
  assert.equal(c.ramFree, 12 * GB)
  assert.equal(machineFacts(null), null)
  assert.equal(machineFacts({}).diskFree, null)
})

test('machineFacts names several GPUs and reads the cluster block', () => {
  const f = machineFacts({ ...gpuHost, gpus: [gpuHost.gpus[0], gpuHost.gpus[0]], cluster: { enabled: true, total_memory: 80 * GB, node_name: 'w1', is_gpu: true, node_count: 3 } })
  assert.equal(f.gpuName, 'RTX 4070 x2')
  assert.equal(f.vramTotal, 24 * GB)
  assert.deepEqual(f.cluster, { node: 'w1', total: 80 * GB, isGpu: true, nodes: 3 })
})

test('toolStatus: quantize and fine-tune say what is missing and why', () => {
  const facts = machineFacts(cpuHost)
  assert.equal(toolStatus('quantize', { facts, backends: { quantize: [] } }).state, 'needs-backend')
  const q = toolStatus('quantize', { facts, backends: { quantize: [{ name: 'llama-cpp-quantization' }] } })
  assert.equal(q.state, 'ready')
  assert.equal(q.needs[0].vals.name, 'llama-cpp-quantization')
  const none = toolStatus('fine-tune', { facts, backends: { fineTune: [{ name: 'trl' }] } })
  assert.equal(none.state, 'ready')
  assert.ok(none.needs.some(n => n.key === 'fineTune.noGpu' && n.tone === 'warn'))
  const gpu = toolStatus('fine-tune', { facts: machineFacts(gpuHost), backends: { fineTune: [{ name: 'trl' }] } })
  assert.ok(gpu.needs.some(n => n.key === 'fineTune.gpu' && n.vals.free === 9 * GB))
  assert.equal(toolStatus('fine-tune', { facts: null, backends: { fineTune: null } }).needs[0].key, 'fineTune.backendUnknown')
  assert.ok(toolStatus('import', { facts, backends: {} }).needs.some(n => n.key === 'import.disk'))
})

test('jobToSurface prefers a running job, then a newest failure', () => {
  const stages = ['queued', 'training']
  const jobs = [
    { id: 'a', status: 'completed', created_at: '2026-10-01T10:00:00Z' },
    { id: 'b', status: 'training', created_at: '2026-10-02T10:00:00Z' },
    { id: 'c', status: 'failed', created_at: '2026-10-03T10:00:00Z' },
  ]
  assert.deepEqual(jobToSurface(jobs, stages), { job: jobs[1], kind: 'running' })
  assert.deepEqual(jobToSurface([jobs[0], jobs[2]], stages), { job: jobs[2], kind: 'failed' })
  assert.equal(jobToSurface([jobs[2], { id: 'd', status: 'completed', created_at: '2026-10-04T10:00:00Z' }], stages), null)
  assert.equal(jobToSurface(null, stages), null)
})

test('log lines come from what the event holds and nothing else', () => {
  const at = new Date(2026, 9, 8, 10, 2, 11)
  const first = logLines({ status: 'training', message: 'Loading model', current_step: 0, total_steps: 900 }, null, at)
  assert.deepEqual(first.map(l => l.text), ['training', 'Loading model'])
  const same = logLines({ status: 'training', message: 'Loading model' }, { status: 'training', message: 'Loading model' }, at)
  assert.deepEqual(same, [])
  const step = logLines({ status: 'training', message: '', current_step: 10, total_steps: 900, loss: 2.2871, learning_rate: 0.0001 }, { status: 'training', current_step: 0 }, at)
  assert.equal(step[0].text, 'step 10/900 loss 2.2871 lr 1.0e-4')
  const failed = logLines({ status: 'failed', message: 'CUDA out of memory' }, { status: 'training' }, at)
  assert.deepEqual(failed.map(l => l.level), ['err', 'err'])
})

test('logLevel flags warnings and failures from the message', () => {
  assert.equal(logLevel({ status: 'training', message: 'Warning: checkpoint slow' }), 'warn')
  assert.equal(logLevel({ status: 'training', message: 'torch.OutOfMemoryError: CUDA out of memory' }), 'err')
  assert.equal(logLevel({ status: 'training', message: 'step 3' }), 'info')
})

test('appendLog keeps the newest lines', () => {
  const lines = Array.from({ length: LOG_LIMIT + 5 }, (_, i) => ({ at: new Date(), level: 'info', tag: 'info', text: String(i) }))
  const out = appendLog([], lines)
  assert.equal(out.length, LOG_LIMIT)
  assert.equal(out[out.length - 1].text, String(LOG_LIMIT + 4))
  assert.equal(appendLog(out, []), out)
})

test('looksLikeMemoryFailure reads the server message', () => {
  assert.equal(looksLikeMemoryFailure('CUDA out of memory. Tried to allocate 1.34 GiB'), true)
  assert.equal(looksLikeMemoryFailure('dataset not found'), false)
})

const form = { model: 'TinyLlama/TinyLlama-1.1B-Chat-v1.0', datasetSource: 'tatsu-lab/alpaca', backend: 'trl', batchSize: 2, maxSeqLength: 2048, gradAccum: 4, gradCheckpointing: false, trainingType: 'lora', trainingMethod: 'sft', rewardCount: 0 }

test('fine-tune checks block on a missing model or dataset and warn on the rest', () => {
  const facts = machineFacts(gpuHost)
  const ok = fineTuneChecks({ form, facts, backends: { fineTune: [{ name: 'trl' }] } })
  assert.equal(blocked(ok), false)
  assert.equal(ok.find(c => c.id === 'memory').vals.batch, 2)
  assert.ok(ok.find(c => c.id === 'disk').meter.used > 0)
  const bad = fineTuneChecks({ form: { ...form, model: '', datasetSource: '' }, facts, backends: { fineTune: [{ name: 'trl' }] } })
  assert.equal(blocked(bad), true)
  assert.deepEqual(bad.filter(c => c.tone === 'fail').map(c => c.id).sort(), ['data', 'model'])
  const missing = fineTuneChecks({ form, facts: machineFacts(cpuHost), backends: { fineTune: [] } })
  assert.equal(blocked(missing), false)
  assert.ok(warned(missing) >= 3)
  assert.equal(missing.find(c => c.id === 'backend').action, 'backends')
  assert.equal(missing.find(c => c.id === 'disk').key, 'fineTune.diskLow')
})

test('fine-tune checks follow the form as it changes', () => {
  const facts = machineFacts(gpuHost)
  const grpo = fineTuneChecks({ form: { ...form, trainingMethod: 'grpo' }, facts, backends: { fineTune: [{ name: 'trl' }] } })
  assert.ok(grpo.some(c => c.id === 'reward'))
  const withReward = fineTuneChecks({ form: { ...form, trainingMethod: 'grpo', rewardCount: 2 }, facts, backends: { fineTune: [{ name: 'trl' }] } })
  assert.ok(!withReward.some(c => c.id === 'reward'))
  const file = fineTuneChecks({ form: { ...form, datasetSource: '', datasetFileName: 'data.jsonl' }, facts, backends: null })
  assert.equal(file.find(c => c.id === 'data').tone, 'ok')
  assert.equal(file.find(c => c.id === 'backend').tone, 'info')
  assert.equal(fineTuneChecks({ form: { ...form, hfToken: 'x' }, facts: null, backends: null }).find(c => c.id === 'access').tone, 'ok')
})

test('quantize checks need a model, a custom type when asked, and say about the backend', () => {
  const facts = machineFacts(cpuHost)
  const base = { model: 'meta-llama/Llama-3.2-1B', backend: 'llama-cpp-quantization', useCustom: false, customType: '' }
  const ok = quantizeChecks({ form: base, facts, backends: { quantize: [{ name: 'llama-cpp-quantization' }] } })
  assert.equal(blocked(ok), false)
  assert.ok(ok.some(c => c.id === 'memory'))
  const custom = quantizeChecks({ form: { ...base, useCustom: true }, facts, backends: { quantize: [{ name: 'llama-cpp-quantization' }] } })
  assert.equal(blocked(custom), true)
  assert.equal(blocked(quantizeChecks({ form: { ...base, model: ' ' }, facts, backends: null })), true)
})

test('detectSource reads the spelling of each source', () => {
  assert.deepEqual(detectSource('huggingface://Qwen/Qwen3-8B'), { kind: 'huggingface', ref: 'Qwen/Qwen3-8B', file: '', ok: true, scheme: 'huggingface' })
  assert.equal(detectSource('hf://owner/repo').kind, 'huggingface')
  const url = detectSource('https://huggingface.co/owner/repo/resolve/main/model.Q4_K_M.gguf')
  assert.equal(url.kind, 'huggingface')
  assert.equal(url.file, 'model.Q4_K_M.gguf')
  assert.equal(detectSource('owner/repo').kind, 'huggingface')
  assert.equal(detectSource('https://example.com/model.gguf').file, 'model.gguf')
  assert.equal(detectSource('https://example.com/config.yaml').kind, 'config')
  assert.equal(detectSource('oci://registry.example.com/model:tag').kind, 'oci')
  assert.equal(detectSource('ollama://llama3.2:3b').ref, 'llama3.2:3b')
  assert.equal(detectSource('file:///models/model.gguf').kind, 'local')
  assert.equal(detectSource('/models/config.yml').kind, 'config')
  assert.equal(detectSource('hf://onlyone').ok, false)
  assert.equal(detectSource('nonsense').kind, 'unknown')
  assert.equal(detectSource('   '), null)
})

test('importRequest builds the request the form sends, and the preview prints it', () => {
  const prefs = { backend: '', name: ' my-model ', description: '', quantizations: 'q4_k_m', mmproj_quantizations: '', embeddings: true, type: '', pipeline_type: '', scheduler_type: '', enable_parameters: '', cuda: false }
  const req = importRequest(' hf://o/r ', prefs, [{ key: 'x', value: 'y' }, { key: '', value: 'z' }])
  assert.deepEqual(req, { uri: 'hf://o/r', preferences: { name: 'my-model', quantizations: 'q4_k_m', embeddings: 'true', x: 'y' } })
  assert.equal(importRequest('hf://o/r', { ...prefs, name: '', quantizations: '', embeddings: false }, []).preferences, null)
  assert.equal(importRequest('hf://o/r', { ...prefs }, [], 'piper').preferences.backend, 'piper')
  assert.equal(requestText(req), 'uri: hf://o/r\npreferences:\n  name: my-model\n  quantizations: q4_k_m\n  embeddings: "true"\n  x: y')
  assert.match(requestText(importRequest('hf://o/r', { ...prefs, name: '', quantizations: '', embeddings: false }, [])), /preferences: none/)
})

test('importChecks state only what is known before the import starts', () => {
  const facts = machineFacts(gpuHost)
  const prefs = { backend: 'vllm', name: 'taken' }
  const backends = [{ name: 'vllm', installed: false }, { name: 'llama-cpp', installed: true }]
  const checks = importChecks({ source: detectSource('hf://o/r'), prefs, backends, installedNames: ['taken'], facts })
  const by = (id) => checks.find(c => c.id === id)
  assert.equal(by('source').tone, 'ok')
  assert.equal(by('backend').key, 'import.backendDownload')
  assert.equal(by('name').tone, 'warn')
  assert.equal(by('disk').vals.free, 118 * GB)
  assert.equal(by('memory').vals.where, 'gpu')
  assert.equal(importChecks({ source: null, prefs, backends: [], installedNames: [], facts }).length, 0)
  const auto = importChecks({ source: detectSource('nonsense'), prefs: { backend: '', name: '' }, backends: [], installedNames: [], facts: null })
  assert.deepEqual(auto.map(c => c.key), ['import.sourceUnknown', 'import.backendAuto', 'import.nameAuto'])
})

test('fitFor compares the import estimate with free memory', () => {
  assert.equal(fitFor(4 * GB, 10 * GB), 'fits')
  assert.equal(fitFor(9 * GB, 10 * GB), 'tight')
  assert.equal(fitFor(11 * GB, 10 * GB), 'over')
  assert.equal(fitFor(0, 10 * GB), null)
  assert.equal(fitFor(4 * GB, null), null)
})

test('explorer helpers shorten a token, count workers and write the join commands', () => {
  assert.equal(shortToken('short'), 'short')
  assert.equal(shortToken('abcdefghijklmnopqrstuvwxyz'), 'abcdef…uvwxyz')
  const a = { name: 'b', Clusters: [{ Workers: ['1', '2'] }, { Workers: ['3'] }] }
  const b = { name: 'a', Clusters: [{ Workers: ['1', '2', '3'] }] }
  const c = { name: 'c', Clusters: [{ Workers: ['1'] }] }
  assert.equal(workerCount(a), 3)
  assert.deepEqual(sortNetworks([c, a, b]).map(n => n.name), ['a', 'b', 'c'])
  const cmd = joinCommands('TOK', 'net-1')
  assert.match(cmd.docker, /LOCALAI_P2P_NETWORK_ID=net-1 .*TOKEN="TOK".* federated --debug$/)
  assert.match(cmd.cli, /^ADDRESS=":80" LOCALAI_P2P_NETWORK_ID=net-1 .* local-ai federated --debug$/)
})
