// The settings a chat keeps (PATCH /api/chats/{id} with { settings }): its own
// system prompt, the sampling values, the reasoning effort and the context
// size. They apply from the next turn. Pure rules, so a node test drives them.
//
// The form holds strings, as the inputs do. An empty field means "the model's
// own value" and is left out of the request.

export const REASONING_EFFORTS = ['', 'none', 'minimal', 'low', 'medium', 'high']

// The ranges the server accepts (core/services/studio/chatsettings.go) and the
// part of each a slider covers. min and max are the slider's ends; a typed value
// may go past `max` where the server allows it (top_k has no upper limit).
// `exclusiveMin` is set when the server needs a value above `min`, so the
// slider starts at one step. step is also the precision of the number box.
export const SETTING_RANGES = {
  temperature: { min: 0, max: 2, step: 0.05, integer: false },
  top_p: { min: 0, max: 1, step: 0.01, integer: false, exclusiveMin: true },
  top_k: { min: 0, max: 200, step: 1, integer: true, unbounded: true },
}

const text = (v) => (v === undefined || v === null ? '' : String(v))

// The form values of the settings the server returned (absent when none).
export function settingsForm(settings) {
  const s = settings || {}
  return {
    system_prompt: text(s.system_prompt),
    temperature: text(s.temperature),
    top_p: text(s.top_p),
    top_k: text(s.top_k),
    reasoning_effort: text(s.reasoning_effort),
    context_size: s.context_size ? String(s.context_size) : '',
  }
}

const num = (v) => (String(v).trim() === '' ? undefined : Number(v))

// What is wrong with the form, as { field: reason key }, by the rules the
// server checks. An empty object means it can be saved.
export function settingsErrors(form) {
  const errors = {}
  const t = num(form.temperature)
  if (t !== undefined && !(t >= SETTING_RANGES.temperature.min && t <= SETTING_RANGES.temperature.max)) errors.temperature = 'temperature'
  const p = num(form.top_p)
  if (p !== undefined && !(p > SETTING_RANGES.top_p.min && p <= SETTING_RANGES.top_p.max)) errors.top_p = 'topP'
  const k = num(form.top_k)
  if (k !== undefined && !(Number.isInteger(k) && k >= SETTING_RANGES.top_k.min)) errors.top_k = 'topK'
  const c = num(form.context_size)
  if (c !== undefined && !(Number.isInteger(c) && c >= 512 && c <= 4 << 20)) errors.context_size = 'contextSize'
  if (!REASONING_EFFORTS.includes(form.reasoning_effort || '')) errors.reasoning_effort = 'effort'
  return errors
}

// The settings object of the request. The server replaces the settings as a
// whole, so every field is sent as the form has it.
export function settingsBody(form) {
  const out = {}
  if (form.system_prompt.trim()) out.system_prompt = form.system_prompt
  for (const key of ['temperature', 'top_p']) {
    const v = num(form[key])
    if (v !== undefined) out[key] = v
  }
  for (const key of ['top_k', 'context_size']) {
    const v = num(form[key])
    if (v !== undefined) out[key] = Math.trunc(v)
  }
  if (form.reasoning_effort) out.reasoning_effort = form.reasoning_effort
  return out
}

// The settings object to send when only the reasoning effort changes. The
// server replaces the settings as a whole, so the other fields go along as
// the chat has them. '' returns the effort to the model's own.
export function settingsWithEffort(settings, effort) {
  return settingsBody({ ...settingsForm(settings), reasoning_effort: effort || '' })
}

// True when the form differs from what the server keeps.
export function settingsChanged(form, settings) {
  const a = settingsForm(settings)
  return Object.keys(a).some((k) => a[k] !== form[k])
}

// The meter warns from CONTEXT_WARN_PCT percent (the next turns compact the
// chat) and is hot from CONTEXT_HOT_PCT. The bar marks both on its track.
export const CONTEXT_WARN_PCT = 80
export const CONTEXT_HOT_PCT = 90

// What the context meter of the composer says, from the last usage event.
// pct and left are null when the server does not know the window.
export function contextReading(usage) {
  if (!usage || !(usage.used >= 0)) return null
  const limit = usage.limit > 0 ? usage.limit : 0
  const pct = limit ? Math.min(100, Math.round((usage.used / limit) * 100)) : null
  return {
    used: usage.used,
    limit,
    pct,
    left: limit ? Math.max(0, limit - usage.used) : null,
    tone: pct === null ? 'ok' : pct >= CONTEXT_HOT_PCT ? 'err' : pct >= CONTEXT_WARN_PCT ? 'warn' : 'ok',
    speed: usage.tokens_per_second > 0 ? Math.round(usage.tokens_per_second * 10) / 10 : null,
  }
}

// "1.2k" for the meter: tokens are counts, not bytes.
export function tokenCount(n) {
  if (!(n >= 0)) return '0'
  if (n < 1000) return String(n)
  if (n < 100000) return `${Math.round(n / 100) / 10}k`
  return `${Math.round(n / 1000)}k`
}
