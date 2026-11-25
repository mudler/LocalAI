// Pure helpers of watches: the rule form, the times of reminders and the
// undo of an action. A watch is an event trigger of an add-on with a
// reminder rule; its reminders are listed under it.

/** The default-reminder choices of the form, in minutes from the item's time. */
export const OFFSET_CHOICES = [0, -5, -10, -15, -30, -60, -120, -1440]

/** The rule form of a new watch. */
export function emptyWatch() {
  return { source: '', types: [], offset: '-15', noDefault: false, allDay: 'morning', quietFrom: '', quietTo: '', tasks: '' }
}

/** The rule form of a stored watch. */
export function watchForm(tr) {
  const w = tr.watch || {}
  return {
    source: tr.source || '', types: Array.isArray(w.types) ? w.types : [],
    offset: w.no_default ? 'none' : String(w.offset_minutes || 0), noDefault: !!w.no_default,
    allDay: w.all_day || 'morning', quietFrom: w.quiet_from || '', quietTo: w.quiet_to || '', tasks: w.tasks || '',
  }
}

/** The watch part of a create or replace request. */
export function watchPayload(f) {
  const none = f.offset === 'none'
  return {
    offset_minutes: none ? 0 : Number(f.offset) || 0,
    no_default: none || undefined,
    all_day: f.allDay === 'none' ? 'none' : undefined,
    quiet_from: f.quietFrom && f.quietTo ? f.quietFrom : undefined,
    quiet_to: f.quietFrom && f.quietTo ? f.quietTo : undefined,
    tasks: (f.tasks || '').trim() || undefined,
    types: f.types && f.types.length ? f.types : undefined,
  }
}

/** What is wrong with the rule form, as a key of chats:watch.problem, or ''. */
export function watchProblem(f, sources) {
  if (!f.source) return 'source'
  if (sources && !sources.some((s) => s.id === f.source)) return 'source'
  if (!!f.quietFrom !== !!f.quietTo) return 'quiet'
  return ''
}

/** The parts of an offset in minutes for the words of the page: { key, count }. */
export function offsetParts(minutes) {
  const m = Math.abs(Number(minutes) || 0)
  if (m === 0) return { key: 'atTime', count: 0 }
  const after = Number(minutes) > 0
  if (m % 1440 === 0) return { key: after ? 'daysAfter' : 'daysBefore', count: m / 1440 }
  if (m % 60 === 0) return { key: after ? 'hoursAfter' : 'hoursBefore', count: m / 60 }
  return { key: after ? 'minutesAfter' : 'minutesBefore', count: m }
}

/** A due time for the owner: "Thu 15:45", with the date when it is a week or more away. */
export function whenText(iso, zone, now = new Date(), locale) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const far = Math.abs(d.getTime() - now.getTime()) >= 6 * 24 * 3600 * 1000
  const opts = { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false }
  if (far) Object.assign(opts, { day: 'numeric', month: 'short' })
  if (zone) opts.timeZone = zone
  try {
    return new Intl.DateTimeFormat(locale, opts).format(d)
  } catch {
    return d.toISOString().slice(0, 16).replace('T', ' ')
  }
}

/** The value of a datetime-local input for an instant in a zone. */
export function localInput(iso, zone) {
  if (!iso) return ''
  const d = new Date(iso)
  try {
    const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
      timeZone: zone || undefined, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
    }).formatToParts(d).map((p) => [p.type, p.value]))
    const hour = parts.hour === '24' ? '00' : parts.hour
    return `${parts.year}-${parts.month}-${parts.day}T${hour}:${parts.minute}`
  } catch {
    return d.toISOString().slice(0, 16)
  }
}

/**
 * The patch that takes an action on a reminder back, from the reminder as it
 * was before: a snooze, a skip or a new time.
 */
export function undoPatch(before, action) {
  if (action === 'snooze') return { snoozed_to: before.snoozed_to || '' }
  if (action === 'skip') return { skip: !!before.skipped }
  if (action === 'retime') {
    return before.at ? { at: before.at } : { offset_minutes: before.offset_minutes || 0 }
  }
  return null
}

/** The patch of a new time chosen in the form: "2026-11-26T14:45" in the watch's zone. */
export function retimePatch(value) {
  return { at: (value || '').replace('T', ' ') }
}

/**
 * Words the owner must never read: internal terms of the host. A watch is
 * never called a trigger, except as the name of the page.
 */
export const DENIED_WORDS = ['payload', 'reconcil', 'actor', 'dedupe', 'event id', ' key', 'webhook']

/** The denied words a text holds, lower case; empty when it is clean. */
export function deniedWords(text) {
  const t = ` ${String(text || '').toLowerCase()}`
  return DENIED_WORDS.filter((w) => t.includes(w))
}
