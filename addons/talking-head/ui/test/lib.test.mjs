import test from 'node:test'
import assert from 'node:assert/strict'
import { chooseModels, fromPipeline, isAvatarModel, missingParts, modelGroups, splitPipelines } from '../src/lib/models.js'
import { addPortrait, base64Of, loadPortraits, MAX_PORTRAITS, removePortrait, savePortraits, squareCrop, STORAGE_KEY } from '../src/lib/portrait.js'
import { blobToBase64, chatMessages, readChat, runTurn, speakable, stagesFor, TurnError, videoBody } from '../src/lib/turn.js'

const LIST = [
  { id: 'qwen', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
  { id: 'off', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp', disabled: true },
  { id: 'broken', capabilities: [], alias_error: 'unknown target' },
  { id: 'piper', capabilities: ['tts'], backend: 'piper' },
  { id: 'whisper', capabilities: ['FLAG_TRANSCRIPT'], backend: 'whisper' },
  { id: 'avatarforcing', capabilities: ['FLAG_VIDEO'], backend: 'avatarforcing-cpp' },
  { id: 'wan', capabilities: ['FLAG_VIDEO'], backend: 'diffusers' },
]

test('models: each part lists the usable models that can do it, and only avatarforcing-cpp is an avatar', () => {
  assert.deepEqual(modelGroups(LIST), { chat: ['qwen'], tts: ['piper'], stt: ['whisper'], avatar: ['avatarforcing'], vad: [] })
  assert.equal(isAvatarModel(LIST[6]), false)
  assert.deepEqual(modelGroups(null), { chat: [], tts: [], stt: [], avatar: [], vad: [] })
})

test('models: every build of the avatar model is an avatar choice, whatever its name', () => {
  const builds = ['avatarforcing', 'avatarforcing-q8_0', 'avatarforcing-f32']
    .map((id) => ({ id, capabilities: ['FLAG_VIDEO'], backend: 'avatarforcing-cpp' }))
  assert.deepEqual(modelGroups(builds).avatar, ['avatarforcing', 'avatarforcing-f32', 'avatarforcing-q8_0'])
})

test('models: a saved choice wins while it is installed, a link picks the avatar model', () => {
  const groups = { chat: ['a', 'b'], tts: ['t'], stt: [], avatar: ['x', 'y'] }
  assert.deepEqual(chooseModels({ chat: 'b', avatar: 'gone' }, groups, ''), { chat: 'b', tts: 't', stt: '', avatar: 'x' })
  assert.equal(chooseModels({ avatar: 'x' }, groups, 'y').avatar, 'y')
  assert.equal(chooseModels({ avatar: 'x' }, groups, 'wan').avatar, 'x')
  assert.deepEqual(missingParts({ chat: '', tts: '', stt: '', avatar: '' }), ['avatar', 'tts'])
  assert.deepEqual(missingParts({ chat: '', tts: 't', stt: '', avatar: 'x' }), [])
})

test('models: a speech-to-speech pipeline cannot be split into parts', () => {
  const pipes = [
    { name: 'voice', transcription: 'w', llm: 'q', tts: 'p', voice: 'amy' },
    { name: 's2s', transcription: 's2s', llm: 's2s', tts: 's2s', any_to_any: true },
    { name: 'half', transcription: '', llm: 'q', tts: 'p' },
  ]
  assert.deepEqual(splitPipelines(pipes).map((p) => p.name), ['voice'])
  assert.deepEqual(fromPipeline(pipes[0]), { pipeline: 'voice', chat: 'q', tts: 'p', stt: 'w', voice: 'amy' })
})

test('portrait: the crop is the largest centred square, moved and zoomed inside the image', () => {
  assert.deepEqual(squareCrop(800, 600), { sx: 100, sy: 0, side: 600 })
  assert.deepEqual(squareCrop(800, 600, { x: 0 }), { sx: 0, sy: 0, side: 600 })
  assert.deepEqual(squareCrop(800, 600, { zoom: 2, x: 1, y: 1 }), { sx: 500, sy: 300, side: 300 })
  assert.deepEqual(squareCrop(512, 512, { zoom: 0.1, x: 9 }), { sx: 0, sy: 0, side: 512 })
  assert.equal(base64Of('data:image/jpeg;base64,QUJD'), 'QUJD')
})

test('portrait: the library keeps the newest few and survives a storage that refuses', () => {
  const store = new Map()
  const storage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) }
  let list = []
  for (let i = 0; i < MAX_PORTRAITS + 2; i++) list = addPortrait(list, { id: `p${i}`, name: `${i}`, dataUrl: 'data:image/jpeg;base64,AA' })
  assert.equal(list.length, MAX_PORTRAITS)
  assert.equal(list[0].id, `p${MAX_PORTRAITS + 1}`)
  assert.equal(savePortraits(list, storage), true)
  assert.equal(loadPortraits(storage).length, MAX_PORTRAITS)
  assert.equal(removePortrait(list, list[0].id).length, MAX_PORTRAITS - 1)
  store.set(STORAGE_KEY, JSON.stringify([{ id: 'x', dataUrl: 'javascript:alert(1)' }]))
  assert.deepEqual(loadPortraits(storage), [], 'only image data URLs are kept')
  const full = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('quota') } }
  assert.deepEqual(loadPortraits(full), [])
  assert.equal(savePortraits(list, full), false)
})

