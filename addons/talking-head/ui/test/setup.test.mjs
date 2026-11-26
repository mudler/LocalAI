import test from 'node:test'
import assert from 'node:assert/strict'
import { CANDIDATES, PARTS, choosePipeline, createSetupApi, defaultPicks, formatBytes, itemBytes, pipelineConfig, planSetup, runSetup, sizeUnknown, totalBytes, waitForJob } from '../src/lib/setup.js'

// A fake gallery and install queue behind the core routes the setup calls.
function fakeCore({ gallery = {}, jobs = {}, status = 200 } = {}) {
  const calls = []
  let next = 0
  const request = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET', body: init.body })
    if (status !== 200) return Response.json({ error: { message: 'unauthorized' } }, { status })
    const url = new URL(path, 'http://x')
    if (url.pathname === '/api/models') {
      const term = url.searchParams.get('term')
      return Response.json({ models: Object.values(gallery).filter((m) => m.name.includes(term)) })
    }
    let m
    if ((m = url.pathname.match(/^\/api\/models\/estimate\/(.+)$/))) return Response.json({ sizeBytes: gallery[decodeURIComponent(m[1]).split('@')[1]]?.size || 0, sizeDisplay: '' })
    if ((m = url.pathname.match(/^\/api\/models\/variants\/(.+)$/))) return Response.json({ variants: [{ model: 'avatarforcing-q8_0', memory_bytes: 600e6 }, { model: 'avatarforcing', memory_bytes: 1.2e9 }], auto_selected: 'avatarforcing-q8_0' })
    if ((m = url.pathname.match(/^\/api\/models\/install\/(.+)$/))) { const id = `job${++next}`; jobs[id] = jobs[id] || [{ progress: 50 }, { progress: 100, processed: true }]; return Response.json({ jobID: id }) }
    if ((m = url.pathname.match(/^\/models\/jobs\/(.+)$/))) { const q = jobs[m[1]]; return Response.json(q.length > 1 ? q.shift() : q[0]) }
    if (url.pathname === '/api/operations') return Response.json({ operations: Object.entries(jobs).filter(([, q]) => q.running).map(([jobID, q]) => ({ fullName: q.running, jobID })) })
    if (url.pathname.startsWith('/api/operations/')) return Response.json({ success: true })
    if (url.pathname === '/models/import') return Response.json({ success: true })
    return Response.json({ error: { message: 'no route' } }, { status: 404 })
  }
  return { calls, request }
}

const G = (name, extra = {}) => ({ id: `localai@${name}`, name, installed: false, license: '', size: 100e6, ...extra })
const GALLERY = Object.fromEntries([
  G('avatarforcing', { license: 'cc-by-nc-4.0', size: 1.2e9 }), G('llama-3.2-1b-instruct:q4_k_m', { size: 1.3e9 }),
  G('whisper-tiny-en-q5_1', { size: 31e6 }), G('vits-piper-en_US-amy-sherpa', { size: 79e6 }), G('silero-vad', { size: 2e6 }),
].map((m) => [m.name, m]))
const noSleep = async () => {}

test('setup: the default picks are small CPU models, one for each part', () => {
  const picks = defaultPicks()
  assert.deepEqual(Object.keys(picks), PARTS)
  assert.equal(picks.avatar, 'avatarforcing')
  for (const p of PARTS) assert.equal(picks[p], CANDIDATES[p][0])
})

test('setup: the plan finds each gallery entry, skips installed parts and adds up the download', async () => {
  const core = fakeCore({ gallery: { ...GALLERY, 'whisper-tiny-en-q5_1': G('whisper-tiny-en-q5_1', { installed: true }) } })
  const items = await planSetup(createSetupApi(core.request), defaultPicks(), new Set(['vits-piper-en_US-amy-sherpa']))
  assert.deepEqual(items.map((i) => [i.part, i.installed]), [['avatar', false], ['chat', false], ['stt', true], ['tts', true], ['vad', false]])
  assert.equal(items[0].license, 'cc-by-nc-4.0')
  assert.equal(items[0].id, 'localai@avatarforcing')
  assert.equal(totalBytes(items), 1.2e9 + 1.3e9 + 2e6)
  assert.equal(formatBytes(totalBytes(items)), '2.5 GB')
  assert.ok(core.calls.every((c) => c.method === 'GET'), 'planning changes nothing')
  assert.ok(!core.calls.some((c) => c.path.includes('estimate/localai%40whisper')), 'no size lookup for an installed part')
})

test('setup: an entry with builds offers them, and a missing entry blocks the plan with a reason', async () => {
  const core = fakeCore({ gallery: { ...GALLERY, avatarforcing: G('avatarforcing', { has_variants: true }) } })
  const items = await planSetup(createSetupApi(core.request), { ...defaultPicks(), chat: 'no-such-model', vad: '' })
  assert.deepEqual(items[0].variants.map((v) => v.model), ['avatarforcing-q8_0', 'avatarforcing'])
  assert.equal(items[0].variant, 'avatarforcing-q8_0')
  // The size follows the chosen build.
  assert.equal(itemBytes(items[0]), 600e6)
  assert.equal(itemBytes({ ...items[0], variant: 'avatarforcing' }), 1.2e9)
  assert.equal(sizeUnknown([{ part: 'tts' }]), true)
  assert.equal(sizeUnknown([{ part: 'tts', installed: true }]), false)
  assert.equal(items.find((i) => i.part === 'chat').error, 'notInGallery')
  assert.equal(items.find((i) => i.part === 'vad'), undefined, 'an optional part left empty is not planned')
})

test('setup: a refused lookup says the person may not install', async () => {
  const core = fakeCore({ status: 403 })
  await assert.rejects(planSetup(createSetupApi(core.request), defaultPicks()), (err) => err.forbidden && err.status === 403)
})

