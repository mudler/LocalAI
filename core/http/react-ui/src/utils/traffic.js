// What the Traffic pages say, worked out from what the API returns.
// Pure functions, so the rules can be read and tested without a page.
//
// Nothing here invents a figure. The usage ledger knows requests and tokens by
// model, user and key. The trace buffer knows how many requests failed and how
// long the slowest 5% took (p95). LocalAI has no p50 or p99, no status or node
// on a usage row, and no price. A function that would need one of those returns
// null, and the page says the figure does not exist instead of drawing zero.

// ---------------------------------------------------------------------------
// The time window the pages share
// ---------------------------------------------------------------------------

// `period` is what the usage API takes (day, week, month, all). `hours` is what
// the trace summary takes, which stops at 168. A window longer than that reads
// the trace figures over the last 7 days and says so.
export const WINDOWS = [
  { id: '24h', period: 'day', hours: 24, capped: false },
  { id: '7d', period: 'week', hours: 168, capped: false },
  { id: '30d', period: 'month', hours: 168, capped: true },
  { id: 'all', period: 'all', hours: 168, capped: true },
]

export const DEFAULT_WINDOW = '24h'

export function windowById(id) {
  return WINDOWS.find(w => w.id === id) || WINDOWS[0]
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

export function compactCount(n) {
  const value = Number(n)
  if (!Number.isFinite(value)) return '0'
  const abs = Math.abs(value)
  if (abs >= 1_000_000) return `${trimZero((value / 1_000_000).toFixed(1))}M`
  if (abs >= 10_000) return `${Math.round(value / 1000)}k`
  if (abs >= 1_000) return `${trimZero((value / 1000).toFixed(1))}k`
  return String(Math.round(value))
}

function trimZero(text) {
  return text.endsWith('.0') ? text.slice(0, -2) : text
}

export function percentText(part, whole) {
  if (!(whole > 0)) return null
  const pct = (part / whole) * 100
  if (pct === 0) return '0%'
  if (pct < 0.1) return '<0.1%'
  return `${pct < 10 ? pct.toFixed(1) : Math.round(pct)}%`
}

// Milliseconds as the shortest honest string.
export function durationText(ms) {
  const value = Number(ms)
  if (!Number.isFinite(value) || value < 0) return '-'
  if (value < 1) return '<1 ms'
  if (value < 1000) return `${Math.round(value)} ms`
  if (value < 60_000) return `${(value / 1000).toFixed(value < 10_000 ? 1 : 0)} s`
  return `${Math.round(value / 60_000)} min`
}

// The trace buffer stores durations in nanoseconds (Go's time.Duration).
export function nsToMs(ns) {
  const value = Number(ns)
  return Number.isFinite(value) ? value / 1_000_000 : null
}

export function bytesText(n) {
  const value = Number(n)
  if (!Number.isFinite(value) || value < 0) return '-'
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

// A bucket from the usage ledger ("2026-10-07 14:00", "2026-10-07",
// "2026-10") as a short axis label. Parsed by hand: `new Date('2026-10-07')`
// is UTC and would shift the day for anyone west of Greenwich.
export function bucketLabel(bucket, period, locale) {
  if (!bucket) return ''
  const text = String(bucket)
  if (period === 'day') return text.split(' ')[1] || text
  const [y, m, d] = text.split('-').map(Number)
  if (!y || !m) return text
  const date = new Date(y, m - 1, d || 1)
  if (Number.isNaN(date.getTime())) return text
  if (period === 'all') return date.toLocaleDateString(locale, { month: 'short', year: 'numeric' })
  return date.toLocaleDateString(locale, { month: 'short', day: 'numeric' })
}

// ---------------------------------------------------------------------------
// Usage rows
// ---------------------------------------------------------------------------

// The identity a bucket belongs to for a grouping. A key bucket is the key, or
// the source class (web, legacy) when no key made the call.
export function groupId(b, by) {
  if (by === 'user') return b.user_id || '(unknown)'
  if (by === 'key') return b.api_key_id || b.source || 'unknown'
  return b.model || '(unknown)'
}

const ZERO = () => ({ requests: 0, prompt: 0, completion: 0, total: 0 })

function add(into, bucket) {
  into.requests += Number(bucket.request_count) || 0
  into.prompt += Number(bucket.prompt_tokens) || 0
  into.completion += Number(bucket.completion_tokens) || 0
  into.total += Number(bucket.total_tokens) || 0
}

export function totalsOf(buckets) {
  const out = ZERO()
  for (const b of buckets || []) add(out, b)
  return out
}

// Rows of the usage table for one grouping. `model` and `user` come from the
// ledger's own rows. `key` comes from the by-source endpoint, which has its own
// shape, so it goes through keyRows below.
export function groupUsage(buckets, by) {
  const map = new Map()
  for (const b of buckets || []) {
    let id
    let name
    let sub = ''
    if (by === 'user') {
      id = b.user_id || '(unknown)'
      name = b.user_name || id
    } else if (by === 'key') {
      id = groupId(b, 'key')
      name = b.api_key_name || (b.source === 'web' || b.source === 'legacy' ? b.source : '') || id
    } else {
      id = b.model || '(unknown)'
      name = id
    }
    if (!map.has(id)) map.set(id, { id, name, sub, ...ZERO() })
    add(map.get(id), b)
  }
  return [...map.values()]
}

// Rows by API key, from the by-source totals. A key carries tokens and
// requests but not a prompt/completion split, so those stay null and the table
// shows a dash rather than a made-up zero.
export function keyRows(totals, { showUser = false } = {}) {
  const named = (totals?.by_key || []).map(k => ({
    id: k.api_key_id,
    name: k.api_key_name || k.api_key_id,
    sub: showUser ? (k.user_name || k.user_id || '') : '',
    kind: 'apikey',
    requests: Number(k.requests) || 0,
    prompt: null,
    completion: null,
    total: Number(k.tokens) || 0,
    lastUsed: k.last_used || null,
  }))
  let unkeyed = []
  if (showUser && Array.isArray(totals?.by_user_source) && totals.by_user_source.length > 0) {
    unkeyed = totals.by_user_source.map(r => ({
      id: `${r.source}:${r.user_id || ''}`,
      name: r.source === 'legacy' ? 'legacy' : 'web',
      sub: r.user_name || r.user_id || '',
      kind: r.source,
      requests: Number(r.requests) || 0,
      prompt: null,
      completion: null,
      total: Number(r.tokens) || 0,
      lastUsed: null,
    }))
  } else {
    for (const cls of ['web', 'legacy']) {
      const entry = totals?.by_source?.[cls]
      if (!entry) continue
      unkeyed.push({
        id: cls, name: cls, sub: '', kind: cls,
        requests: Number(entry.requests) || 0, prompt: null, completion: null,
        total: Number(entry.tokens) || 0, lastUsed: null,
      })
    }
  }
  return [...named, ...unkeyed]
}

const SORTERS = {
  name: (a, b) => String(a.name).localeCompare(String(b.name)),
  requests: (a, b) => a.requests - b.requests,
  prompt: (a, b) => (a.prompt ?? -1) - (b.prompt ?? -1),
  completion: (a, b) => (a.completion ?? -1) - (b.completion ?? -1),
  total: (a, b) => a.total - b.total,
  lastUsed: (a, b) => Date.parse(a.lastUsed || 0) - Date.parse(b.lastUsed || 0),
}

export function sortRows(rows, { key = 'total', direction = 'desc' } = {}) {
  const cmp = SORTERS[key] || SORTERS.total
  const factor = direction === 'asc' ? 1 : -1
  return [...rows].sort((a, b) => factor * cmp(a, b) || String(a.name).localeCompare(String(b.name)))
}

export function filterRows(rows, query) {
  const needle = String(query || '').trim().toLowerCase()
  if (!needle) return rows
  return rows.filter(r => String(r.name).toLowerCase().includes(needle) || String(r.sub || '').toLowerCase().includes(needle))
}

// ---------------------------------------------------------------------------
// Series for the charts
// ---------------------------------------------------------------------------

// One point per ledger bucket, oldest first.
export function seriesByBucket(buckets) {
  const map = new Map()
  for (const b of buckets || []) {
    if (!b.bucket) continue
    if (!map.has(b.bucket)) map.set(b.bucket, { bucket: b.bucket, ...ZERO() })
    add(map.get(b.bucket), b)
  }
  return [...map.values()].sort((a, b) => a.bucket.localeCompare(b.bucket))
}

// Tokens per bucket, split into the top groups and "Other". Each group keeps
// the colour of its rank in the whole window, not in the bucket, so a model
// does not change colour from one column to the next.
export function stackedByGroup(buckets, by, top = 4) {
  const totals = groupUsage(buckets, by).sort((a, b) => b.total - a.total || a.name.localeCompare(b.name))
  const keep = totals.slice(0, top)
  const keepIds = new Set(keep.map(g => g.id))
  const hasOther = totals.length > top
  const groups = keep.map((g, i) => ({ id: g.id, name: g.name, series: i + 1 }))
  if (hasOther) groups.push({ id: '__other__', name: 'Other', series: 'other' })
  const points = new Map()
  for (const b of buckets || []) {
    if (!b.bucket) continue
    const id = groupId(b, by)
    const gid = keepIds.has(id) ? id : '__other__'
    if (!points.has(b.bucket)) points.set(b.bucket, { bucket: b.bucket, values: {} })
    const p = points.get(b.bucket)
    p.values[gid] = (p.values[gid] || 0) + (Number(b.total_tokens) || 0)
  }
  return {
    groups,
    points: [...points.values()].sort((a, b) => a.bucket.localeCompare(b.bucket)),
  }
}

// The trace summary's twelve columns as chart points: succeeded and failed
// stacked, oldest first. `start` is an ISO time from the server.
export function traceColumns(summary) {
  const buckets = Array.isArray(summary?.buckets) ? summary.buckets : []
  return buckets.map(b => ({
    start: b.start,
    ok: Math.max(0, (Number(b.count) || 0) - (Number(b.errors) || 0)),
    failed: Number(b.errors) || 0,
  }))
}

// ---------------------------------------------------------------------------
// Chart geometry
// ---------------------------------------------------------------------------

// A "nice" top for a bar axis that starts at zero: a round multiple of a power
// of ten. The axis never cuts the tallest bar and never starts above zero.
export function niceMax(value) {
  if (!(value > 0)) return 1
  const exp = Math.floor(Math.log10(value))
  const base = 10 ** exp
  const frac = value / base
  const step = [1, 1.5, 2, 3, 4, 5, 6, 8, 10].find(v => frac <= v + 1e-9)
  return step * base
}

// Geometry for stacked bars: each column is a list of segments, bottom to top.
//   columns  [{ key, label, segments: [{ id, value }] }]
export function barGeometry(columns, { width = 640, height = 200, left = 46, right = 12, top = 12, bottom = 26 } = {}) {
  const plotW = Math.max(40, width - left - right)
  const plotH = height - top - bottom
  const n = Math.max(1, columns.length)
  const peak = Math.max(0, ...columns.map(c => c.segments.reduce((s, seg) => s + seg.value, 0)))
  const max = niceMax(peak)
  const slot = plotW / n
  const barW = Math.max(2, Math.min(40, slot - Math.min(6, slot * 0.25)))
  const y = v => top + plotH - (v / max) * plotH
  const cols = columns.map((c, i) => {
    const x = left + slot * i + (slot - barW) / 2
    let acc = 0
    const segs = c.segments
      .filter(seg => seg.value > 0)
      .map(seg => {
        const y1 = y(acc + seg.value)
        const y0 = y(acc)
        acc += seg.value
        return { id: seg.id, x, y: y1, w: barW, h: Math.max(1, y0 - y1) }
      })
    return { key: c.key, label: c.label, x, cx: x + barW / 2, total: acc, segs }
  })
  const ticks = [0, 0.5, 1].map(f => ({ value: max * f, y: y(max * f) }))
  // Fewer x labels when columns are narrow, so none touch.
  const every = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(plotW / 64))))
  return { width, height, left, right, top, plotW, plotH, baseline: top + plotH, max, cols, ticks, every }
}