test('turn: replies are cleaned for the voice and the history keeps text turns only', () => {
  assert.equal(speakable('<think>hmm</think>**Hello** there! 😀 See [docs](http://x).'), 'Hello there! See docs.')
  assert.equal(speakable('<think>never closed'), '')
  assert.equal(speakable('## Hi, **friend** .'), 'Hi, friend.')
  assert.deepEqual(chatMessages(' Be brief ', [{ role: 'user', text: 'hi' }, { role: 'note', text: '' }, { role: 'said', text: 'x' }, { role: 'assistant', text: 'hello' }], 'how are you'), [
    { role: 'system', content: 'Be brief' }, { role: 'user', content: 'hi' }, { role: 'assistant', content: 'hello' }, { role: 'user', content: 'how are you' },
  ])
  assert.deepEqual(stagesFor('audio'), ['transcribe', 'chat', 'speech', 'video'])
  assert.deepEqual(stagesFor('text'), ['chat', 'speech', 'video'])
  assert.deepEqual(stagesFor('say'), ['speech', 'video'])
})

test('turn: the video request sends nfe in params and a seed only when set', () => {
  assert.deepEqual(videoBody({ model: 'a', portrait: 'P', audio: 'A', nfe: 6, seed: '' }), { model: 'a', start_image: 'P', audio: 'A', response_format: 'b64_json', params: { nfe: '6' } })
  assert.equal(videoBody({ model: 'a', nfe: '4', seed: '7' }).seed, 7)
  assert.equal(videoBody({ model: 'a', nfe: 1, seed: 0 }).params, undefined)
  assert.equal(videoBody({ model: 'a', nfe: 6, seed: 0 }).seed, undefined)
})

const sse = (chunks) => new Response(new ReadableStream({
  start(c) { for (const s of chunks) c.enqueue(new TextEncoder().encode(s)); c.close() },
}), { headers: { 'content-type': 'text/event-stream' } })

test('turn: a streamed chat answer is read across split chunks', async () => {
  const seen = []
  const text = await readChat(sse([
    'data: {"choices":[{"delta":{"content":"Hel"}}]}\n\ndata: {"choices":[{"del',
    'ta":{"content":"lo"}}]}\n\n: keep-alive\n\ndata: [DONE]\n\n',
  ]), (t) => seen.push(t))
  assert.equal(text, 'Hello')
  assert.deepEqual(seen, ['Hel', 'Hello'])
  const plain = await readChat(Response.json({ choices: [{ message: { content: 'Hi' } }] }))
  assert.equal(plain, 'Hi')
})

// A fake core: records each call and answers like LocalAI.
function fakeCore({ fail } = {}) {
  const calls = []
  const request = async (path, init) => {
    calls.push({ path, init })
    if (init.signal?.aborted) throw new DOMException('aborted', 'AbortError')
    if (fail && path === fail.path) return Response.json({ error: { message: fail.message } }, { status: fail.status })
    if (path === '/v1/audio/transcriptions') return Response.json({ text: ' hello face ' })
    if (path === '/v1/chat/completions') return sse(['data: {"choices":[{"delta":{"content":"**Hi** there."}}]}\n\n', 'data: [DONE]\n\n'])
    if (path === '/tts') return new Response(new Blob([new Uint8Array([82, 73, 70, 70])], { type: 'audio/wav' }))
    if (path === '/video') return Response.json({ data: [{ b64_json: btoa('mp4') }] })
    return new Response('', { status: 404 })
  }
  return { calls, request }
}

const MODELS = { chat: 'qwen', tts: 'piper', stt: 'whisper', avatar: 'avatarforcing' }

