// Pure helpers of the Calendar page: dates are plain YYYY-MM-DD strings, so no
// browser zone changes a day. The server owns zones and repeats.

const pad = (n) => String(n).padStart(2, '0')
export const dateOf = (y, m, d) => `${y}-${pad(m)}-${pad(d)}`

export function parseDate(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(s || '')
  return m ? { y: +m[1], m: +m[2], d: +m[3] } : null
}

// addDays moves a date by n days.
export function addDays(s, n) {
  const p = parseDate(s)
  const t = new Date(Date.UTC(p.y, p.m - 1, p.d + n))
  return dateOf(t.getUTCFullYear(), t.getUTCMonth() + 1, t.getUTCDate())
}

// weekday is 0 for Sunday.
export function weekday(s) {
  const p = parseDate(s)
  return new Date(Date.UTC(p.y, p.m - 1, p.d)).getUTCDay()
}

export function addMonths(s, n) {
  const p = parseDate(s)
  const t = new Date(Date.UTC(p.y, p.m - 1 + n, 1))
  return dateOf(t.getUTCFullYear(), t.getUTCMonth() + 1, 1)
}

// monthGrid returns the weeks (arrays of seven dates) that show the month of
// `cursor`, starting on `weekStart` (1 = Monday).
export function monthGrid(cursor, weekStart = 1) {
  const first = dateOf(parseDate(cursor).y, parseDate(cursor).m, 1)
  const lead = (weekday(first) - weekStart + 7) % 7
  let day = addDays(first, -lead)
  const weeks = []
  do {
    const week = []
    for (let i = 0; i < 7; i++) { week.push(day); day = addDays(day, 1) }
    weeks.push(week)
  } while (parseDate(day).m === parseDate(first).m)
  return weeks
}

// gridRange is the first and last date a month grid shows.
export function gridRange(cursor, weekStart = 1) {
  const weeks = monthGrid(cursor, weekStart)
  return { from: weeks[0][0], to: weeks[weeks.length - 1][6] }
}

const datePart = (s) => (s || '').slice(0, 10)
export const timePart = (s) => ((s || '').length > 10 ? s.slice(11, 16) : '')

// byDay puts each occurrence on every day it covers, within [from, to].
export function byDay(occurrences, from, to) {
  const out = {}
  for (const o of occurrences || []) {
    const start = datePart(o.start)
    let end = datePart(o.end)
    // A timed event that ends at midnight sharp does not touch the next day.
    if (!o.allDay && timePart(o.end) === '00:00' && end > start) end = addDays(end, -1)
    for (let d = start < from ? from : start; d <= end && d <= to; d = addDays(d, 1)) (out[d] ||= []).push(o)
  }
  return out
}

// agenda groups occurrences by their start day, in order.
export function agenda(occurrences) {
  const days = []
  for (const o of occurrences || []) {
    const d = datePart(o.start)
    const last = days[days.length - 1]
    if (last && last.date === d) last.items.push(o)
    else days.push({ date: d, items: [o] })
  }
  return days
}

export const emptyForm = (date, zone) => ({
  id: '', title: '', allDay: false, startDate: date, startTime: '09:00', endDate: date, endTime: '10:00',
  zone: zone || '', place: '', attendees: '', notes: '', freq: '', interval: '1', until: '',
})

// formOf fills the form from an event as the server keeps it.
export function formOf(e) {
  return {
    id: e.id, title: e.title, allDay: !!e.allDay,
    startDate: datePart(e.start), startTime: timePart(e.start) || '09:00', endDate: datePart(e.end), endTime: timePart(e.end) || '10:00',
    zone: e.zone || '', place: e.place || '', attendees: e.attendees || '', notes: e.notes || '',
    freq: e.repeat ? e.repeat.freq : '', interval: String(e.repeat?.interval || 1), until: e.repeat?.until || '',
  }
}

// argsOf turns the form into the arguments of ui-event-create or
// ui-event-update. Every field is sent, so a cleared field clears the event.
export function argsOf(f) {
  const start = f.allDay ? f.startDate : `${f.startDate}T${f.startTime}`
  const end = f.allDay ? (f.endDate || f.startDate) : `${f.endDate || f.startDate}T${f.endTime}`
  const args = {
    title: f.title.trim(), start, end, allDay: !!f.allDay, notes: f.notes, place: f.place, attendees: f.attendees,
    repeat: f.freq ? { freq: f.freq, interval: Math.max(1, parseInt(f.interval, 10) || 1), ...(f.until ? { until: f.until } : {}) } : { freq: 'none' },
  }
  if (f.zone) args.zone = f.zone
  if (f.id) args.id = f.id
  return args
}

// problem is the key of the first thing wrong with the form, or ''.
export function problem(f) {
  if (!f.title.trim()) return 'calendar.form.needTitle'
  if (!parseDate(f.startDate)) return 'calendar.form.needStart'
  const s = `${f.startDate}T${f.allDay ? '00:00' : f.startTime}`
  const e = `${f.endDate || f.startDate}T${f.allDay ? '00:00' : f.endTime}`
  if (e < s) return 'calendar.form.endBeforeStart'
  if (f.freq && f.until && f.until < f.startDate) return 'calendar.form.untilBeforeStart'
  return ''
}

// downloadIcs offers the file to the person through a link.
export function downloadIcs(doc, text, name) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/calendar;charset=utf-8' }))
  const a = doc.createElement('a')
  a.href = url
  a.download = name
  doc.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
