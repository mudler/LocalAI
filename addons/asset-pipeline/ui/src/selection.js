// SPDX-License-Identifier: MIT
const key = 'localai.asset-pipeline.selected-resource'

export function restoredSelection(resources) {
  let saved
  try { saved = localStorage.getItem(key) } catch { /* Storage may be disabled. */ }
  return resources.find(resource => resource.id === saved)?.id || resources[0]?.id || ''
}

export function rememberSelection(id) {
  try { localStorage.setItem(key, id) } catch { /* Navigation must still work without storage. */ }
}
