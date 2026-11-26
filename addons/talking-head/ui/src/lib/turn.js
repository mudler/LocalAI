// One turn of the conversation, as four calls to the core API:
//
//   transcribe  /v1/audio/transcriptions   the recording -> your words
//   chat        /v1/chat/completions       your words -> the reply (streamed)
//   speech      /tts                       the reply -> a WAV
//   video       /video                     the portrait and the WAV -> an MP4
//
// A typed message skips transcribe; "Say this" skips transcribe and chat and
// speaks the typed text as it is. `request(path, init)` is the core fetch of
// the SDK, so the calls carry the signed-in person's own session. Aborting
// `signal` stops the call in flight and the turn rejects with an AbortError.

export const STAGES = ['transcribe', 'chat', 'speech', 'video']

// The stages a kind of input runs, in order.
export function stagesFor(kind) {
  if (kind === 'audio') return STAGES
  if (kind === 'text') return ['chat', 'speech', 'video']
  return ['speech', 'video'] // say
}

// The default of the avatarforcing-cpp backend: nfe 6 is step 5.
export const DEFAULT_NFE = 6
export const MIN_NFE = 3
export const MAX_NFE = 12

// A failure of one stage. `stage` names it, `status` is the HTTP status when
// the server answered, and `code` is a key of the page's error words when the
// page knows the cause.
export class TurnError extends Error {
  constructor(stage, message, { status, code } = {}) {
    super(message)
    this.name = 'TurnError'
    this.stage = stage
    this.status = status
    this.code = code
  }
}

