import test from 'node:test'
import assert from 'node:assert/strict'
import { makeSdk } from './host.mjs'
import { setSdk } from '../src/runtime.js'

// The calls the pages make go out through sdk.core.fetch with the paths and
// bodies of the core API client (core utils/api.js).
async function setup(answer) {
  const host = await makeSdk({
    addonId: 'studio', pages: ['studio'],
    overrides: {
      fetch: async (url, init) => {
        host.calls.push([url, init])
        const r = typeof answer === 'function' ? answer(url, init) : answer
        return {
          ok: r.ok !== false, status: r.status || 200,
          headers: new Map(Object.entries(r.headers || { 'content-type': 'application/json' })),
          json: async () => r.body ?? {}, blob: async () => new Blob(['x']),
        }
      },
    },
  })
  setSdk(host.sdk)
  return { ...host, api: await import('../src/utils/api.js') }
}

test('JSON calls: the endpoint, the method, the JSON body and the content type', async () => {
  const { api, calls } = await setup({ body: { ok: 1 } })
  assert.deepEqual(await api.imageApi.generate({ prompt: 'x' }), { ok: 1 })
  await api.videoApi.generate({ prompt: 'v' })
  await api.threeDApi.generate({ a: 1 })
  await api.threeDApi.animate({ b: 2 })
  await api.realtimeApi.call({ sdp: 's' })
  await api.realtimeApi.pipelineModels()
  await api.voiceApi.register({ name: 'n' })
  assert.deepEqual(calls.map(([url, init]) => [url, init.method || 'GET']), [
    ['/v1/images/generations', 'POST'], ['/video', 'POST'], ['/3d/generations', 'POST'], ['/3d/animate', 'POST'],
    ['/v1/realtime/calls', 'POST'], ['/api/pipeline-models', 'GET'], ['/v1/voice/register', 'POST'],
  ])
  assert.equal(calls[0][1].body, '{"prompt":"x"}')
  assert.equal(calls[0][1].headers.get('content-type'), 'application/json')
})

test('models: the list keeps only the parameters that are set, install carries the variant, estimate the contexts', async () => {
  const { api, calls } = await setup({ body: {} })
  await api.modelsApi.list({ term: 'a b', page: 2, empty: '', none: null })
  await api.modelsApi.install('org/name')
  await api.modelsApi.install('org/name', 'q4 k')
  await api.modelsApi.estimate('m', [4096, 8192])
  await api.modelsApi.estimate('m')
  assert.deepEqual(calls.map(([url]) => url), [
    '/api/models?term=a+b&page=2', '/api/models/install/org/name', '/api/models/install/org/name?variant=q4%20k',
    '/api/models/estimate/m?contexts=4096%2C8192', '/api/models/estimate/m',
  ])
})

test('an error answer becomes an Error with the server message, the status and the body', async () => {
  const { api } = await setup({ ok: false, status: 400, body: { error: { message: 'no such model' } } })
  await assert.rejects(api.imageApi.generate({}), (e) => e.message === 'no such model' && e.status === 400 && e.body.error.message === 'no such model')
})

test('audio calls return the blob and the saved file names from the headers', async () => {
  const { api, calls } = await setup({
    headers: { 'content-type': 'audio/wav', 'content-disposition': 'attachment; filename="out.wav"', 'x-audio-input-url': '/generated-audio/in.wav' },
  })
  const out = await api.ttsApi.generate({ text: 'hi' })
  assert.equal(out.serverUrl, '/generated-audio/out.wav')
  assert.equal(out.inputUrl, '/generated-audio/in.wav')
  assert.equal(out.referenceUrl, null)
  assert.equal(calls[0][0], '/tts')
  await api.soundApi.generate({})
  await api.ttsApi.generateV1({})
  assert.deepEqual(calls.slice(1).map(([url]) => url), ['/v1/sound-generation', '/v1/audio/speech'])
})

test('multipart calls send FormData and set no content type', async () => {
  const { api, calls } = await setup({ headers: { 'content-type': 'audio/wav' } })
  await api.audioTransformApi.process({ model: 'm', audioFile: new Blob(['a']), referenceFile: new Blob(['r']), format: 'wav', sampleRate: 16000, params: { gain: 2, skip: '', none: null } })
  const form = calls[0][1].body
  assert.ok(form instanceof FormData)
  assert.deepEqual([...form.keys()].sort(), ['audio', 'model', 'params[gain]', 'reference', 'response_format', 'sample_rate'])
  assert.equal(calls[0][0], '/audio/transformations')
  assert.equal(calls[0][1].headers.has('content-type'), false)
  await api.diarizationApi.run({ file: new Blob(['a']), model: 'd', profiles: true })
  assert.equal(calls[1][0], '/v1/audio/diarization')
  assert.deepEqual([...calls[1][1].body.keys()].sort(), ['file', 'include_speaker_profiles', 'include_text', 'model', 'response_format'])
  await api.imageApi.upscale({ model: 'u', image: new Blob(['i']), scale: 4 })
  assert.equal(calls[2][0], '/v1/images/upscale')
})

test('remesh posts the mesh and returns the blob', async () => {
  const { api, calls } = await setup({ headers: { 'content-type': 'model/gltf-binary' } })
  const blob = await api.threeDApi.remesh(new Blob(['m']), 'trellis', 12)
  assert.equal(calls[0][0], '/3d/remesh')
  assert.equal(await blob.text(), 'x')
})

test('fetchUrl: a core path goes through the SDK, a blob address is fetched directly', async () => {
  const { api, calls } = await setup({ body: {} })
  await api.fetchUrl('/generated-images/a.png')
  assert.deepEqual(calls.map(([url]) => url), ['/generated-images/a.png'])
  const seen = []
  const real = globalThis.fetch
  globalThis.fetch = async (url) => { seen.push(url); return { ok: true } }
  try {
    await api.fetchUrl('blob:http://x/abc')
    await api.fetchUrl('data:text/plain,hi')
  } finally { globalThis.fetch = real }
  assert.deepEqual(seen, ['blob:http://x/abc', 'data:text/plain,hi'])
  assert.equal(calls.length, 1)
})

test('the stream address of the audio transform is a WebSocket address', async () => {
  const { api } = await setup({})
  assert.equal(api.audioTransformApi.streamUrl(), '/audio/transformations/stream')
})

test('every endpoint the stand-in uses is one the core config lists', async () => {
  const { readFileSync } = await import('node:fs')
  const { fileURLToPath } = await import('node:url')
  const { dirname, join } = await import('node:path')
  const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '../src/utils/api.js'), 'utf8')
  const config = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '../src/utils/config.js'), 'utf8')
  const names = [...src.matchAll(/API_CONFIG\.endpoints\.([A-Za-z0-9]+)/g)].map(m => m[1])
  assert.ok(names.length > 10)
  for (const name of new Set(names)) assert.match(config, new RegExp(`\\b${name}\\b`), name)
})
