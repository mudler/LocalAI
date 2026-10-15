// Stubs and sample files for the Voices and Faces specs: a short sine WAV, a
// small PNG, and the routes the two pages call.
import { deflateSync } from 'node:zlib'

export const SPEAKER_MODEL = 'wespeaker-resnet34'
export const FACE_MODEL = 'insightface-buffalo-l'
export const VOICE_KEY = 'localai_voice_enrollments'
export const FACE_KEY = 'localai_face_enrollments'

export function wav(seconds = 4, amplitude = 0.4) {
  const rate = 16000
  const n = rate * seconds
  const buf = Buffer.alloc(44 + n * 2)
  buf.write('RIFF', 0); buf.writeUInt32LE(36 + n * 2, 4); buf.write('WAVEfmt ', 8)
  buf.writeUInt32LE(16, 16); buf.writeUInt16LE(1, 20); buf.writeUInt16LE(1, 22)
  buf.writeUInt32LE(rate, 24); buf.writeUInt32LE(rate * 2, 28); buf.writeUInt16LE(2, 32); buf.writeUInt16LE(16, 34)
  buf.write('data', 36); buf.writeUInt32LE(n * 2, 40)
  for (let i = 0; i < n; i += 1) {
    const env = 0.5 + 0.5 * Math.sin(i / 2400)
    buf.writeInt16LE(Math.round(Math.sin(i * 0.06) * env * amplitude * 32767), 44 + i * 2)
  }
  return buf
}

