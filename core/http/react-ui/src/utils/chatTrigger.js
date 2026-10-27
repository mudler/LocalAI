// The schedule of a trigger is one line of text that the server reads, for
// example "weekdays 08:00". These functions turn the fields of the form into
// that text and back. No React, so a node test drives them.

export const presetOrder = ['daily', 'weekdays', 'weekly', 'monthly', 'hourly', 'everyHours', 'everyMinutes', 'once', 'cron', 'file']

/** The minutes past the hour that "Every hour at minute" offers. */
export const HOURLY_MINUTES = [0, 5, 10, 15, 20, 30, 45]
/** The steps of "Every N hours". */
export const EVERY_HOURS = [2, 3, 4, 6, 8, 12]
/** The steps of "Every N minutes". */
export const EVERY_MINUTES = [5, 10, 15, 20, 30]

export const dayCodes = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']

export function emptyForm() {
  return { preset: 'daily', time: '08:00', days: ['mon'], day: 1, date: '', cron: '0 8 * * 1-5', minute: 0, hours: 6, minutes: 15 }
}

/** The text that the server reads. An incomplete form gives text that the server refuses with a reason. */
export function scheduleText(f) {
  switch (f.preset) {
    case 'daily': return `daily ${f.time}`
    case 'weekdays': return `weekdays ${f.time}`
    case 'weekly': return `weekly ${dayCodes.filter((d) => f.days.includes(d)).join(',')} ${f.time}`
    case 'monthly': return `monthly ${f.day} ${f.time}`
    case 'hourly': return `${f.minute} * * * *`
    case 'everyHours': return `0 */${f.hours} * * *`
    case 'everyMinutes': return `*/${f.minutes} * * * *`
    case 'once': return `once ${f.date} ${f.time}`
    case 'cron': return f.cron.trim()
    default: return ''
  }
}

/** A time of day as the server writes it: HH:MM, hours 00 to 23, minutes 00 to 59. */
function validTime(s) {
  return /^\d{2}:\d{2}$/.test(s) && Number(s.slice(0, 2)) <= 23 && Number(s.slice(3)) <= 59
}

/**
 * The fields for a schedule text. A text becomes a preset only when the preset
 * writes back exactly the same text; anything else (other case, unknown days,
 * a bad day number, extra spaces) stays as an advanced "cron" text, unchanged.
 */
export function formOf(text) {
  const trimmed = text.trim()
  const advanced = { ...emptyForm(), preset: 'cron', cron: trimmed }
  const f = emptyForm()
  const w = trimmed.split(' ').filter((x) => x !== '')
  const head = w[0] ?? ''
  const last = w[w.length - 1] ?? ''
  let found = null
  if (w.length === 2 && (head === 'daily' || head === 'weekdays') && validTime(last)) {
    found = { ...f, preset: head === 'daily' ? 'daily' : 'weekdays', time: last }
  } else if (w.length === 3 && head === 'weekly' && validTime(last)) {
    const days = (w[1] ?? '').split(',')
    if (days.every((d) => dayCodes.includes(d))) found = { ...f, preset: 'weekly', days, time: last }
  } else if (w.length === 3 && head === 'monthly' && validTime(last)) {
    const day = Number(w[1])
    if (/^\d{1,2}$/.test(w[1] ?? '') && day >= 1 && day <= 31) found = { ...f, preset: 'monthly', day, time: last }
  } else if (w.length === 3 && head === 'once' && /^\d{4}-\d{2}-\d{2}$/.test(w[1] ?? '') && validTime(last)) {
    found = { ...f, preset: 'once', date: w[1] ?? '', time: last }
  } else if (w.length === 5) {
    const num = (re, x) => (re.test(x) ? Number(x) : NaN)
    const rest = w.slice(2).join(' ')
    const minute = num(/^\d{1,2}$/, head)
    const stepM = /^\*\/(\d{1,2})$/.exec(head)
    const stepH = /^\*\/(\d{1,2})$/.exec(w[1] ?? '')
    if (rest === '* * *' && w[1] === '*' && HOURLY_MINUTES.includes(minute)) found = { ...f, preset: 'hourly', minute }
    else if (rest === '* * *' && head === '0' && stepH && EVERY_HOURS.includes(Number(stepH[1]))) found = { ...f, preset: 'everyHours', hours: Number(stepH[1]) }
    else if (rest === '* * *' && w[1] === '*' && stepM && EVERY_MINUTES.includes(Number(stepM[1]))) found = { ...f, preset: 'everyMinutes', minutes: Number(stepM[1]) }
  }
  if (found === null || scheduleText(found) !== trimmed) return advanced
  return found
}

/** The fields of a stored trigger: a file trigger is the preset "file"; a schedule is read as formOf does. */
export function presetOf(t) {
  return t.kind === 'file' ? { ...emptyForm(), preset: 'file' } : formOf(t.schedule || '')
}

/** The IANA name of the time zone of the browser; UTC when the browser does not say. */
export function browserZone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

function zoneName(d, zone) {
  const names = ['en-GB', 'en-US'].map((l) => new Intl.DateTimeFormat(l, { timeZone: zone, timeZoneName: 'short' }).formatToParts(d).find((p) => p.type === 'timeZoneName')?.value ?? '')
  return names.find((n) => n !== '' && !/^GMT[+-]/.test(n)) ?? names[0] ?? ''
}

/**
 * A time of a schedule, read in the time zone of the trigger: "Mon 26 Oct, 07:00".
 * An empty zone is the zone of the browser. A zone other than the browser's gets
 * its short name after the time. The year is added when it is not the current
 * year in that zone.
 */
export function formatNext(iso, tz) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const here = browserZone()
  const zone = tz || here
  const yearOf = (ts) => new Intl.DateTimeFormat('en-GB', { year: 'numeric', timeZone: zone }).format(ts)
  const opts = { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  try {
    const other = yearOf(d) !== yearOf(new Date())
    const text = d.toLocaleString('en-GB', { ...opts, year: other ? 'numeric' : undefined, timeZone: zone })
    return zone === here ? text : `${text} ${zoneName(d, zone)}`
  } catch {
    return d.toLocaleString('en-GB', { ...opts, timeZone: here })
  }
}

/** The times of the preview, separated by semicolons. */
export function joinTimes(times, tz) {
  return times.map((t) => formatNext(t, tz)).filter(Boolean).join('; ')
}

/** The body of a create or replace request from the fields of the form. */
export function triggerPayload(v) {
  const file = v.schedule.preset === 'file'
  const body = {
    name: v.name.trim(), prompt: v.prompt, kind: file ? 'file' : 'schedule', enabled: v.enabled,
    daily_cap: v.dailyCap ? Number(v.dailyCap) : 0,
    agent_id: v.agentId || undefined, model: v.model || undefined, source_chat_id: v.sourceChatId || undefined,
  }
  if (file) {
    body.folder = v.folder.trim()
    body.quiet_seconds = v.quiet ? Number(v.quiet) : 0
  } else {
    body.schedule = scheduleText(v.schedule)
    body.time_zone = v.zone
  }
  return body
}

/** A short word for the state of a run. */
export function runTone(status) {
  if (status === 'done') return 'done'
  if (status === 'failed') return 'failed'
  if (status === 'skipped') return 'skipped'
  return 'going'
}

/** The key a browser needs for push: a URL-safe base64 string as bytes. */
export function urlBase64ToBytes(b64) {
  const pad = '='.repeat((4 - (b64.length % 4)) % 4)
  const raw = atob((b64 + pad).replace(/-/g, '+').replace(/_/g, '/'))
  return Uint8Array.from(raw, (c) => c.charCodeAt(0))
}
