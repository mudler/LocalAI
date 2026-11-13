// Which model reads replies aloud: the text-to-speech default of the user.
// Pure, so the node unit tests load it.
import { CAP_TTS } from './capabilities.js'
import { defaultChoice, modelsFor } from './defaults.js'
import { isRemoteModel } from './sound.js'

// ttsModel says which model reads aloud, from the data of defaults.json and
// ui.json and the installed models (the list of /api/models/capabilities):
//   { state: 'model', model }      the model the user chose, installed
//   { state: 'automatic', model }  no choice: the first installed one
//   { state: 'ask' }               the user wants to be asked each time
//   { state: 'missing', model }    the chosen model is not installed
//   { state: 'none' }              no model can speak
// remote is true when that model sends the text to another server.
export function ttsModel(defaults, ui, models) {
  const choice = defaultChoice(defaults, ui, 'tts')
  const installed = modelsFor(models, CAP_TTS)
  const find = (id) => (models || []).find(m => m.id === id)
  if (choice.mode === 'ask') return { state: 'ask', model: '', remote: false }
  if (choice.mode === 'model') {
    const ok = installed.some(m => m.id === choice.model)
    return { state: ok ? 'model' : 'missing', model: choice.model, remote: ok && isRemoteModel(find(choice.model)) }
  }
  if (installed.length === 0) return { state: 'none', model: '', remote: false }
  return { state: 'automatic', model: installed[0].id, remote: isRemoteModel(find(installed[0].id)) }
}
