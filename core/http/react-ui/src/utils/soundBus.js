// Where the app says that something happened. emitSound(event) is called from
// the chat stream and the inbox; soundPlayer.notify decides whether it plays.
// Kept apart from the player so the stream hooks stay free of audio code, and
// so an event costs nothing while the sound is off.
import { notify } from './soundPlayer.js'

export function emitSound(event) {
  try { notify(event) } catch { /* a sound never breaks the app */ }
}
