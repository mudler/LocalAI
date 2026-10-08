import { expect } from '@playwright/test'
// Shared fixtures for the Studio front page specs: the installed models, a
// gallery that offers a model per type, a machine, believable stored history,
// and stand-ins for the files the history points at.
//
// History is seeded by writing the same browser-storage entries the workspaces
// write, so the page under test reads exactly what it reads in use.

export const GB = 1024 * 1024 * 1024

export const ALL_TYPES = ['images', 'video', 'threed', 'tts', 'sound', 'transform', 'diarization']

const CAPABILITY = {
  images: 'FLAG_IMAGE', video: 'FLAG_VIDEO', threed: 'FLAG_3D', tts: 'FLAG_TTS',
  sound: 'FLAG_SOUND_GENERATION', transform: 'FLAG_AUDIO_TRANSFORM', diarization: 'FLAG_DIARIZATION',
}

export const INSTALLED = {
  images: ['flux.1-schnell', 'sd-1.5-lcm'],
  video: ['wan2.1-t2v-1.3b'],
  threed: ['trellis-image-to-3d'],
  tts: ['kokoro-82m'],
  sound: ['ace-step'],
  transform: ['localvqe'],
  diarization: ['sortformer-4spk'],
}

export function capabilities(types = ALL_TYPES) {
  return {
    data: types.flatMap(type => INSTALLED[type].map(id => ({ id, capabilities: [CAPABILITY[type]] }))),
  }
}

// What the gallery offers when a type has no model, keyed by the tag Studio asks for.
export const GALLERY = {
  'text-to-image': [{ name: 'flux.1-schnell', description: 'Draws pictures from a sentence', size: 12.4, vram: 13 }],
  'text-to-video': [
    { name: 'wan2.1-t2v-14b', description: 'Larger clip model', size: 28, vram: 30 },
    { name: 'wan2.1-t2v-1.3b', description: 'Makes short clips from a sentence', size: 5.4, vram: 6 },
  ],
  'image-to-3d': [{ name: 'trellis-image-to-3d', description: 'Turns one picture into a 3D object', size: 6.8, vram: 9 }],
  tts: [{ name: 'kokoro-82m', description: 'Reads text aloud', size: 1.1, vram: 1.5 }],
  music: [{ name: 'ace-step', description: 'Makes music and ambient sound', size: 3.5, vram: 5 }],
  'audio-transform': [{ name: 'localvqe', description: 'Cleans up a recording', size: 0.1, vram: 0.3 }],
  diarization: [{ name: 'sortformer-4spk', description: 'Finds who spoke', size: 0.5, vram: 1 }],
}

export const GPU_RESOURCES = {
  type: 'gpu',
  available: true,
  gpus: [{ name: 'RTX 4090', vendor: 'nvidia', total_vram: 24 * GB, used_vram: 8.8 * GB, usage_percent: 36.7 }],
  ram: { total: 64 * GB, used: 18.2 * GB, free: 45.8 * GB, available: 45.8 * GB, usage_percent: 28.4 },
  aggregate: { total_memory: 24 * GB, used_memory: 8.8 * GB, free_memory: 15.2 * GB, usage_percent: 36.7, gpu_count: 1 },
}