test('setup: the run installs the missing parts in order, follows each job and writes the pipeline', async () => {
  const core = fakeCore({ gallery: GALLERY })
  const api = createSetupApi(core.request)
  const items = await planSetup(api, defaultPicks(), new Set(['silero-vad']))
  const events = []
  await runSetup({ api, items, pipeline: pipelineConfig('talking-head', defaultPicks()), sleep: noSleep, onItem: (k, p) => events.push(`${k}:${p.state || 'job'}${p.progress ? `:${p.progress}` : ''}`) })
  const installs = core.calls.filter((c) => c.path.startsWith('/api/models/install/')).map((c) => decodeURIComponent(c.path.split('/').pop()))
  assert.deepEqual(installs, ['localai@avatarforcing', 'localai@llama-3.2-1b-instruct:q4_k_m', 'localai@whisper-tiny-en-q5_1', 'localai@vits-piper-en_US-amy-sherpa'])
  assert.ok(events.includes('avatar:installing:50'))
  assert.ok(events.includes('avatar:done:100'))
  assert.ok(events.includes('vad:skipped'))
  const imp = core.calls.find((c) => c.path === '/models/import')
  assert.deepEqual(JSON.parse(imp.body), { name: 'talking-head', pipeline: { transcription: 'whisper-tiny-en-q5_1', llm: 'llama-3.2-1b-instruct:q4_k_m', tts: 'vits-piper-en_US-amy-sherpa', vad: 'silero-vad' } })
  assert.equal(events.at(-1), 'pipeline:done')
})

test('setup: a failed job stops the run; a retry skips the parts that are done', async () => {
  const jobs = { job2: [{ error: 'download failed: 404' }] }
  const core = fakeCore({ gallery: GALLERY, jobs })
  const api = createSetupApi(core.request)
  const items = await planSetup(api, { ...defaultPicks(), vad: '' })
  const states = {}
  const onItem = (k, p) => { states[k] = { ...states[k], ...p } }
  await assert.rejects(runSetup({ api, items, pipeline: pipelineConfig('talking-head', defaultPicks()), sleep: noSleep, onItem }), /download failed/)
  assert.equal(states.avatar.state, 'done')
  assert.equal(states.chat.state, 'failed')
  assert.equal(states.chat.error, 'download failed: 404')
  assert.equal(states.pipeline, undefined)
  // Retry: the avatar is done and is not installed again.
  core.calls.length = 0
  await runSetup({ api, items, pipeline: pipelineConfig('talking-head', defaultPicks()), sleep: noSleep, onItem })
  const installs = core.calls.filter((c) => c.path.startsWith('/api/models/install/'))
  assert.equal(installs.length, 3)
  assert.ok(!installs.some((c) => c.path.includes('avatarforcing')))
  assert.equal(states.pipeline.state, 'done')
})

test('setup: Cancel cancels the running job on the server', async () => {
  const core = fakeCore({ gallery: GALLERY, jobs: { job1: [{ progress: 10 }] } })
  const api = createSetupApi(core.request)
  const items = await planSetup(api, defaultPicks())
  const controller = new AbortController()
  const states = {}
  const sleep = async () => { controller.abort(); throw Object.assign(new Error('cancelled'), { name: 'AbortError' }) }
  await assert.rejects(runSetup({ api, items, pipeline: {}, signal: controller.signal, sleep, onItem: (k, p) => { states[k] = { ...states[k], ...p } } }), (e) => e.name === 'AbortError')
  assert.equal(states.avatar.state, 'cancelled')
  assert.ok(core.calls.some((c) => c.path === '/api/operations/job1/cancel' && c.method === 'POST'))
})

test('setup: a job the server cancelled ends the wait', async () => {
  const api = { job: async () => ({ cancelled: true, processed: true }), cancel: async () => {} }
  await assert.rejects(waitForJob(api, 'j', { sleep: noSleep }), (e) => e.name === 'AbortError')
})

test('setup: an existing pipeline with the same parts is reused; a name in use is never overwritten', () => {
  const picks = defaultPicks()
  const same = { name: 'my-voice', transcription: picks.stt, llm: picks.chat, tts: picks.tts, vad: picks.vad }
  assert.deepEqual(choosePipeline([same], [], picks), { name: 'my-voice', exists: true })
  assert.deepEqual(choosePipeline([], ['talking-head'], picks), { name: 'talking-head-2', exists: false })
  assert.deepEqual(choosePipeline([{ ...same, tts: 'kokoro' }], [], picks), { name: 'talking-head', exists: false })
  assert.deepEqual(pipelineConfig('p', { ...picks, vad: '' }).pipeline.vad, undefined)
})

test('setup: an install still running from an earlier visit is followed, not started again', async () => {
  const jobs = { old1: Object.assign([{ progress: 70 }, { progress: 100, processed: true }], { running: 'localai@llama-3.2-1b-instruct:q4_k_m' }) }
  const core = fakeCore({ gallery: GALLERY, jobs })
  const api = createSetupApi(core.request)
  const items = await planSetup(api, { ...defaultPicks(), vad: '' }, new Set(['avatarforcing', 'whisper-tiny-en-q5_1', 'vits-piper-en_US-amy-sherpa']))
  assert.equal(items.find((i) => i.part === 'chat').jobID, 'old1')
  await runSetup({ api, items, pipeline: pipelineConfig('talking-head', defaultPicks()), sleep: noSleep })
  assert.equal(core.calls.filter((c) => c.path.startsWith('/api/models/install/')).length, 0)
  assert.ok(core.calls.some((c) => c.path === '/models/jobs/old1'))
})
