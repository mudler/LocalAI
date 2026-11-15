// Remember the last model the user picked, keyed by capability, so returning to
// a page (Home chat box, Image, TTS, Talk...) defaults to that model instead of
// whatever happens to sort first. Only persisted when a capability key exists.
// localStorage access is wrapped because private-browsing modes throw.
const LAST_MODEL_PREFIX = 'localai_last_model:'

export function readLastModel(capability) {
  if (!capability) return null
  try { return localStorage.getItem(LAST_MODEL_PREFIX + capability) } catch { return null }
}

export function writeLastModel(capability, model) {
  if (!capability || !model) return
  try { localStorage.setItem(LAST_MODEL_PREFIX + capability, model) } catch { /* ignore */ }
}

// The chat choice of the user: defaultChoice for chat, from defaults.json (or
// the older defaultModel of ui.json), null until the files are read.
// AppearanceSync sets it; the picker of a new chat reads it (startPick in
// utils/defaults.js), and the other chat pickers start with its model.
let chatStart = null
const startListeners = new Set()

export function setChatStart(choice) {
  const next = choice && typeof choice === 'object' ? { mode: choice.mode, model: choice.model || '', from: choice.from || '' } : null
  if (JSON.stringify(next) === JSON.stringify(chatStart)) return
  chatStart = next
  for (const fn of startListeners) fn()
}

export function readChatStart() { return chatStart }

export function subscribeChatStart(fn) {
  startListeners.add(fn)
  return () => { startListeners.delete(fn) }
}

export function readPreferredModel(capability) {
  return capability === 'FLAG_CHAT' && chatStart?.mode === 'model' ? chatStart.model : ''
}