// Stub everything the front page reads. `types` are the types that have a
// model. `installs` collects the names POSTed to the install endpoint; after an
// install the type gains its model on the next capabilities read when
// `installOnPost` is set.
export async function mockStudio(page, {
  types = ALL_TYPES,
  resources = GPU_RESOURCES,
  operations = [],
  installOnPost = false,
  capabilitiesStatus = 200,
  installStatus = 200,
} = {}) {
  const state = { types: [...types], installs: [], capabilityCalls: 0 }
  await page.route('**/api/models/capabilities', route => {
    state.capabilityCalls += 1
    if (capabilitiesStatus !== 200) return route.fulfill({ status: capabilitiesStatus, json: { error: 'down' } })
    return route.fulfill({ json: capabilities(state.types) })
  })
  // The v1 list is the fallback useModels takes when the capabilities call
  // fails; make it fail too so an error state is reachable.
  if (capabilitiesStatus !== 200) {
    await page.route('**/v1/models', route => route.fulfill({ status: 500, json: { error: 'down' } }))
  }
  await page.route('**/api/resources', route => route.fulfill({ json: resources }))
  await page.route('**/api/operations', route => route.fulfill({ json: { operations } }))
  await page.route(/\/api\/models\/estimate\//, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    const hit = Object.values(GALLERY).flat().find(m => m.name === name)
    if (!hit) return route.fulfill({ json: {} })
    return route.fulfill({
      json: {
        sizeBytes: hit.size * GB,
        sizeDisplay: `${hit.size} GB`,
        estimates: { 4096: { vramBytes: hit.vram * GB, vramDisplay: `${hit.vram} GB` } },
      },
    })
  })
  await page.route(/\/api\/models\/install\//, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop())
    state.installs.push(name)
    if (installStatus !== 200) return route.fulfill({ status: installStatus, json: { error: 'no space left' } })
    if (installOnPost) {
      const type = ALL_TYPES.find(t => INSTALLED[t].includes(name))
      if (type && !state.types.includes(type)) state.types.push(type)
    }
    return route.fulfill({ json: { uuid: 'job-1', status: 'ok' } })
  })
  await page.route(/\/api\/models(\?.*)?$/, route => {
    const tag = new URL(route.request().url()).searchParams.get('tag')
    const models = (GALLERY[tag] || []).map(m => ({ name: m.name, id: m.name, description: m.description, installed: false }))
    return route.fulfill({ json: { models, total_pages: 1 } })
  })
  return state
}

// --- Files the history points at -------------------------------------------

const PALETTES = [
  ['#f6c8a5', '#6f9db8'], ['#a9c9b8', '#2f5a52'], ['#d8b27a', '#2a5b63'], ['#b7bfd6', '#3d4a73'], ['#e7b3a0', '#7a4b57'],
]

