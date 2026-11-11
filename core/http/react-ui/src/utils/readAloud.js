// Reads a reply aloud with the default text-to-speech model of the user
// (LocalAI/defaults.json, key tts). One reply plays at a time. The text is sent
// to that model, so the settings page names it and says when it is remote.
import { ttsApi, modelsApi } from './api'
import { ttsModel } from './ttsModel.js'

export { ttsModel }

let audio = null
let objectUrl = ''
let token = 0
let state = { status: 'idle', id: '' }
const listeners = new Set()

function set(next) {
  state = next
  listeners.forEach(fn => fn())
}

export const readAloudState = () => state
export function subscribeReadAloud(fn) {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

function release() {
  if (audio) { audio.onended = null; audio.onerror = null; audio.pause() }
  audio = null
  if (objectUrl) { URL.revokeObjectURL(objectUrl); objectUrl = '' }
}

export function stopReadAloud() {
  token++
  release()
  set({ status: 'idle', id: '' })
}

let modelCache = null
export async function installedModels() {
  if (!modelCache) modelCache = modelsApi.listCapabilities().then(d => d?.data || []).catch((e) => { modelCache = null; throw e })
  return modelCache
}

// speak reads text aloud. id names the reply, so its button can show the
// state. It resolves when the speech ended or was stopped, and rejects with
// the reason when the model or the browser refused.
export async function speak(id, text, { model, voice = '', speed = 1 }) {
  stopReadAloud()
  const mine = ++token
  set({ status: 'loading', id })
  try {
    const { blob } = await ttsApi.generateV1({ model, input: text, ...(voice ? { voice } : {}), response_format: 'wav', stream: false })
    if (mine !== token) return
    objectUrl = URL.createObjectURL(blob)
    audio = new Audio(objectUrl)
    audio.playbackRate = speed
    await new Promise((resolve, reject) => {
      audio.onended = resolve
      audio.onerror = () => reject(new Error('audio'))
      set({ status: 'playing', id })
      audio.play().catch(reject)
    })
  } catch (e) {
    if (mine === token) { release(); set({ status: 'error', id }) }
    throw e
  }
  if (mine === token) { release(); set({ status: 'idle', id: '' }) }
}
