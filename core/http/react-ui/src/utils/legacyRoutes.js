// The old routes of the migration table and where they go now. Redirects stay
// in the app until a separate decision removes them.

// Old generator route segment -> Studio tab key (the tabs of the Studio add-on).
export const STUDIO_TABS = Object.freeze({
  image: 'images',
  video: 'video',
  '3d': 'threed',
  tts: 'tts',
  sound: 'sound',
  diarization: 'diarization',
  transform: 'transform',
})

// Studio and Talk are add-ons. Their pages live under /app/addons/<id>/<page>;
// the tab is one path segment and the model travels as ?model=<id>, because
// an add-on path segment cannot hold "/" or ":" (see resolveSubpath in the SDK).
export const STUDIO_PAGE = '/app/addons/studio/studio'
export const TALK_PAGE = '/app/addons/talk/talk'

function addonAddress(base, { tab, model, search = '', hash = '' } = {}) {
  const params = new URLSearchParams(search)
  if (model) params.set('model', model)
  const query = params.toString()
  return `${base}${tab ? `/${encodeURIComponent(tab)}` : ''}${query ? `?${query}` : ''}${hash}`
}

// Where an old /app/studio[/tab[/model]] link opens now. The model, the query
// string and the hash are kept.
export function studioAddress(args) {
  return addonAddress(STUDIO_PAGE, args)
}

// Where an old /app/talk link opens now.
export function talkAddress(args) {
  return addonAddress(TALK_PAGE, args)
}

// The target of an old generator route.
export function studioRedirect(legacy, model, search = '', hash = '') {
  return studioAddress({ tab: STUDIO_TABS[legacy], model, search, hash })
}

// One row per old route of the spec's migration table that changes or keeps
// its path. `kind` is 'same' (no redirect), 'studio-tab' (a Studio tab) or
// 'addon-page' (a page of an add-on).
export const MIGRATION_ROUTES = Object.freeze([
  { from: '/app/chat', to: '/app/chat', kind: 'same' },
  { from: '/app/models', to: '/app/models', kind: 'same' },
  { from: '/app/settings', to: '/app/settings', kind: 'same' },
  { from: '/app/studio', to: '/app/addons/studio/studio', kind: 'addon-page' },
  { from: '/app/talk', to: '/app/addons/talk/talk', kind: 'addon-page' },
  { from: '/app/image', to: '/app/addons/studio/studio/images', kind: 'studio-tab' },
  { from: '/app/video', to: '/app/addons/studio/studio/video', kind: 'studio-tab' },
  { from: '/app/3d', to: '/app/addons/studio/studio/threed', kind: 'studio-tab' },
  { from: '/app/tts', to: '/app/addons/studio/studio/tts', kind: 'studio-tab' },
  { from: '/app/sound', to: '/app/addons/studio/studio/sound', kind: 'studio-tab' },
  { from: '/app/diarization', to: '/app/addons/studio/studio/diarization', kind: 'studio-tab' },
  { from: '/app/transform', to: '/app/addons/studio/studio/transform', kind: 'studio-tab' },
  { from: '/app/face', to: '/app/addons/face/recognition', kind: 'addon-page' },
  { from: '/app/voice', to: '/app/addons/voice/recognition', kind: 'addon-page' },
  { from: '/app/voice-library', to: '/app/addons/voice/library', kind: 'addon-page' },
  { from: '/app/voice-library/new', to: '/app/addons/voice/create', kind: 'addon-page' },
  { from: '/app/fine-tune', to: '/app/addons/fine-tuning/jobs', kind: 'addon-page' },
  { from: '/app/quantize', to: '/app/addons/quantization/jobs', kind: 'addon-page' },
])