// ---------------------------------------------------------------------------
// The figures at the top of the overview
// ---------------------------------------------------------------------------

// `ledger` is the usage totals, `summary` the trace summary or null when the
// buffer cannot be read. A figure is null when its source is missing, and the
// page then says where the figure would come from.
export function overviewFigures({ ledger, summary, tracing }) {
  const failedKnown = summary != null && tracing !== false
  const total = summary?.total ?? 0
  return {
    requests: ledger ? ledger.requests : null,
    tokensIn: ledger ? ledger.prompt : null,
    tokensOut: ledger ? ledger.completion : null,
    failed: failedKnown ? (summary.errors ?? 0) : null,
    failedShare: failedKnown ? percentText(summary.errors ?? 0, total) : null,
    // p95 is 0 when the buffer holds no request. Zero would read as "instant".
    p95: failedKnown && total > 0 && Number.isFinite(summary.p95_ms) ? summary.p95_ms : null,
    traced: failedKnown ? total : null,
  }
}

// ---------------------------------------------------------------------------
// Models: what the ledger and the backend traces say about each model
// ---------------------------------------------------------------------------

// Joins the ledger rows with the backend-operation buffer and the loaded set.
// The buffer is capped, so its counts are "in the buffer", not all time.
//   ledger   rows from groupUsage(.., 'model')
//   backend  backend traces [{ model_name, error, duration, type, timestamp }]
//   loaded   rows from localModelRows()
export function modelStats({ ledger = [], backend = [], loaded = [] }) {
  const ops = new Map()
  for (const t of backend) {
    const name = t.model_name
    if (!name) continue
    if (!ops.has(name)) ops.set(name, { operations: 0, failed: 0, lastError: '', lastErrorAt: null, ms: 0, timed: 0 })
    const o = ops.get(name)
    o.operations += 1
    if (t.error) {
      o.failed += 1
      const at = traceTime(t.timestamp)
      if (!o.lastErrorAt || (at && at > o.lastErrorAt)) { o.lastError = String(t.error); o.lastErrorAt = at }
    }
    const ms = nsToMs(t.duration)
    if (ms != null && t.status !== 'running') { o.ms += ms; o.timed += 1 }
  }
  const loadedBy = new Map(loaded.map(r => [r.model_name, r]))
  const names = new Set([...ledger.map(r => r.name), ...ops.keys(), ...loadedBy.keys()])
  const ledgerBy = new Map(ledger.map(r => [r.name, r]))
  const rows = []
  for (const name of names) {
    const l = ledgerBy.get(name)
    const o = ops.get(name)
    const m = loadedBy.get(name)
    rows.push({
      id: name,
      name,
      requests: l ? l.requests : 0,
      prompt: l ? l.prompt : 0,
      completion: l ? l.completion : 0,
      total: l ? l.total : 0,
      operations: o ? o.operations : 0,
      failed: o ? o.failed : 0,
      lastError: o ? o.lastError : '',
      meanMs: o && o.timed > 0 ? o.ms / o.timed : null,
      loaded: Boolean(m),
      backend: m?.backend || '',
      rssBytes: m?.rss_bytes ?? null,
      cpuPercent: m?.cpu_percent ?? null,
      startedAt: m?.started_at || null,
    })
  }
  return rows
}

