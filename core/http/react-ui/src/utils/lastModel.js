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