// The text a voice can read: no reasoning block, no markdown marks, no emoji
// and plain spaces. A small model often answers with markdown even when told
// not to, and a TTS engine reads the marks aloud.
export function speakable(text) {
  return String(text || '')
    .replace(/<think>[\s\S]*?(<\/think>|$)/g, ' ')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/[*_`~]+/g, '')
    .replace(/[#>|]+/g, ' ')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}\u{FE0F}]/gu, '')
    .replace(/\s+/g, ' ')
    .replace(/ ([.,!?;:])/g, '$1')
    .trim()
}

// The messages of a chat request: the instructions, the earlier turns (text
// only) and the new message.
export function chatMessages(instructions, history, text) {
  const out = []
  if (instructions && instructions.trim()) out.push({ role: 'system', content: instructions.trim() })
  for (const turn of history || []) {
    if ((turn.role === 'user' || turn.role === 'assistant') && turn.text) out.push({ role: turn.role, content: turn.text })
  }
  out.push({ role: 'user', content: text })
  return out
}

// The body of the /video request. `nfe` goes in params because that is the
// name the backend documents; seed 0 or blank means the backend default.
export function videoBody({ model, portrait, audio, nfe, seed }) {
  const body = { model, start_image: portrait, audio, response_format: 'b64_json' }
  const n = Number(nfe)
  if (Number.isFinite(n) && n >= MIN_NFE) body.params = { nfe: String(Math.round(n)) }
  const s = Number(seed)
  if (seed !== '' && seed !== null && seed !== undefined && Number.isFinite(s) && s > 0) body.seed = Math.round(s)
  return body
}

// The base64 of a blob, without FileReader, so tests run in node too.
export async function blobToBase64(blob) {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  let out = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) out += String.fromCharCode.apply(null, bytes.subarray(i, i + chunk))
  return btoa(out)
}

export function base64ToBlob(b64, type) {
  const raw = atob(b64)
  const bytes = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i)
  return new Blob([bytes], { type })
}

async function failure(stage, response) {
  let message = `HTTP ${response.status}`
  try {
    const data = await response.json()
    if (data?.error?.message) message = data.error.message
    else if (typeof data?.error === 'string') message = data.error
    else if (typeof data?.message === 'string') message = data.message
  } catch { /* not JSON */ }
  return new TurnError(stage, message, { status: response.status })
}

// Reads a chat completion. A streamed answer (text/event-stream) calls onDelta
// with the text so far after each chunk; a JSON answer calls it once.
export async function readChat(response, onDelta) {
  const type = response.headers.get('content-type') || ''
  if (!type.includes('text/event-stream') || !response.body) {
    const data = await response.json()
    const text = data?.choices?.[0]?.message?.content || ''
    onDelta?.(text)
    return text
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let text = ''
  const take = (line) => {
    const trimmed = line.trim()
    if (!trimmed.startsWith('data:')) return
    const payload = trimmed.slice(5).trim()
    if (!payload || payload === '[DONE]') return
    try {
      const delta = JSON.parse(payload)?.choices?.[0]?.delta?.content
      if (delta) {
        text += delta
        onDelta?.(text)
      }
    } catch { /* a keep-alive or a partial line */ }
  }
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop()
    lines.forEach(take)
  }
  take(buffer + decoder.decode())
  return text
}

// Runs one turn. `input` is { kind: 'audio', blob } | { kind: 'text', text } |
// { kind: 'say', text }. onStage(stage, state, info) reports 'running', then
// 'done' with { ms }, or 'failed'. onUser(text) gives the words of the person
// (the transcript of a recording), onReply(text) the reply as it streams.
export async function runTurn({
  request, input, models, portrait, history = [], instructions = '', voice = '', nfe = DEFAULT_NFE, seed = '',
  maxTokens = 160, signal, onStage = () => {}, onUser = () => {}, onReply = () => {}, now = () => globalThis.performance?.now?.() ?? Date.now(),
}) {
  const timings = {}
  const stage = async (name, fn) => {
    const start = now()
    onStage(name, 'running')
    try {
      const result = await fn()
      timings[name] = Math.round(now() - start)
      onStage(name, 'done', { ms: timings[name] })
      return result
    } catch (err) {
      onStage(name, err?.name === 'AbortError' ? 'cancelled' : 'failed', { ms: Math.round(now() - start) })
      throw err
    }
  }
  const call = async (name, path, init) => {
    const response = await request(path, { ...init, signal })
    if (!response.ok) throw await failure(name, response)
    return response
  }

  let userText = input.kind === 'audio' ? '' : String(input.text || '').trim()
  if (input.kind === 'audio') {
    userText = await stage('transcribe', async () => {
      const form = new FormData()
      const ext = ((input.blob.type || '').split('/')[1] || 'webm').split(';')[0]
      form.append('file', input.blob, `speech.${ext}`)
      form.append('model', models.stt)
      const data = await (await call('transcribe', '/v1/audio/transcriptions', { method: 'POST', body: form })).json()
      const text = String(data?.text || '').trim()
      if (!text) throw new TurnError('transcribe', 'nothing was heard', { code: 'emptyTranscript' })
      return text
    })
  }
  if (!userText) throw new TurnError(input.kind === 'say' ? 'speech' : 'chat', 'empty message', { code: 'emptyText' })
  onUser(userText)

  let replyText = userText
  if (input.kind !== 'say') {
    replyText = await stage('chat', async () => {
      const response = await call('chat', '/v1/chat/completions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: models.chat, messages: chatMessages(instructions, history, userText), stream: true, max_tokens: maxTokens }),
      })
      const text = await readChat(response, (so) => onReply(speakable(so)))
      const clean = speakable(text)
      if (!clean) throw new TurnError('chat', 'the model gave an empty reply', { code: 'emptyReply' })
      return clean
    })
  }
  onReply(replyText)

  const audio = await stage('speech', async () => {
    const body = { model: models.tts, input: input.kind === 'say' ? speakable(replyText) || replyText : replyText }
    if (voice && voice.trim()) body.voice = voice.trim()
    const response = await call('speech', '/tts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    return response.blob()
  })

  const video = await stage('video', async () => {
    const body = videoBody({ model: models.avatar, portrait, audio: await blobToBase64(audio), nfe, seed })
    const response = await call('video', '/video', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    const data = await response.json()
    const item = data?.data?.[0]
    if (item?.b64_json) return base64ToBlob(item.b64_json, 'video/mp4')
    if (item?.url) {
      // A server that ignores response_format answers with an address on itself.
      const path = new URL(item.url, 'http://x').pathname
      return (await call('video', path, { method: 'GET' })).blob()
    }
    throw new TurnError('video', 'the server returned no video', { code: 'noVideo' })
  })

  return { userText, replyText, audio, video, timings }
}