export const MODEL_SORTERS = {
  name: (a, b) => a.name.localeCompare(b.name),
  requests: (a, b) => a.requests - b.requests,
  total: (a, b) => a.total - b.total,
  failed: (a, b) => a.failed - b.failed,
  meanMs: (a, b) => (a.meanMs ?? -1) - (b.meanMs ?? -1),
  rssBytes: (a, b) => (a.rssBytes ?? -1) - (b.rssBytes ?? -1),
}

export function sortModelRows(rows, { key = 'total', direction = 'desc' } = {}) {
  const cmp = MODEL_SORTERS[key] || MODEL_SORTERS.total
  const factor = direction === 'asc' ? 1 : -1
  return [...rows].sort((a, b) => factor * cmp(a, b) || a.name.localeCompare(b.name))
}

// ---------------------------------------------------------------------------
// Traces
// ---------------------------------------------------------------------------

// A timestamp from either buffer: an ISO string, or nanoseconds since the epoch
// (older backend traces were stored that way).
export function traceTime(value) {
  if (value == null || value === '') return null
  if (typeof value === 'number') {
    const ms = value > 1e17 ? value / 1e6 : value > 1e14 ? value / 1e3 : value
    return Number.isFinite(ms) ? ms : null
  }
  const ms = Date.parse(value)
  return Number.isNaN(ms) ? null : ms
}