test('turn: a recording runs the four stages in order and sends the portrait and the speech to /video', async () => {
  const core = fakeCore()
  const states = []
  const users = []
  const replies = []
  let clock = 0
  const result = await runTurn({
    request: core.request, input: { kind: 'audio', blob: new Blob(['x'], { type: 'audio/webm;codecs=opus' }) }, models: MODELS, portrait: 'UE9SVA==',
    history: [{ role: 'user', text: 'before' }], instructions: 'Be brief', voice: 'amy', nfe: 4, seed: '',
    onStage: (s, st) => states.push(`${s}:${st}`), onUser: (t) => users.push(t), onReply: (t) => replies.push(t), now: () => (clock += 100),
  })
  assert.deepEqual(core.calls.map((c) => c.path), ['/v1/audio/transcriptions', '/v1/chat/completions', '/tts', '/video'])
  assert.deepEqual(states, ['transcribe:running', 'transcribe:done', 'chat:running', 'chat:done', 'speech:running', 'speech:done', 'video:running', 'video:done'])
  assert.deepEqual(users, ['hello face'])
  assert.equal(result.replyText, 'Hi there.')
  assert.equal(replies.at(-1), 'Hi there.')
  const form = core.calls[0].init.body
  assert.equal(form.get('model'), 'whisper')
  assert.equal(form.get('file').name, 'speech.webm')
  const chat = JSON.parse(core.calls[1].init.body)
  assert.equal(chat.model, 'qwen')
  assert.equal(chat.stream, true)
  assert.deepEqual(chat.messages.map((m) => m.role), ['system', 'user', 'user'])
  assert.deepEqual(JSON.parse(core.calls[2].init.body), { model: 'piper', input: 'Hi there.', voice: 'amy' })
  const video = JSON.parse(core.calls[3].init.body)
  assert.equal(video.model, 'avatarforcing')
  assert.equal(video.start_image, 'UE9SVA==')
  assert.equal(video.audio, await blobToBase64(new Blob([new Uint8Array([82, 73, 70, 70])])))
  assert.deepEqual(video.params, { nfe: '4' })
  assert.equal(await result.video.text(), 'mp4')
  assert.equal(result.video.type, 'video/mp4')
  assert.deepEqual(Object.keys(result.timings), ['transcribe', 'chat', 'speech', 'video'])
})

test('turn: "Say this" speaks the typed text with no chat call', async () => {
  const core = fakeCore()
  const result = await runTurn({ request: core.request, input: { kind: 'say', text: ' Good *morning* ' }, models: MODELS, portrait: 'P' })
  assert.deepEqual(core.calls.map((c) => c.path), ['/tts', '/video'])
  assert.equal(JSON.parse(core.calls[0].init.body).input, 'Good morning')
  assert.equal(result.replyText, 'Good *morning*')
})

test('turn: a failed stage names the stage and keeps the server message', async () => {
  const core = fakeCore({ fail: { path: '/video', status: 400, message: 'avatarforcing-cpp: needs a portrait (start_image)' } })
  const states = []
  await assert.rejects(
    runTurn({ request: core.request, input: { kind: 'text', text: 'hi' }, models: MODELS, portrait: '', onStage: (s, st) => states.push(`${s}:${st}`) }),
    (err) => err instanceof TurnError && err.stage === 'video' && err.status === 400 && /needs a portrait/.test(err.message),
  )
  assert.equal(states.at(-1), 'video:failed')
})

test('turn: an empty transcript stops the turn before the chat', async () => {
  const request = async () => Response.json({ text: '  ' })
  await assert.rejects(
    runTurn({ request, input: { kind: 'audio', blob: new Blob(['x']) }, models: MODELS, portrait: 'P' }),
    (err) => err.code === 'emptyTranscript' && err.stage === 'transcribe',
  )
})

test('turn: an abort stops the turn and reports the stage as cancelled', async () => {
  const core = fakeCore()
  const controller = new AbortController()
  const states = []
  const request = async (path, init) => {
    if (path === '/tts') controller.abort()
    return core.request(path, init)
  }
  await assert.rejects(
    runTurn({ request, input: { kind: 'text', text: 'hi' }, models: MODELS, portrait: 'P', signal: controller.signal, onStage: (s, st) => states.push(`${s}:${st}`) }),
    (err) => err.name === 'AbortError',
  )
  assert.equal(states.at(-1), 'speech:cancelled')
  assert.ok(core.calls.every((c) => c.init.signal === controller.signal), 'every call carries the signal')
})
