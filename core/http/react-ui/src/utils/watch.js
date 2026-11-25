// Pure helpers of watches: the rule form, the times of reminders and the
// undo of an action. A watch is an event trigger of an add-on with a
// reminder rule; its reminders are listed under it.

/** The default-reminder choices of the form, in minutes from the item's time. */
export const OFFSET_CHOICES = [0, -5, -10, -15, -30, -60, -120, -1440]

/** The rule form of a new watch. */
export function emptyWatch() {
  return { source: '', types: [], offset: '-15', allDay: 'morning', quietFrom: '', quietTo: '', tasks: '' }
}

/** The rule form of a stored watch. */
export function watchForm(tr) {
  const w = tr.watch || {}
  return {
    source: tr.source || '', types: Array.isArray(w.types) ? w.types : [],
    offset: w.no_default ? 'none' : String(w.offset_minutes || 0),
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

/**
 * The words of an offset in minutes from the item's time: "15 min before".
 * t is of the chats namespace, or prefix names it ("chats:").
 */
export function offsetWords(t, minutes, prefix = '') {
  const o = offsetParts(minutes)
  return t(`${prefix}watch.${o.key}`, { count: o.count })
}

/** The default reminder of a watch rule as stored: its offset, or none. */
export function ruleOffsetWords(t, rule, prefix = '') {
  return rule.no_default ? t(`${prefix}watch.none`) : offsetWords(t, rule.offset_minutes, prefix)
}

/** When a reminder fires against its item: why the rule moved it, or its offset. */
export function reminderOffsetWords(t, r, prefix = '') {
  if (r.adjusted === 'quiet') return t(`${prefix}watch.adjQuiet`)
  if (r.adjusted === 'all_day') return t(`${prefix}watch.adjAllDay`)
  return offsetWords(t, r.offset_minutes, prefix)
}

/** The zone of the viewer's clock. */
function viewerZone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

/**
 * A due time for the owner: "Thu 15:45" in the watch's zone, with the date
 * when it is a week or more away. When the viewer's clock (viewer, a zone)
 * reads another time, the zone's short name follows, so the time is not
 * taken for local.
 */
export function whenText(iso, zone, now = new Date(), locale, viewer = viewerZone()) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const far = Math.abs(d.getTime() - now.getTime()) >= 6 * 24 * 3600 * 1000
  // h23, not hour12: false, which some engines write as 24:05 after midnight.
  const opts = { weekday: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  if (far) Object.assign(opts, { day: 'numeric', month: 'short' })
  try {
    const text = new Intl.DateTimeFormat(locale, { ...opts, timeZone: zone || undefined }).format(d)
    if (!zone || !viewer || new Intl.DateTimeFormat(locale, { ...opts, timeZone: viewer }).format(d) === text) return text
    return new Intl.DateTimeFormat(locale, { ...opts, timeZone: zone, timeZoneName: 'short' }).format(d)
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
      timeZone: zone || undefined, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
    }).formatToParts(d).map((p) => [p.type, p.value]))
    return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`
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