// How the server counts it (see isFailure in trace_summary.go): a transport
// error or a 5xx is a failure of the runtime, a 4xx is the caller being
// refused, status 0 is still in flight.
export function traceState(trace) {
  const status = trace?.response?.status
  if (status === 0) return 'running'
  if (trace?.error || (typeof status === 'number' && status >= 500)) return 'failed'
  if (typeof status === 'number' && status >= 400) return 'refused'
  return 'ok'
}

// The words under "Why it failed": the real error and the real status, in
// plain sentences. No cause is guessed and no fix is suggested.
export function failureWords(trace) {
  const state = traceState(trace)
  const status = trace?.response?.status
  const error = trace?.error ? String(trace.error) : ''
  if (state === 'running') return { kind: 'running', status: null, error: '' }
  if (state === 'failed') return { kind: error ? 'error' : 'status', status: status || null, error }
  if (state === 'refused') return { kind: 'refused', status, error }
  return null
}

// The model a request named, read from its JSON body when the body was kept.
export function requestModel(trace) {
  const body = decodeBody(trace?.request?.body)
  if (!body) return ''
  try {
    const parsed = JSON.parse(body)
    return typeof parsed?.model === 'string' ? parsed.model : ''
  } catch {
    return ''
  }
}

// The body is base64 of the bytes (Go's []byte). Returns text, or '' when
// there is none. Text that is not JSON is returned as it is.
export function decodeBody(body) {
  if (!body) return ''
  try {
    const bin = atob(body)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new TextDecoder().decode(bytes)
  } catch {
    return typeof body === 'string' ? body : ''
  }
}