const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0 })
const crc = (b) => { let c = 0xffffffff; for (const x of b) c = crcTable[(c ^ x) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0 }
function chunk(type, data) {
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
  const body = Buffer.concat([Buffer.from(type), data])
  const sum = Buffer.alloc(4); sum.writeUInt32BE(crc(body))
  return Buffer.concat([len, body, sum])
}
export function png(w = 160, h = 120) {
  const raw = Buffer.alloc((w * 3 + 1) * h)
  for (let y = 0; y < h; y += 1) {
    raw[y * (w * 3 + 1)] = 0
    for (let x = 0; x < w; x += 1) {
      const o = y * (w * 3 + 1) + 1 + x * 3
      const face = Math.hypot(x - w / 2, y - h / 2.2) < h / 3
      raw[o] = face ? 196 : 120 + (y * 40) / h
      raw[o + 1] = face ? 168 : 140
      raw[o + 2] = face ? 150 : 150 + (x * 40) / w
    }
  }
  const head = Buffer.alloc(13); head.writeUInt32BE(w, 0); head.writeUInt32BE(h, 4); head[8] = 8; head[9] = 2
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', head), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

export const clipFile = (name = 'board-sync-clip.wav', seconds = 8) => ({ name, mimeType: 'audio/wav', buffer: wav(seconds) })
export const photoFile = (name = 'team-offsite.png') => ({ name, mimeType: 'image/png', buffer: png() })

export const people = [
  { id: 'v-marta', name: 'Marta Rossi', labels: { team: 'platform' }, registeredAt: new Date(Date.now() - 12 * 86400000).toISOString() },
  { id: 'v-jonas', name: 'Jonas Weber', labels: { team: 'infra' }, registeredAt: new Date(Date.now() - 12 * 86400000).toISOString() },
  { id: 'v-ilse', name: 'Ilse Janssen', labels: { role: 'guest' }, registeredAt: new Date(Date.now() - 5 * 86400000).toISOString() },
]
export const faces = [
  { id: 'f-anna', name: 'Anna Kowalski', labels: { team: 'design' }, registeredAt: new Date(Date.now() - 9 * 86400000).toISOString() },
  { id: 'f-tomas', name: 'Tomas Ribeiro', labels: { team: 'infra' }, registeredAt: new Date(Date.now() - 9 * 86400000).toISOString() },
]

export const match = (id, name, distance) => ({ id, name, distance, confidence: 0, match: false })

// Routes and stored state for both pages. `opts` swaps pieces:
//   voiceModels / faceModels   installed models (default: one each)
//   stored / storedFaces       the browser-side lists
//   identify / verify          a JSON body, or { status, body } for a failure
//   faceIdentify / faceVerify  the same for faces
//   delay                      milliseconds to hold identify and verify
export async function mockIdentity(page, opts = {}) {
  const calls = { identify: [], verify: [], register: [], forget: [], profile: [], faceIdentify: [], faceVerify: [], faceRegister: [] }
  const voiceModels = opts.voiceModels ?? [SPEAKER_MODEL]
  const faceModels = opts.faceModels ?? [FACE_MODEL]
  await page.addInitScript(([vk, fk, voices, facesList]) => {
    try {
      if (voices) localStorage.setItem(vk, JSON.stringify(voices))
      if (facesList) localStorage.setItem(fk, JSON.stringify(facesList))
    } catch { /* storage blocked */ }
  }, [VOICE_KEY, FACE_KEY, opts.stored ?? null, opts.storedFaces ?? null])

  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [
    ...voiceModels.map(id => ({ id, capabilities: ['FLAG_SPEAKER_RECOGNITION'], backend: 'wespeaker' })),
    ...faceModels.map(id => ({ id, capabilities: ['FLAG_FACE_RECOGNITION'], backend: 'insightface' })),
    { id: 'qwen-base', capabilities: ['FLAG_TTS'], backend: 'qwen3-tts-cpp', voice_cloning: { reference_transcript_required: true, accepted_audio_formats: ['audio/wav'] } },
  ] } }))
  await page.route('**/api/voice-profiles', async route => {
    if (route.request().method() === 'POST') {
      calls.profile.push(route.request().postData()?.length || 0)
      return route.fulfill({ status: 201, json: { id: 'prof-1', name: 'Marta Rossi', voice: 'localai://voice-profiles/prof-1', transcript: 'x', created_at: '2026-07-01T12:00:00Z', consent_confirmed_at: '2026-07-01T12:00:00Z', audio: { duration_ms: 4000, sample_rate: 24000 } } })
    }
    return route.fulfill({ json: { data: opts.profiles ?? [] } })
  })
  await page.route('**/api/voice-profiles/*/audio', route => route.fulfill({ status: 200, contentType: 'audio/wav', body: wav(3) }))
  await page.route('**/api/models?**', route => route.fulfill({ json: { models: opts.gallery ?? [] } }))
  const answer = async (route, spec, fallback) => {
    if (opts.delay) await new Promise(r => setTimeout(r, opts.delay))
    if (spec?.status) return route.fulfill({ status: spec.status, json: spec.body ?? { error: { message: 'failed' } } })
    return route.fulfill({ json: spec ?? fallback })
  }
  const body = (route) => { try { return route.request().postDataJSON() } catch { return null } }
  await page.route('**/v1/voice/identify', route => { calls.identify.push(body(route)); return answer(route, opts.identify, { matches: [] }) })
  await page.route('**/v1/voice/verify', route => { calls.verify.push(body(route)); return answer(route, opts.verify, { verified: true, distance: 0.19, threshold: 0.25, confidence: 24, model: SPEAKER_MODEL }) })
  await page.route('**/v1/voice/register', route => { const b = body(route); calls.register.push(b); return route.fulfill({ json: { id: `new-${calls.register.length}`, name: b.name, registered_at: new Date().toISOString() } }) })
  await page.route('**/v1/voice/forget', route => { calls.forget.push(body(route)); return route.fulfill({ status: opts.forgetStatus ?? 204, body: '' }) })
  await page.route('**/v1/face/identify', route => { calls.faceIdentify.push(body(route)); return answer(route, opts.faceIdentify, { matches: [] }) })
  await page.route('**/v1/face/verify', route => { calls.faceVerify.push(body(route)); return answer(route, opts.faceVerify, { verified: true, distance: 0.21, threshold: 0.35, confidence: 40, model: FACE_MODEL, img1_area: { x: 20, y: 10, w: 60, h: 70 }, img2_area: { x: 30, y: 12, w: 55, h: 66 } }) })
  await page.route('**/v1/face/register', route => { const b = body(route); calls.faceRegister.push(b); return route.fulfill({ json: { id: `fnew-${calls.faceRegister.length}`, name: b.name, registered_at: new Date().toISOString() } }) })
  await page.route('**/v1/face/forget', route => { calls.forget.push({ face: true, ...body(route) }); return route.fulfill({ status: 204, body: '' }) })
  return calls
}
