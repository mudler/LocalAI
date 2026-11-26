// Which installed models can fill each part of a turn. The list is the one of
// GET /api/models/capabilities: { id, capabilities: ['FLAG_CHAT', ...], backend,
// disabled, alias_error }. A pipeline is a row of GET /api/pipeline-models.

// The backend that turns a portrait and speech into a talking-head clip. Other
// video models take a prompt, not speech, so they cannot answer here.
export const AVATAR_BACKEND = 'avatarforcing-cpp'

// The part of a turn -> the capability flag a model needs for it.
export const ROLES = {
  chat: 'FLAG_CHAT',
  tts: 'FLAG_TTS',
  stt: 'FLAG_TRANSCRIPT',
  avatar: 'FLAG_VIDEO',
}

// The host sends the upper-case flag; an older host sent the bare lower-case name.
export function hasCapability(model, flag) {
  const caps = model?.capabilities || []
  return caps.includes(flag) || caps.includes(flag.replace(/^FLAG_/, '').toLowerCase())
}

export function isAvatarModel(model) {
  return hasCapability(model, ROLES.avatar) && model?.backend === AVATAR_BACKEND
}

// The model ids for each part, sorted by name. A disabled model or an alias
// that cannot be resolved cannot answer, so it is left out.
export function modelGroups(list) {
  const usable = (Array.isArray(list) ? list : []).filter((m) => m && m.id && !m.disabled && !m.alias_error)
  const ids = (keep) => usable.filter(keep).map((m) => m.id).sort((a, b) => a.localeCompare(b))
  return {
    chat: ids((m) => hasCapability(m, ROLES.chat)),
    tts: ids((m) => hasCapability(m, ROLES.tts)),
    stt: ids((m) => hasCapability(m, ROLES.stt)),
    avatar: ids(isAvatarModel),
    // Voice detection is a part of a pipeline only, never of a turn.
    vad: ids((m) => hasCapability(m, 'FLAG_VAD')),
  }
}

// The pipelines whose parts can be used one by one. A speech-to-speech model
// answers every part in one call, so its parts are not separate models.
export function splitPipelines(pipelines) {
  return (Array.isArray(pipelines) ? pipelines : [])
    .filter((p) => p && p.name && !p.any_to_any && !p.self_contained && p.llm && p.tts && p.transcription)
}

// The models a pipeline names, as the settings of this page.
export function fromPipeline(pipeline) {
  return { pipeline: pipeline.name, chat: pipeline.llm, tts: pipeline.tts, stt: pipeline.transcription, voice: pipeline.voice || '' }
}

// Fills each part with the saved choice when that model is still installed,
// or with the first model that can do it. `wanted` is the avatar model of a
// link (?model=), which wins when it is a talking-head model.
export function chooseModels(saved, groups, wanted) {
  const pick = (role) => {
    const list = groups[role] || []
    if (role === 'avatar' && wanted && list.includes(wanted)) return wanted
    return list.includes(saved?.[role]) ? saved[role] : (list[0] || '')
  }
  return { chat: pick('chat'), tts: pick('tts'), stt: pick('stt'), avatar: pick('avatar') }
}

// What stops a turn from running, for the empty states of the page. A missing
// chat model still allows "Say this"; a missing transcription model still
// allows typing.
export function missingParts(choice) {
  const out = []
  if (!choice.avatar) out.push('avatar')
  if (!choice.tts) out.push('tts')
  return out
}
