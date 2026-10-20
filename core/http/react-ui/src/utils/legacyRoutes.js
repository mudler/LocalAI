// The old routes of the migration table and where they go now. Redirects stay
// in the app until a separate decision removes them.

// Old generator route segment -> Studio tab key (pages/Studio.jsx MODALITIES).
export const STUDIO_TABS = Object.freeze({
  image: 'images',
  video: 'video',
  '3d': 'threed',
  tts: 'tts',
  sound: 'sound',
  diarization: 'diarization',
  transform: 'transform',
})

// The target of an old generator route. The model segment, the query string
// and the hash are kept as they came.
export function studioRedirect(legacy, model, search = '', hash = '') {
  const tab = STUDIO_TABS[legacy]
  const tail = model ? `/${encodeURIComponent(model)}` : ''
  return `/app/studio/${tab}${tail}${search}${hash}`
}

// One row per old route of the spec's migration table that changes or keeps
// its path. `kind` is 'same' (no redirect), 'studio-tab' or 'addon-page'.
export const MIGRATION_ROUTES = Object.freeze([
  { from: '/app/chat', to: '/app/chat', kind: 'same' },
  { from: '/app/models', to: '/app/models', kind: 'same' },
  { from: '/app/settings', to: '/app/settings', kind: 'same' },
  { from: '/app/studio', to: '/app/studio', kind: 'same' },
  { from: '/app/talk', to: '/app/talk', kind: 'same' },
  { from: '/app/image', to: '/app/studio/images', kind: 'studio-tab' },
  { from: '/app/video', to: '/app/studio/video', kind: 'studio-tab' },
  { from: '/app/3d', to: '/app/studio/threed', kind: 'studio-tab' },
  { from: '/app/tts', to: '/app/studio/tts', kind: 'studio-tab' },
  { from: '/app/sound', to: '/app/studio/sound', kind: 'studio-tab' },
  { from: '/app/diarization', to: '/app/studio/diarization', kind: 'studio-tab' },
  { from: '/app/transform', to: '/app/studio/transform', kind: 'studio-tab' },
  { from: '/app/face', to: '/app/addons/face/recognition', kind: 'addon-page' },
  { from: '/app/voice', to: '/app/addons/voice/recognition', kind: 'addon-page' },
  { from: '/app/voice-library', to: '/app/addons/voice/library', kind: 'addon-page' },
  { from: '/app/voice-library/new', to: '/app/addons/voice/create', kind: 'addon-page' },
  { from: '/app/fine-tune', to: '/app/addons/fine-tuning/jobs', kind: 'addon-page' },
  { from: '/app/quantize', to: '/app/addons/quantization/jobs', kind: 'addon-page' },
])
