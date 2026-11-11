// The notification sounds and the voice for replies (the sound and voice keys
// of LocalAI/ui.json): pure rules, so the node unit tests load them. Playing
// is in soundPlayer.js. The server checks the same ranges in
// core/services/studio/wellknown/sound.go.

export const SOUND_EVENTS = Object.freeze(['replyDone', 'approval', 'taskDone', 'error'])
export const SOUND_NAMES = Object.freeze(['none', 'soft-chime', 'double-tap', 'bell', 'pop', 'low-tone'])
export const SOUND_DEFAULTS = Object.freeze({ replyDone: 'soft-chime', approval: 'double-tap', taskDone: 'bell', error: 'low-tone' })
export const DEFAULT_VOLUME = 0.5

// What each event says is already on the screen where the person looks, so
// these three stay quiet while the tab is in the front. A task that finished
// somewhere else (a trigger, an agent job) always plays.
export const QUIET_IN_FRONT = Object.freeze(['replyDone', 'approval', 'error'])

// Each sound is a few notes: frequency in Hz, start and length in seconds,
// and the wave. They are synthesised, so nothing is downloaded.
export const TONES = Object.freeze({
  'soft-chime': [{ f: 784, at: 0, len: 0.5, wave: 'sine' }, { f: 1175, at: 0.12, len: 0.6, wave: 'sine' }],
  'double-tap': [{ f: 660, at: 0, len: 0.12, wave: 'triangle' }, { f: 660, at: 0.18, len: 0.12, wave: 'triangle' }],
  bell: [{ f: 988, at: 0, len: 1.1, wave: 'sine' }, { f: 1976, at: 0, len: 0.6, wave: 'sine', gain: 0.3 }],
  pop: [{ f: 520, at: 0, len: 0.09, wave: 'square', gain: 0.5 }],
  'low-tone': [{ f: 196, at: 0, len: 0.45, wave: 'triangle' }, { f: 165, at: 0.2, len: 0.5, wave: 'triangle' }],
})

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)

// parseTime reads "HH:MM" as minutes after midnight, or null.
export function parseTime(s) {
  const m = /^([01][0-9]|2[0-3]):([0-5][0-9])$/.exec(typeof s === 'string' ? s : '')
  return m ? Number(m[1]) * 60 + Number(m[2]) : null
}

// soundOf reads the sound key of ui.json with every default filled in. A value
// outside its range is left at its default, so a file the server refused (and
// shows as its last good version) never breaks playing.
export function soundOf(ui) {
  const s = isObject(ui?.sound) ? ui.sound : {}
  const events = { ...SOUND_DEFAULTS }
  if (isObject(s.events)) for (const e of SOUND_EVENTS) if (SOUND_NAMES.includes(s.events[e])) events[e] = s.events[e]
  const volume = typeof s.volume === 'number' && s.volume >= 0 && s.volume <= 1 ? s.volume : DEFAULT_VOLUME
  let quiet = null
  if (Array.isArray(s.quietHours) && s.quietHours.length === 2) {
    const from = parseTime(s.quietHours[0])
    const to = parseTime(s.quietHours[1])
    if (from !== null && to !== null && from !== to) quiet = { from, to, fromText: s.quietHours[0], toText: s.quietHours[1] }
  }
  return { enabled: s.enabled === true, volume, quiet, events }
}

// inQuietHours says whether a moment falls inside the quiet span. The span may
// cross midnight (22:00 to 07:00). The end is not inside.
export function inQuietHours(quiet, date = new Date()) {
  if (!quiet) return false
  const now = date.getHours() * 60 + date.getMinutes()
  return quiet.from < quiet.to ? now >= quiet.from && now < quiet.to : now >= quiet.from || now < quiet.to
}

// soundDecision answers whether an event plays now, and the name of the sound,
// or why not (a code the tests read):
//   state { gesture: the person used the page, front: the tab is in the front }
export function soundDecision(sound, event, state = {}, date = new Date()) {
  if (!SOUND_EVENTS.includes(event)) return { play: false, why: 'event' }
  if (!sound.enabled) return { play: false, why: 'off' }
  const name = sound.events[event]
  if (name === 'none') return { play: false, why: 'none' }
  if (!state.gesture) return { play: false, why: 'gesture' }
  if (inQuietHours(sound.quiet, date)) return { play: false, why: 'quiet' }
  if (state.front && QUIET_IN_FRONT.includes(event)) return { play: false, why: 'front' }
  if (sound.volume <= 0) return { play: false, why: 'volume' }
  return { play: true, name, volume: sound.volume }
}

export const READ_ALOUD = Object.freeze(['off', 'button', 'auto'])
export const SPEED = Object.freeze({ min: 0.5, max: 2, step: 0.25 })

// voiceOf reads the voice key of ui.json with every default filled in.
export function voiceOf(ui) {
  const v = isObject(ui?.voice) ? ui.voice : {}
  return {
    readAloud: READ_ALOUD.includes(v.readAloud) ? v.readAloud : 'off',
    voice: typeof v.voice === 'string' ? v.voice.trim() : '',
    speed: typeof v.speed === 'number' && v.speed >= SPEED.min && v.speed <= SPEED.max ? v.speed : 1,
    skipCode: v.skipCode !== false,
  }
}

// The backends that send a request to another server. A model on one of them
// is not local: reading aloud sends the text of a reply to it.
const REMOTE_BACKENDS = ['cloud-proxy', 'localai-proxy']
export const isRemoteModel = (model) => REMOTE_BACKENDS.includes(model?.backend)

// readAloudText turns the markdown of a reply into the text to speak: code
// blocks and tables become the short phrases of omitted(kind) when skipCode is
// on, and the marks that read badly aloud are dropped (headings, emphasis,
// link targets, inline code ticks).
export function readAloudText(markdown, { skipCode = true, omitted = (kind) => kind } = {}) {
  let text = String(markdown || '').replace(/\r\n?/g, '\n')
  const fence = /^(```|~~~)[^\n]*\n[\s\S]*?(?:\n\1[^\n]*(?=\n|$)|$)/gm
  text = skipCode ? text.replace(fence, `\n${omitted('code')}\n`) : text.replace(fence, (block) => block.replace(/^(```|~~~)[^\n]*\n?/, '').replace(/\n?(```|~~~)\s*$/, ''))
  if (skipCode) text = text.replace(/(?:^[ \t]*\|.*\|[ \t]*\n?){2,}/gm, `${omitted('table')}\n`)
  return text
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^[ \t]*#{1,6}[ \t]+/gm, '')
    .replace(/(\*\*|__|\*|_|~~)(\S(?:[^*_~]*\S)?)\1/g, '$2')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/^[ \t]*[-*+][ \t]+/gm, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}