export function prettyBody(body) {
  const text = decodeBody(body)
  if (!text) return ''
  try { return JSON.stringify(JSON.parse(text), null, 2) } catch { return text }
}

// The backend operations that ran while an API request was open, matched on
// time and, when the request names one, on the model. The two buffers share no
// request id, so this is a match and the page says so. Offsets are in ms from
// the start of the request.
export function relatedOperations(trace, backendTraces) {
  const start = traceTime(trace?.timestamp)
  const length = nsToMs(trace?.duration)
  if (start == null || length == null) return []
  const model = requestModel(trace)
  const end = start + Math.max(length, 1)
  return (backendTraces || [])
    .map(op => ({ op, at: traceTime(op.timestamp), ms: nsToMs(op.duration) }))
    .filter(({ at }) => at != null && at >= start - 50 && at <= end)
    .filter(({ op }) => !model || !op.model_name || op.model_name === model)
    .map(({ op, at, ms }) => ({
      id: op.id || `${op.type}-${at}`,
      type: op.type || 'operation',
      model: op.model_name || '',
      summary: op.summary || '',
      offsetMs: Math.max(0, at - start),
      ms: ms ?? 0,
      failed: Boolean(op.error),
      error: op.error ? String(op.error) : '',
    }))
    .sort((a, b) => a.offsetMs - b.offsetMs)
}

// Rows of the traces list after the filters.
export function filterTraces(traces, { state = 'all', query = '', slowMs = 2000 } = {}) {
  const needle = String(query || '').trim().toLowerCase()
  return traces.filter(t => {
    if (state === 'failed' && traceState(t) !== 'failed') return false
    if (state === 'slow' && !((nsToMs(t.duration) ?? 0) >= slowMs)) return false
    if (!needle) return true
    return [t.request?.path, t.request?.method, t.user_name, t.user_id, t.error, t.id]
      .some(v => v && String(v).toLowerCase().includes(needle))
  })
}

export function traceCounts(traces, slowMs = 2000) {
  let failed = 0
  let slow = 0
  for (const t of traces) {
    if (traceState(t) === 'failed') failed += 1
    if ((nsToMs(t.duration) ?? 0) >= slowMs) slow += 1
  }
  return { all: traces.length, failed, slow }
}

// ---------------------------------------------------------------------------
// Export, done in the browser from rows the page already holds
// ---------------------------------------------------------------------------

function csvCell(value) {
  if (value == null) return ''
  const text = String(value)
  return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
}

export function toCSV(columns, rows) {
  const head = columns.map(c => csvCell(c.label)).join(',')
  const body = rows.map(row => columns.map(c => csvCell(c.value(row))).join(','))
  return [head, ...body].join('\n') + '\n'
}

