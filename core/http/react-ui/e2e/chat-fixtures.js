// Shared fixtures for the Chats and Talk specs: stubbed API routes, a completion
// answer and a faked realtime connection.
import { mockHome } from './home-fixtures.js'
import { installStudioAddons } from './studio-addon.js'


export const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'] },
  { id: 'gemma-3-12b', capabilities: ['FLAG_CHAT', 'FLAG_VISION'] },
  { id: 'phi-4-mini', capabilities: ['FLAG_CHAT'] },
]

// Stub what the chat pages read. Models are the chat models the list offers; `loaded`
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

// The Talk page's WebRTC, faked: the page connects as it would, and the spec
// plays the server's events with window.__talk.emit(event).
export async function fakeRealtime(page, { pipelines = true, micError = null } = {}) {
  // Talk is an add-on: install the built bundle that draws the page.
  await installStudioAddons(page, { ids: ['talk'] })
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