export function pictureSvg(seed = 0, w = 640, h = 420) {
  const [a, b] = PALETTES[seed % PALETTES.length]
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient></defs>
<rect width="${w}" height="${h}" fill="url(#g)"/>
<path d="M0 ${h * 0.72} Q ${w * 0.25} ${h * 0.55} ${w * 0.5} ${h * 0.7} T ${w} ${h * 0.64} V ${h} H0 Z" fill="${b}" opacity=".55"/>
<circle cx="${w * 0.72}" cy="${h * 0.28}" r="${h * 0.09}" fill="#fff" opacity=".6"/></svg>`
}

// One second of silence as a WAV, so a hand-off that fetches the source gets
// a real audio blob.
export function silentWav(seconds = 1) {
  const rate = 8000
  const samples = rate * seconds
  const buf = Buffer.alloc(44 + samples * 2)
  buf.write('RIFF', 0); buf.writeUInt32LE(36 + samples * 2, 4); buf.write('WAVE', 8)
  buf.write('fmt ', 12); buf.writeUInt32LE(16, 16); buf.writeUInt16LE(1, 20); buf.writeUInt16LE(1, 22)
  buf.writeUInt32LE(rate, 24); buf.writeUInt32LE(rate * 2, 28); buf.writeUInt16LE(2, 32); buf.writeUInt16LE(16, 34)
  buf.write('data', 36); buf.writeUInt32LE(samples * 2, 40)
  return buf
}

export async function stubMedia(page) {
  await page.route('**/generated-images/**', route => {
    const n = [...new URL(route.request().url()).pathname].reduce((s, c) => s + c.charCodeAt(0), 0)
    route.fulfill({ contentType: 'image/svg+xml', body: pictureSvg(n) })
  })
  await page.route('**/generated-audio/**', route => route.fulfill({ contentType: 'audio/wav', body: silentWav() }))
  await page.route('**/generated-videos/**', route => route.fulfill({ status: 404, body: '' }))
}

// --- History ---------------------------------------------------------------

export const KEYS = {
  image: 'localai_image_history',
  video: 'localai_video_history',
  tts: 'localai_tts_history',
  sound: 'localai_sound_history',
  'audio-transform': 'localai_audio_transform_history',
  diarization: 'localai_diarization_history',
  favourites: 'localai_studio_favourites',
}

const HARBOUR = 'A fishing harbour at first light, pastel houses, long reflections, 35 mm film grain'

// A morning's work: a harbour picture with a second take and a clip made from
// it (one project of three), a narration that came from nothing, a forest, a
// bowl, a song, a cleaned-up recording and a meeting.
export function sampleHistory(now = Date.now()) {
  const ago = (minutes) => now - minutes * 60_000
  return {
    image: [
      { id: 'img-bowls', createdAt: ago(1500), prompt: 'Studio still life, three ceramic bowls on linen, soft window light', model: 'flux.1-schnell', params: { size: '1024x768' }, results: [{ url: '/generated-images/bowls.png' }] },
      { id: 'img-forest', createdAt: ago(420), prompt: 'Misty pine forest at sunrise, soft light between the trunks', model: 'flux.1-schnell', params: { size: '768x1024' }, results: [{ url: '/generated-images/forest.png' }] },
      { id: 'img-harbour-2', createdAt: ago(190), prompt: HARBOUR, model: 'flux.1-schnell', params: { size: '1216x832' }, results: [{ url: '/generated-images/harbour-2.png' }], parentId: 'img-harbour', edge: 'take' },
      { id: 'img-harbour', createdAt: ago(195), prompt: HARBOUR, model: 'flux.1-schnell', params: { size: '1216x832' }, results: [{ url: '/generated-images/harbour.png' }] },
    ],
    video: [
      { id: 'vid-push', createdAt: ago(60), prompt: 'Slow push-in across the water, gulls crossing the frame', model: 'wan2.1-t2v-1.3b', params: { size: '832x480' }, elapsedMs: 118000, results: [{ url: '/generated-videos/push.mp4' }], parentId: 'img-harbour', edge: 'animate' },
    ],
    tts: [
      { id: 'tts-welcome', createdAt: ago(40), prompt: 'Welcome to the harbour tour. The first boats leave at six, and the bakery opens at five.', model: 'kokoro-82m', params: {}, results: [{ url: '/generated-audio/welcome.wav' }] },
    ],
    sound: [
      { id: 'snd-rain', createdAt: ago(300), prompt: 'Lo-fi piano with rain on glass', model: 'ace-step', params: { mode: 'simple' }, results: [{ url: '/generated-audio/rain.wav' }] },
    ],
    'audio-transform': [
      { id: 'trf-clean', createdAt: ago(900), prompt: 'audio: standup.wav', model: 'localvqe', params: {}, results: [{ kind: 'output', url: '/generated-audio/standup-clean.wav' }, { kind: 'input', url: '/generated-audio/standup.wav' }] },
    ],
    diarization: [
      { id: 'dia-sync', createdAt: ago(2200), prompt: 'team-sync-oct-2.wav', model: 'sortformer-4spk', params: { speakers: 3, seconds: 252 }, results: [] },
    ],
  }
}

// Write entries to browser storage before the page loads, once per browser
// session, so a reload after "Clear history" does not bring them back.
export async function seedHistory(page, history, { favourites = [] } = {}) {
  await page.addInitScript(({ history, favourites, keys }) => {
    if (sessionStorage.getItem('studio-seeded')) return
    sessionStorage.setItem('studio-seeded', '1')
    for (const [type, entries] of Object.entries(history)) {
      if (entries.length) localStorage.setItem(keys[type], JSON.stringify(entries))
    }
    if (favourites.length) localStorage.setItem(keys.favourites, JSON.stringify(favourites))
  }, { history, favourites, keys: KEYS })
}

// 3D history is IndexedDB. Written from the page, then the page is reloaded.
export async function seedThreeD(page, entries) {
  await page.evaluate(async (rows) => {
    await new Promise((resolve, reject) => {
      const open = indexedDB.open('localai-3d-history', 1)
      open.onupgradeneeded = () => {
        const store = open.result.createObjectStore('generations', { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt')
      }
      open.onsuccess = () => {
        const tx = open.result.transaction('generations', 'readwrite')
        for (const row of rows) tx.objectStore('generations').put(row)
        tx.oncomplete = () => { open.result.close(); resolve() }
        tx.onerror = () => reject(tx.error)
      }
      open.onerror = () => reject(open.error)
    })
  }, entries)
}

export const readStore = (page, key) => page.evaluate(k => JSON.parse(localStorage.getItem(k) || 'null'), key)

// The composer has no type chips: the tabs above it are the modes. A type is
// picked with Alt+1..7, in the order the composer lists in data-types.
export const composer = (page) => page.getByTestId('studio-composer')
export async function pickType(page, key) {
  const types = (await composer(page).getAttribute('data-types')).split(' ')
  const n = types.indexOf(key) + 1
  await expect(async () => {
    await page.keyboard.press(`Alt+Digit${n}`)
    await expect(composer(page)).toHaveAttribute('data-type', key, { timeout: 1500 })
  }).toPass({ timeout: 10_000 })
}