export function saveFile(name, text, type) {
  const blob = new Blob([text], { type })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

// ---------------------------------------------------------------------------
// Prometheus
// ---------------------------------------------------------------------------

// What LocalAI's /metrics can carry. `always` ones come with metrics on; the
// others exist only while the feature they describe runs, so the page checks
// the live scrape before it calls one present.
export const KNOWN_METRICS = [
  { name: 'api_call', type: 'histogram', always: true, labels: ['method', 'path'], key: 'apiCall' },
  { name: 'localai_tokens_total', type: 'counter', labels: ['user', 'served_model', 'kind'], key: 'tokens' },
  { name: 'localai_billed_requests_total', type: 'counter', labels: ['user', 'served_model', 'endpoint'], key: 'billed' },
  { name: 'localai_cost_usd_total', type: 'counter', labels: ['user', 'served_model'], key: 'cost' },
  { name: 'localai_usage_unrecorded_total', type: 'counter', labels: ['endpoint', 'reason'], key: 'unrecorded' },
  { name: 'localai_pii_events_total', type: 'counter', labels: ['kind', 'origin', 'action', 'direction'], key: 'pii' },
  { name: 'localai_failover_switches_total', type: 'counter', labels: ['chain', 'from', 'to', 'reason'], key: 'failoverSwitches' },
  { name: 'localai_failover_target_up', type: 'gauge', labels: ['target'], key: 'failoverUp' },
  { name: 'localai_agent_runs_total', type: 'counter', labels: [], key: 'agentRuns' },
  { name: 'localai_agent_run_seconds', type: 'histogram', labels: [], key: 'agentSeconds' },
  { name: 'localai_compression_events_total', type: 'counter', labels: ['model', 'result'], key: 'compressionEvents' },
  { name: 'localai_prefix_cache_forced_disturb_total', type: 'counter', labels: ['model'], key: 'prefixCache' },
]

// The metric families a scrape names, from its "# TYPE" lines.
export function parseMetricFamilies(text) {
  const out = new Map()
  for (const line of String(text || '').split('\n')) {
    const m = /^# TYPE\s+(\S+)\s+(\S+)/.exec(line)
    if (m) out.set(m[1], m[2])
  }
  return out
}

// The known list with what a scrape saw, and the families that are not on the
// list. `families` is null when the scrape could not be read.
export function metricsReport(families) {
  if (!families) return { known: KNOWN_METRICS.map(m => ({ ...m, seen: null })), other: null }
  const known = KNOWN_METRICS.map(m => ({
    ...m,
    seen: families.has(m.name) || [...families.keys()].some(f => f === `${m.name}_total` || f.startsWith(`${m.name}_`)),
  }))
  const names = new Set(KNOWN_METRICS.map(m => m.name))
  const other = [...families.keys()].filter(f => ![...names].some(n => f === n || f.startsWith(`${n}_`)))
  return { known, other }
}

// The scrape config to paste into prometheus.yml. /metrics is admin-only, so a
// scrape needs a bearer token of an admin API key. The token is a placeholder.
export function scrapeConfig(host) {
  const target = host || 'localhost:8080'
  return [
    'scrape_configs:',
    '  - job_name: localai',
    '    metrics_path: /metrics',
    '    scheme: http',
    '    authorization:',
    '      type: Bearer',
    '      credentials: <admin API key>',
    '    static_configs:',
    `      - targets: ['${target}']`,
    '',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// The host: CPU and memory readings kept while the page is open
// ---------------------------------------------------------------------------

export const MAX_HOST_SAMPLES = 240

// A reading of the CPU share, or null when the host reports none.
export function cpuReading(resources) {
  const value = resources?.cpu?.usage_percent
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : null
}

// Same rule as appendSample in operateStatus: bounded, oldest out, one reading
// per instant, a missing value is skipped and not stored as zero.
export function appendReading(samples, value, now = Date.now(), max = MAX_HOST_SAMPLES) {
  if (value == null || !Number.isFinite(value)) return samples
  const last = samples[samples.length - 1]
  const base = last && last.t >= now ? samples.slice(0, -1) : samples
  const grown = [...base, { t: now, value }]
  return grown.length > max ? grown.slice(grown.length - max) : grown
}

// A percent line over the samples. The axis is fixed 0 to 100: a percentage
// has a natural scale, and a zoomed axis would make a quiet CPU look busy.
export function percentGeometry(samples, { width = 420, height = 150, left = 38, right = 60, top = 12, bottom = 24 } = {}) {
  if (!Array.isArray(samples) || samples.length < 2) return null
  const first = samples[0].t
  const last = samples[samples.length - 1].t
  const span = last - first
  if (!(span > 0)) return null
  const plotW = width - left - right
  const plotH = height - top - bottom
  const x = t => left + ((t - first) / span) * plotW
  const y = v => top + plotH - (v / 100) * plotH
  const points = samples.map(s => ({ x: x(s.t), y: y(s.value), t: s.t, value: s.value }))
  const path = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ')
  const steps = plotW > 300 ? 3 : 2
  const xTicks = Array.from({ length: steps + 1 }, (_, i) => {
    const t = first + (span * i) / steps
    return { x: x(t), t }
  })
  return {
    width, height, left, right, top, plotW, plotH, baseline: top + plotH,
    points, path, end: points[points.length - 1], xTicks, spanMs: span,
    ticks: [0, 50, 100].map(v => ({ value: v, y: y(v) })),
  }
}

// One GPU's memory as a row: used, total and a share, from /api/resources.
export function gpuRows(resources) {
  const gpus = Array.isArray(resources?.gpus) ? resources.gpus : []
  return gpus.map((g, i) => {
    const total = Number(g.total_vram) || 0
    const used = Math.max(0, Math.min(total, Number(g.used_vram) || 0))
    return {
      id: `${g.index ?? i}`,
      index: g.index ?? i,
      name: g.name || `GPU ${i}`,
      vendor: g.vendor || '',
      total,
      used,
      pct: total > 0 ? (used / total) * 100 : null,
    }
  })
}

// ---------------------------------------------------------------------------
// Forecast: a straight line through the buckets so far
// ---------------------------------------------------------------------------

const BUCKETS_IN = { day: 24, week: 7, month: 30 }
const HOURS_PER_BUCKET = { day: 1, week: 24, month: 24, all: 730 }

function regression(values) {
  const n = values.length
  if (n < 2) return null
  let sx = 0, sy = 0, sxy = 0, sxx = 0
  for (let i = 0; i < n; i++) { sx += i; sy += values[i]; sxy += i * values[i]; sxx += i * i }
  const denom = n * sxx - sx * sx
  if (denom === 0) return { slope: 0, intercept: sy / n }
  const slope = (n * sxy - sx * sy) / denom
  return { slope, intercept: (sy - slope * sx) / n }
}

// Where the window would end if the buckets so far kept their trend. Null for
// "all" (it has no end), for a window already complete and for fewer than two
// buckets. It is a straight line, and the page calls it that.
export function projectTotals(series, period) {
  const total = BUCKETS_IN[period]
  if (!total || !Array.isArray(series) || series.length < 2) return null
  const remaining = total - series.length
  if (remaining <= 0) return null
  const out = {}
  for (const key of ['requests', 'prompt', 'completion', 'total']) {
    const reg = regression(series.map(p => p[key]))
    let sum = series.reduce((s, p) => s + p[key], 0)
    for (let i = 0; i < remaining; i++) sum += Math.max(0, Math.round(reg.intercept + reg.slope * (series.length + i)))
    out[key] = sum
  }
  return out
}

// For each quota: how far along it is, and whether the current pace stays
// inside it until it resets. `quotas` is the /api/auth/quota list.
export function quotaForecast(quotas, series, period) {
  if (!quotas?.length || !series?.length) return []
  const hpb = HOURS_PER_BUCKET[period] || 24
  const perHour = key => (series.reduce((s, p) => s + p[key], 0) / series.length) / hpb
  const tokensPerHour = perHour('total')
  const requestsPerHour = perHour('requests')
  const out = []
  for (const q of quotas) {
    const resets = q.resets_at ? Date.parse(q.resets_at) : NaN
    const hoursToReset = Number.isFinite(resets) ? Math.max(0, (resets - Date.now()) / 3_600_000) : Infinity
    const items = []
    const one = (label, current, max, rate) => {
      const left = rate > 0 ? (max - current) / rate : Infinity
      items.push({ label, current, max, hoursLeft: Math.min(left, hoursToReset), within: left >= hoursToReset })
    }
    if (q.max_total_tokens != null) one('tokens', q.current_tokens || 0, q.max_total_tokens, tokensPerHour)
    if (q.max_requests != null) one('requests', q.current_requests || 0, q.max_requests, requestsPerHour)
    if (items.length) out.push({ model: q.model || '', window: q.window, items })
  }
  return out
}
