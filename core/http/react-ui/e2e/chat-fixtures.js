// Shared fixtures for the Chat and Talk specs: stubbed API routes, conversations
// written to the browser storage the Chat page reads, and a completion stream
// the spec drives by hand.
import { mockHome, seedChats, CHATS_KEY } from './home-fixtures.js'

export { CHATS_KEY, seedChats }

const GB = 1024 * 1024 * 1024

export const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'] },
  { id: 'gemma-3-12b', capabilities: ['FLAG_CHAT', 'FLAG_VISION'] },
  { id: 'phi-4-mini', capabilities: ['FLAG_CHAT'] },
]

// A bare conversation in the shape useChat stores.
export function chatOf(id, name, model, history, extra = {}, updatedAt = Date.now()) {
  return {
    id, name, model, history, updatedAt, createdAt: updatedAt - 60_000,
    systemPrompt: '', mcpMode: false, mcpServers: [], clientMCPServers: [],
    temperature: null, topP: null, topK: null,
    tokenUsage: { prompt: 0, completion: 0, total: 0 }, contextSize: null,
    ...extra,
  }
}

export const pair = (q, a) => [{ role: 'user', content: q }, { role: 'assistant', content: a }]

export const GO_BLOCK = 'func main() {\n\tctx, cancel := context.WithCancel(context.Background())\n\tdefer cancel()\n}'

// Stub what Chat reads. Models are the chat models the list offers; `loaded`
// the ones the server holds in memory.
export async function mockChat(page, { models = MODELS, loaded = ['qwen3-8b', 'phi-4-mini'], resources, features } = {}) {
  await mockHome(page, {
    loaded: loaded.map(id => ({ id, backend: 'llama-cpp' })),
    models: models.map(m => m.id),
    chatModels: models.map(m => m.id),
    ...(resources ? { resources } : {}),
    ...(features ? { features } : {}),
  })
  // Capabilities with the vision flag, which mockHome leaves out.
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models } }))
}

export const RESOURCES_24GB = {
  type: 'gpu',
  gpus: [{ name: 'RTX 4090', vendor: 'nvidia', total_vram: 24 * GB, used_vram: 7.6 * GB }],
  ram: { total: 64 * GB, used: 20 * GB, available: 40 * GB },
  aggregate: { total_memory: 24 * GB, used_memory: 7.6 * GB, gpu_count: 1, usage_percent: 31 },
}

// One answer in the OpenAI stream format.
export function sse(text, usage = { prompt_tokens: 3, completion_tokens: 5, total_tokens: 8 }) {
  return `data: ${JSON.stringify({ choices: [{ delta: { content: text } }] })}\n\n`
    + `data: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: 'stop' }], usage })}\n\n`
    + 'data: [DONE]\n\n'
}

export async function answerWith(page, text) {
  let body = null
  await page.route('**/v1/chat/completions', (route) => {
    body = route.request().postDataJSON()
    route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse(text) })
  })
  return { request: () => body }
}

// Replace fetch for the completion endpoint with a stream the spec drives:
//   await page.evaluate(() => window.__sse.push({ choices: [{ delta: { content: 'Hi' } }] }))
//   await page.evaluate(() => window.__sse.end())
// Abort is honoured, as the real fetch does.
export async function controlStream(page) {
  await page.addInitScript(() => {
    const orig = window.fetch.bind(window)
    const enc = new TextEncoder()
    window.__sse = { ctrl: null, calls: 0 }
    window.__sse.push = (obj) => window.__sse.ctrl?.enqueue(enc.encode(`data: ${JSON.stringify(obj)}\n\n`))
    window.__sse.end = () => { window.__sse.ctrl?.enqueue(enc.encode('data: [DONE]\n\n')); window.__sse.ctrl?.close() }
    window.fetch = (url, init) => {
      if (!String(url).includes('/v1/chat/completions')) return orig(url, init)
      window.__sse.calls++
      const stream = new ReadableStream({ start(c) { window.__sse.ctrl = c } })
      init?.signal?.addEventListener('abort', () => {
        try { window.__sse.ctrl.error(new DOMException('aborted', 'AbortError')) } catch { /* already closed */ }
      })
      return Promise.resolve(new Response(stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }))
    }
  })
}

export async function storedChats(page) {
  return page.evaluate((key) => JSON.parse(localStorage.getItem(key) || '{}'), CHATS_KEY)
}

// Open Chat with these conversations stored.
export async function openChat(page, chats, active = chats[0]?.id) {
  await seedChats(page, chats, active)
  await page.goto('/app/chat')
  // Keys and clicks only work once the page has mounted its handlers.
  await page.getByTestId('chat-composer').waitFor()
}

// A tiny valid PNG, for a message with an image in it.
export const PNG = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='

// The Talk page's WebRTC, faked: the page connects as it would, and the spec
// plays the server's events with window.__talk.emit(event).
export async function fakeRealtime(page, { pipelines = true, micError = null } = {}) {
  await page.route('**/api/pipeline-models', route => route.fulfill({
    json: pipelines
      ? [
          { name: 'voice-assistant', voice: 'alloy', vad: 'silero-vad', transcription: 'whisper-large-v3-turbo', llm: 'qwen3-8b', tts: 'kokoro-82m' },
          { name: 'voice-it', voice: 'paola', vad: 'silero-vad', transcription: 'whisper-small', llm: 'gemma-3-12b', tts: 'piper-it' },
        ]
      : [],
  }))
  await page.route('**/v1/realtime/calls', route => route.fulfill({ json: { sdp: 'v=0' } }))
  await page.addInitScript(({ micError }) => {
    window.__talk = { dc: null, pc: null, sent: [], hold: false }
    class FakePC {
      constructor() { this.connectionState = 'new'; this.iceGatheringState = 'complete'; this.localDescription = null; window.__talk.pc = this }
      addTrack() {}
      createDataChannel() {
        const dc = { readyState: 'open', send: (m) => window.__talk.sent.push(JSON.parse(m)), close() {}, onmessage: null }
        window.__talk.dc = dc
        return dc
      }
      async createOffer() { return { type: 'offer', sdp: 'v=0' } }
      async setLocalDescription(d) { this.localDescription = d }
      async setRemoteDescription() {
        if (window.__talk.hold) return new Promise(() => {})
        this.connectionState = 'connected'
        this.onconnectionstatechange && this.onconnectionstatechange()
      }
      close() { this.connectionState = 'closed' }
      getStats() { return Promise.resolve(new Map()) }
    }
    window.RTCPeerConnection = FakePC
    navigator.mediaDevices.getUserMedia = async () => {
      if (micError) { const e = new Error('denied'); e.name = micError; throw e }
      return new MediaStream()
    }
    window.__talk.emit = (ev) => window.__talk.dc.onmessage({ data: JSON.stringify(ev) })
    window.__talk.fail = () => { window.__talk.pc.connectionState = 'failed'; window.__talk.pc.onconnectionstatechange() }
  }, { micError })
}
