// The link between a Home widget and the trigger that rewrites its data file,
// and the state its footer shows. Pure functions: no network, no DOM.

const RUNNING = new Set(['running', 'queued'])

// linkedTrigger finds the trigger of a widget: the id home.json names, else the
// trigger whose plan names the widget. Without a trigger, a proposal whose plan
// names the widget means the user has not confirmed it yet.
export function linkedTrigger(w, triggers = [], proposals = []) {
  const byId = w?.trigger ? triggers.find(t => t.id === w.trigger) : null
  const trigger = byId || triggers.find(t => t.plan?.widget && t.plan.widget === w?.id) || null
  const proposal = trigger ? null : (proposals.find(p => p.plan?.widget && p.plan.widget === w?.id) || null)
  return { trigger, proposal }
}

// cadenceOf is the tag in the widget's header: paused, hourly, daily,
// weekdays, weekly, monthly, file, or '' for a schedule with no short name.
export function cadenceOf(t) {
  if (!t) return ''
  if (!t.enabled) return 'paused'
  if (t.kind === 'file') return 'file'
  const w = String(t.schedule || '').trim().split(/\s+/)
  if (['daily', 'weekdays', 'weekly', 'monthly'].includes(w[0])) return w[0]
  if (w.length === 5 && /^\d{1,2}$/.test(w[0]) && w[1] === '*' && w[2] === '*' && w[3] === '*' && w[4] === '*') return 'hourly'
  return ''
}

// footerState says which line the footer of a data widget shows:
//   invalid      the file breaks its schema; the last good version shows
//   missing      the file does not exist (and no run failed)
//   waiting      a proposal names the widget; the user has not confirmed it
//   running      a run is queued or running
//   paused       the trigger is off
//   failedEmpty  the last run failed and there is no data
//   stale        the last run failed; the old data shows, dimmed
//   firstRun     the trigger is on and has never run
//   fresh        the last run ended (or was skipped) and the data is good
//   page, file   no trigger: where the data comes from
export function footerState({ data, trigger = null, proposal = null }) {
  const st = data?.state
  if (st === 'stale' || (st === 'error' && (data.items?.length || data.value !== null))) return 'invalid'
  if (!trigger) {
    if (proposal) return 'waiting'
    if (st === 'empty' || st === 'error') return 'missing'
    return data?.source?.kind === 'page' ? 'page' : 'file'
  }
  const last = trigger.last_run
  if (last && RUNNING.has(last.status)) return 'running'
  if (!trigger.enabled) return 'paused'
  if (last?.status === 'failed') return st === 'ok' ? 'stale' : 'failedEmpty'
  if (!last) return 'firstRun'
  if (st === 'empty') return 'missing'
  return 'fresh'
}

// scheduledCount is how many widgets a trigger that is on keeps up to date.
export function scheduledCount(widgets, triggers) {
  return widgets.filter(w => linkedTrigger(w, triggers).trigger?.enabled).length
}

// plainSummary is the server's summary of a trigger without the time zone the
// server appends in brackets: the places that show it name the zone apart.
export function plainSummary(t) {
  const s = String(t?.summary || t?.schedule || '')
  const zone = t?.time_zone
  return zone && s.endsWith(` (${zone})`) ? s.slice(0, -(zone.length + 3)) : s
}
