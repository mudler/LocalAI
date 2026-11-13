// Plays the notification sounds of ui.json with the Web Audio API. The sounds
// are synthesised (utils/sound.js TONES), so nothing is downloaded. Browsers
// refuse audio before the person has used the page, so the context is made on
// the first click, key press or touch, and no sound plays before that.

import { TONES, soundDecision } from './sound.js'

let ctx = null
let gesture = false
let current = null // the sound of ui.json, kept up to date by SoundSync
let listening = false

function audioContext() {
  if (ctx) return ctx
  const Ctor = typeof window !== 'undefined' ? (window.AudioContext || window.webkitAudioContext) : null
  if (!Ctor) return null
  try { ctx = new Ctor() } catch { ctx = null }
  return ctx
}

// markGesture records that the person used the page and wakes the context.
export function markGesture() {
  gesture = true
  const c = audioContext()
  if (c && c.state === 'suspended') c.resume().catch(() => {})
}

// listenForGesture starts the one-time listeners. The first real input is the
// gesture: it is never faked by the page.
export function listenForGesture() {
  if (listening || typeof window === 'undefined') return
  listening = true
  const events = ['pointerdown', 'keydown', 'touchstart']
  const once = () => {
    markGesture()
    events.forEach(e => window.removeEventListener(e, once, true))
  }
  events.forEach(e => window.addEventListener(e, once, true))
}

export const hasGesture = () => gesture

// setSound keeps the settings the bus decides with.
export function setSound(sound) { current = sound }

function inFront() {
  try { return document.visibilityState === 'visible' && document.hasFocus() } catch { return true }
}

// tone plays one named sound at a volume from 0 to 1. It answers false when
// the page cannot play yet.
export function tone(name, volume) {
  const notes = TONES[name]
  const c = audioContext()
  if (!notes || !c || !gesture) return false
  if (c.state === 'suspended') c.resume().catch(() => {})
  const start = c.currentTime + 0.02
  for (const n of notes) {
    const osc = c.createOscillator()
    const gain = c.createGain()
    osc.type = n.wave
    osc.frequency.value = n.f
    const peak = Math.max(0.0001, volume * 0.4 * (n.gain ?? 1))
    gain.gain.setValueAtTime(0.0001, start + n.at)
    gain.gain.exponentialRampToValueAtTime(peak, start + n.at + 0.015)
    gain.gain.exponentialRampToValueAtTime(0.0001, start + n.at + n.len)
    osc.connect(gain).connect(c.destination)
    osc.start(start + n.at)
    osc.stop(start + n.at + n.len + 0.05)
  }
  return true
}

// A hook for the tests: every sound that played, with its event.
function record(entry) {
  try { (window.__localaiSounds ||= []).push(entry) } catch { /* no window */ }
}

// notify plays the sound of an event when the settings, the quiet hours, the
// gesture and the place of the tab allow it. It answers the decision.
export function notify(event, now = new Date()) {
  if (!current) return { play: false, why: 'off' }
  const d = soundDecision(current, event, { gesture, front: inFront() }, now)
  if (d.play && tone(d.name, d.volume)) record({ event, name: d.name, volume: d.volume })
  return d
}

// preview plays a sound from the settings page: the click is the gesture.
export function preview(name, volume) {
  markGesture()
  const ok = tone(name, volume)
  if (ok) record({ event: 'preview', name, volume })
  return ok
}
