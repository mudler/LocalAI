// The entry of the Calendar add-on. The page shows the owner's calendar as a
// month or as an agenda, lets the owner add, change and delete events, sets the
// default zone and exports one ICS file. Every call goes to the add-on through
// the typed read of the host, as the signed-in person; the add-on keeps the
// calendar of that person and of nobody else.
import { locales } from './i18n/index.js'
import { addDays, addMonths, agenda, argsOf, byDay, downloadIcs, emptyForm, formOf, gridRange, monthGrid, parseDate, problem, timePart } from './lib/calendar.js'

const WEEK_START = 1
const AGENDA_DAYS = 60
const REPEATS = ['daily', 'weekly', 'monthly', 'yearly']

export function createAddon(sdk) {
  const { React } = sdk
  const h = React.createElement
  for (const [language, bundle] of Object.entries(locales)) sdk.locale.define(language, bundle)
  const t = (key, vars) => {
    let s = sdk.locale.t(key, key)
    for (const [k, v] of Object.entries(vars || {})) s = s.split(`{{${k}}}`).join(String(v))
    return s
  }
  const call = (tool, args) => sdk.api.read({ arguments: { tool, ...(args || {}) } })
  const lang = () => { try { return sdk.locale.language() || 'en' } catch { return 'en' } }
  const fmt = (opts, date) => {
    const p = parseDate(date)
    return new Intl.DateTimeFormat(lang(), { timeZone: 'UTC', ...opts }).format(new Date(Date.UTC(p.y, p.m - 1, p.d)))
  }
  const dayLabel = (date) => fmt({ weekday: 'long', day: 'numeric', month: 'long' }, date)
  const monthLabel = (date) => fmt({ month: 'long', year: 'numeric' }, date)
  const shortWeekday = (date) => fmt({ weekday: 'short' }, date)
  const when = (o) => (o.allDay ? t('calendar.allDay') : `${timePart(o.start)}${timePart(o.end) ? `–${timePart(o.end)}` : ''}`)
  const refused = (out) => (out && out.ok === false ? out.error || t('calendar.failed') : '')

  function Field({ label, children, id }) {
    return h('label', { className: 'cal-field', htmlFor: id }, h('span', { className: 'cal-field__label' }, label), children)
  }

  function EventForm({ initial, zone, onSaved, onCancel }) {
    const { useState } = React
    const [f, setF] = useState(initial)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const set = (k) => (e) => setF({ ...f, [k]: e.target.type === 'checkbox' ? e.target.checked : e.target.value })
    const bad = problem(f)
    const save = async (e) => {
      e.preventDefault()
      if (bad) { setError(t(bad)); return }
      setBusy(true)
      setError('')
      try {
        const out = await call(f.id ? 'ui-event-update' : 'ui-event-create', argsOf(f))
        const why = refused(out)
        if (why) setError(why)
        else onSaved()
      } catch { setError(t('calendar.failed')) } finally { setBusy(false) }
    }
    const input = (k, type, extra) => h('input', { id: `cal-${k}`, type, value: f[k], onChange: set(k), disabled: busy, 'data-testid': `calendar-${k}`, ...extra })
    return h('form', { className: 'cal-form', onSubmit: save, 'data-testid': 'calendar-form', 'aria-label': f.id ? t('calendar.form.editTitle') : t('calendar.form.newTitle') },
      h('h2', { className: 'cal-form__title' }, f.id ? t('calendar.form.editTitle') : t('calendar.form.newTitle')),
      h(Field, { label: t('calendar.form.title'), id: 'cal-title' }, input('title', 'text', { maxLength: 200, required: true })),
      h('label', { className: 'cal-check' }, h('input', { type: 'checkbox', checked: f.allDay, onChange: set('allDay'), disabled: busy, 'data-testid': 'calendar-allDay' }), t('calendar.form.allDay')),
      h('div', { className: 'cal-row' },
        h(Field, { label: t('calendar.form.startDate'), id: 'cal-startDate' }, input('startDate', 'date')),
        f.allDay ? null : h(Field, { label: t('calendar.form.startTime'), id: 'cal-startTime' }, input('startTime', 'time'))),
      h('div', { className: 'cal-row' },
        h(Field, { label: t('calendar.form.endDate'), id: 'cal-endDate' }, input('endDate', 'date')),
        f.allDay ? null : h(Field, { label: t('calendar.form.endTime'), id: 'cal-endTime' }, input('endTime', 'time'))),
      f.allDay ? null : h(Field, { label: t('calendar.form.zone'), id: 'cal-zone' }, input('zone', 'text', { list: 'cal-zones', placeholder: zone })),
      h(Field, { label: t('calendar.form.place'), id: 'cal-place' }, input('place', 'text', { maxLength: 200 })),
      h(Field, { label: t('calendar.form.attendees'), id: 'cal-attendees' }, input('attendees', 'text', { maxLength: 500 })),
      h(Field, { label: t('calendar.form.notes'), id: 'cal-notes' }, h('textarea', { id: 'cal-notes', value: f.notes, onChange: set('notes'), rows: 3, maxLength: 4000, disabled: busy, 'data-testid': 'calendar-notes' })),
      h('div', { className: 'cal-row' },
        h(Field, { label: t('calendar.form.repeat'), id: 'cal-freq' },
          h('select', { id: 'cal-freq', value: f.freq, onChange: set('freq'), disabled: busy, 'data-testid': 'calendar-freq' },
            h('option', { value: '' }, t('calendar.repeat.none')),
            REPEATS.map((r) => h('option', { key: r, value: r }, t(`calendar.repeat.${r}`))))),
        f.freq ? h(Field, { label: t('calendar.form.interval'), id: 'cal-interval' }, input('interval', 'number', { min: 1, max: 366 })) : null,
        f.freq ? h(Field, { label: t('calendar.form.until'), id: 'cal-until' }, input('until', 'date')) : null),
      f.id && f.freq ? h('p', { className: 'cal-hint' }, t('calendar.form.seriesHint')) : null,
      error ? h('p', { className: 'cal-error', role: 'alert', 'data-testid': 'calendar-error' }, error) : null,
      h('div', { className: 'cal-actions' },
        h('button', { type: 'submit', className: 'btn btn-primary', disabled: busy, 'data-testid': 'calendar-save' }, t('calendar.form.save')),
        h('button', { type: 'button', className: 'btn btn-secondary', onClick: onCancel, disabled: busy, 'data-testid': 'calendar-cancel' }, t('calendar.form.cancel'))))
  }

  function EventLine({ o, onOpen }) {
    return h('li', { className: 'cal-line', 'data-testid': 'calendar-event' },
      h('button', { type: 'button', className: 'cal-line__btn', onClick: () => onOpen(o) },
        h('span', { className: 'cal-line__when' }, when(o)),
        h('span', { className: 'cal-line__title' }, o.title),
        o.place ? h('span', { className: 'cal-line__place' }, o.place) : null,
        o.repeat ? h('span', { className: 'cal-line__repeat', title: t('calendar.repeats') }, '↻') : null))
  }

  function Detail({ o, onEdit, onDeleted, onClose }) {
    const { useState } = React
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const [confirm, setConfirm] = useState('')
    const run = async (args) => {
      setBusy(true)
      setError('')
      try {
        const why = refused(await call('ui-event-delete', args))
        if (why) setError(why)
        else onDeleted()
      } catch { setError(t('calendar.failed')) } finally { setBusy(false) }
    }
    return h('section', { className: 'cal-detail', 'data-testid': 'calendar-detail' },
      h('h2', null, o.title),
      h('p', null, `${dayLabel(o.start.slice(0, 10))}, ${when(o)}`),
      o.place ? h('p', null, `${t('calendar.form.place')}: ${o.place}`) : null,
      o.attendees ? h('p', null, `${t('calendar.form.attendees')}: ${o.attendees}`) : null,
      o.notes ? h('p', { className: 'cal-notes' }, o.notes) : null,
      o.repeat ? h('p', null, t(`calendar.repeat.${o.repeat.freq}`)) : null,
      error ? h('p', { className: 'cal-error', role: 'alert' }, error) : null,
      confirm ? h('div', { className: 'cal-actions', role: 'group', 'aria-label': t('calendar.confirm') },
        h('span', null, t('calendar.confirm')),
        h('button', { type: 'button', className: 'btn btn-danger', disabled: busy, 'data-testid': 'calendar-confirm', onClick: () => run(confirm === 'day' ? { id: o.id, occurrence: o.occurrence } : { id: o.id }) }, t('calendar.yes')),
        h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setConfirm('') }, t('calendar.no')))
        : h('div', { className: 'cal-actions' },
          h('button', { type: 'button', className: 'btn btn-primary', onClick: () => onEdit(o), 'data-testid': 'calendar-edit' }, o.repeat ? t('calendar.editSeries') : t('calendar.edit')),
          o.repeat ? h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setConfirm('day'), 'data-testid': 'calendar-skip' }, t('calendar.skipDay')) : null,
          h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setConfirm('all'), 'data-testid': 'calendar-delete' }, o.repeat ? t('calendar.deleteSeries') : t('calendar.delete')),
          h('button', { type: 'button', className: 'btn btn-secondary', onClick: onClose }, t('calendar.close'))))
  }

  function Settings({ zone, onChanged, zones }) {
    const { useState } = React
    const [value, setValue] = useState(zone)
    const [note, setNote] = useState('')
    const [busy, setBusy] = useState(false)
    const save = async () => {
      setBusy(true)
      setNote('')
      try {
        const why = refused(await call('settings-set', { zone: value.trim() }))
        if (why) setNote(why)
        else { setNote(t('calendar.settings.saved')); onChanged(value.trim()) }
      } catch { setNote(t('calendar.failed')) } finally { setBusy(false) }
    }
    const exportIcs = async () => {
      setBusy(true)
      setNote('')
      try {
        const out = await call('events-export')
        const why = refused(out)
        if (why) setNote(why)
        else downloadIcs(document, out.ics, out.filename || 'calendar.ics')
      } catch { setNote(t('calendar.failed')) } finally { setBusy(false) }
    }
    return h('section', { className: 'cal-settings', 'data-testid': 'calendar-settings' },
      h('h2', null, t('calendar.settings.title')),
      h('p', { className: 'cal-hint' }, t('calendar.settings.zoneHelp')),
      h('div', { className: 'cal-row' },
        h(Field, { label: t('calendar.settings.zone'), id: 'cal-default-zone' },
          h('input', { id: 'cal-default-zone', type: 'text', list: 'cal-zones', value, onChange: (e) => setValue(e.target.value), disabled: busy, 'data-testid': 'calendar-default-zone' })),
        h('button', { type: 'button', className: 'btn btn-primary', onClick: save, disabled: busy || !value.trim(), 'data-testid': 'calendar-zone-save' }, t('calendar.settings.save'))),
      h('div', { className: 'cal-actions' },
        h('button', { type: 'button', className: 'btn btn-secondary', onClick: exportIcs, disabled: busy, 'data-testid': 'calendar-export' }, t('calendar.export'))),
      note ? h('p', { role: 'status', className: 'cal-hint', 'data-testid': 'calendar-note' }, note) : null,
      h('datalist', { id: 'cal-zones' }, zones.map((z) => h('option', { key: z, value: z }))))
  }

  function Month({ cursor, today, occurrences, onPick, onDay }) {
    const weeks = monthGrid(cursor, WEEK_START)
    const { from, to } = gridRange(cursor, WEEK_START)
    const days = byDay(occurrences, from, to)
    const inMonth = parseDate(cursor).m
    return h('div', { className: 'cal-month', role: 'grid', 'aria-label': monthLabel(cursor), 'data-testid': 'calendar-month' },
      h('div', { className: 'cal-month__head', role: 'row' }, weeks[0].map((d) => h('span', { key: d, role: 'columnheader' }, shortWeekday(d)))),
      weeks.map((week) => h('div', { key: week[0], className: 'cal-month__week', role: 'row' },
        week.map((d) => h('div', { key: d, role: 'gridcell', className: `cal-day${parseDate(d).m !== inMonth ? ' is-outside' : ''}${d === today ? ' is-today' : ''}`, 'data-date': d },
          h('button', { type: 'button', className: 'cal-day__num', onClick: () => onDay(d), 'aria-label': dayLabel(d) }, parseDate(d).d),
          h('ul', { className: 'cal-day__events' }, (days[d] || []).slice(0, 3).map((o, i) => h(EventLine, { key: `${o.id}${o.occurrence}${i}`, o, onOpen: onPick }))),
          (days[d] || []).length > 3 ? h('button', { type: 'button', className: 'cal-day__more', onClick: () => onDay(d) }, t('calendar.more', { count: days[d].length - 3 })) : null)))))
  }

  function Agenda({ occurrences, onPick }) {
    const groups = agenda(occurrences)
    if (groups.length === 0) return h('p', { className: 'cal-empty', 'data-testid': 'calendar-empty' }, t('calendar.agenda.empty', { days: AGENDA_DAYS }))
    return h('div', { className: 'cal-agenda', 'data-testid': 'calendar-agenda' }, groups.map((g) => h('section', { key: g.date, className: 'cal-agenda__day' },
      h('h3', null, dayLabel(g.date)),
      h('ul', null, g.items.map((o, i) => h(EventLine, { key: `${o.id}${o.occurrence}${i}`, o, onOpen: onPick }))))))
  }

  function Component({ page }) {
    if (page !== 'calendar') return null
    return h(Page, null)
  }

  function Page() {
    const { useState, useEffect, useCallback } = React
    const [view, setView] = useState('month')
    const [today, setToday] = useState('')
    const [zone, setZone] = useState('')
    const [cursor, setCursor] = useState('')
    const [occ, setOcc] = useState([])
    const [more, setMore] = useState(false)
    const [phase, setPhase] = useState('loading')
    const [form, setForm] = useState(null)
    const [picked, setPicked] = useState(null)
    const [zones] = useState(() => { try { return Intl.supportedValuesOf('timeZone') } catch { return [] } })

    // The first call learns today's date and the zone of the owner.
    useEffect(() => {
      let live = true
      call('settings-get').then((out) => {
        if (!live) return
        if (refused(out) || !out.today) { setPhase('error'); return }
        setZone(out.zone)
        setToday(out.today)
        setCursor(out.today.slice(0, 8) + '01')
      }).catch(() => live && setPhase('error'))
      return () => { live = false }
    }, [])

    const range = useCallback(() => (view === 'month' ? gridRange(cursor, WEEK_START) : { from: today, to: addDays(today, AGENDA_DAYS - 1) }), [view, cursor, today])
    const load = useCallback(async () => {
      if (!today) return
      const r = range()
      try {
        const out = await call('events-list', { from: r.from, to: r.to })
        if (refused(out)) { setPhase('error'); return }
        setOcc(out.events || [])
        setMore(!!out.more)
        setPhase('ready')
      } catch { setPhase('error') }
    }, [today, range])
    useEffect(() => { load() }, [load])

    const reload = () => { setForm(null); setPicked(null); load() }
    const edit = async (o) => {
      try {
        const out = await call('event-get', { id: o.id })
        if (refused(out)) { setPhase('error'); return }
        setPicked(null)
        setForm(formOf(out.event))
      } catch { setPhase('error') }
    }
    const create = (date) => setForm(emptyForm(date || today, ''))

    if (phase === 'loading') return h('p', { 'aria-busy': 'true', 'data-testid': 'calendar-loading' }, t('calendar.loading'))
    if (phase === 'error') return h('div', { className: 'cal-page' }, h('p', { className: 'cal-error', role: 'alert', 'data-testid': 'calendar-unreadable' }, t('calendar.unreadable')))

    return h('section', { className: 'cal-page', 'data-testid': 'calendar-page' },
      h('header', { className: 'cal-bar' },
        h('h1', { className: 'page-title cal-bar__title' }, view === 'month' ? monthLabel(cursor) : t('calendar.agenda.title')),
        h('div', { className: 'cal-bar__tools' },
          view === 'month' ? h('div', { role: 'group', 'aria-label': t('calendar.navigate') },
            h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setCursor(addMonths(cursor, -1)), 'aria-label': t('calendar.prev'), 'data-testid': 'calendar-prev' }, '‹'),
            h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setCursor(today.slice(0, 8) + '01'), 'data-testid': 'calendar-today' }, t('calendar.today')),
            h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setCursor(addMonths(cursor, 1)), 'aria-label': t('calendar.next'), 'data-testid': 'calendar-next' }, '›')) : null,
          h('div', { role: 'group', 'aria-label': t('calendar.view') },
            h('button', { type: 'button', className: 'btn btn-secondary', 'aria-pressed': view === 'month', onClick: () => setView('month'), 'data-testid': 'calendar-view-month' }, t('calendar.view.month')),
            h('button', { type: 'button', className: 'btn btn-secondary', 'aria-pressed': view === 'agenda', onClick: () => setView('agenda'), 'data-testid': 'calendar-view-agenda' }, t('calendar.view.agenda'))),
          h('button', { type: 'button', className: 'btn btn-primary', onClick: () => create(), 'data-testid': 'calendar-new' }, t('calendar.new')))),
      more ? h('p', { className: 'cal-hint', role: 'status' }, t('calendar.truncated')) : null,
      form ? h(EventForm, { key: form.id || 'new', initial: form, zone, onSaved: reload, onCancel: () => setForm(null) }) : null,
      picked ? h(Detail, { key: picked.id + picked.occurrence, o: picked, onEdit: edit, onDeleted: reload, onClose: () => setPicked(null) }) : null,
      view === 'month'
        ? h(Month, { cursor, today, occurrences: occ, onPick: setPicked, onDay: create })
        : h(Agenda, { occurrences: occ, onPick: setPicked }),
      h(Settings, { zone, zones, onChanged: setZone }))
  }

  // The views are exposed so a test can draw each one with its own data.
  return { Component, components: { CalendarMonth: Month, CalendarAgenda: Agenda, CalendarForm: EventForm, CalendarDetail: Detail }, chatActions: [], dispose() {} }
}
