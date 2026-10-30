// Human words for a tool call in the chat: a past-tense verb, the one argument
// that matters and, when the output says it, how much came back. The raw tool
// id stays available to the step detail; this only decides what a person reads
// first ("Read quote-acme.pdf", "Listed /Contracts · 3 files").
//
// The verb is picked from the words of the tool name, so "read_file",
// "files__read" and "read_document" all read as "Read". A name with no known
// word is "Used <tool>".

// Checked in order: the first verb with a word in the name wins, so
// "search_files" is a search and "list_collections" a listing.
const VERBS = [
  ['delegate', (w) => w.includes('spawn') || (w.includes('agent') && (w.includes('ask') || w.includes('call')))],
  ['search', (w) => w.some((x) => ['search', 'grep', 'find', 'lookup'].includes(x))],
  ['list', (w) => w.some((x) => ['list', 'ls', 'glob', 'tree'].includes(x))],
  ['read', (w) => w.some((x) => ['read', 'cat', 'open', 'view'].includes(x))],
  ['create', (w) => w.some((x) => ['create', 'new', 'mkdir'].includes(x))],
  ['write', (w) => w.some((x) => ['write', 'save'].includes(x))],
  ['change', (w) => w.some((x) => ['edit', 'update', 'patch', 'replace', 'modify', 'change'].includes(x))],
  ['move', (w) => w.some((x) => ['move', 'rename', 'mv'].includes(x))],
  ['delete', (w) => w.some((x) => ['delete', 'remove', 'rm', 'trash'].includes(x))],
  ['run', (w) => w.some((x) => ['bash', 'shell', 'sh', 'exec', 'run', 'command', 'python', 'execute'].includes(x))],
  ['fetch', (w) => w.some((x) => ['fetch', 'browse', 'http', 'curl', 'download', 'url'].includes(x))],
]

// Verbs whose argument is one file: the step shows its name, the full path is
// in the title.
const FILE_VERBS = new Set(['read', 'write', 'create', 'change', 'delete'])

// Argument keys, the most telling first.
const ARG_KEYS = ['path', 'file', 'file_path', 'filename', 'query', 'q', 'pattern', 'url', 'command', 'cmd', 'name', 'from']

// The words of a tool name: the last part of a namespaced id ("files__read",
// "addon__sandbox__bash"), split on _, - and . and lower-cased.
export function toolWords(name) {
  const parts = String(name || '').toLowerCase().split('__').filter(Boolean)
  const last = parts.length > 0 ? parts[parts.length - 1] : ''
  return last.split(/[_\-.\s]+/).filter(Boolean)
}

export function toolVerb(name) {
  const words = toolWords(name)
  for (const [verb, test] of VERBS) if (test(words)) return verb
  return 'used'
}

// The argument a step shows. The server stores a short summary (a path, a
// command, a pattern); some harnesses store the arguments as JSON.
export function toolArg(summary) {
  const s = String(summary || '').trim()
  if (!s.startsWith('{')) return s
  try {
    const args = JSON.parse(s)
    for (const k of ARG_KEYS) {
      if (typeof args[k] === 'string' && args[k].trim()) return args[k].trim()
    }
    return ''
  } catch {
    return s
  }
}

const baseName = (p) => {
  const trimmed = p.replace(/\/+$/, '')
  const at = trimmed.lastIndexOf('/')
  return at >= 0 && at < trimmed.length - 1 ? trimmed.slice(at + 1) : trimmed
}

const nonEmptyLines = (s) => String(s || '').split('\n').map((l) => l.trim()).filter(Boolean)

// What the output says, in a word: a count for a listing or a search, a size
// for a write. null when there is nothing worth saying. Returned as an i18n
// key of the chat namespace (steps.*) with its values.
export function toolResult(verb, output) {
  const out = String(output || '').trim()
  if (!out) return null
  if (verb === 'list') {
    if (/^the folder is empty\.?$/i.test(out)) return { key: 'steps.emptyFolder' }
    let entries
    try {
      const parsed = JSON.parse(out)
      if (Array.isArray(parsed)) entries = parsed.map(String)
    } catch { /* not JSON: one entry per line */ }
    if (!entries) entries = nonEmptyLines(out)
    if (entries.length === 0) return null
    const folders = entries.some((e) => e.endsWith('/'))
    return { key: folders ? 'steps.items' : 'steps.files', values: { count: entries.length } }
  }
  if (verb === 'search') {
    if (/^nothing found\.?$/i.test(out)) return { key: 'steps.noMatches' }
    return { key: 'steps.matches', values: { count: nonEmptyLines(out).length } }
  }
  if (verb === 'write' || verb === 'create') {
    const bytes = out.match(/(\d+) bytes/)
    if (bytes) return { key: 'steps.size', values: { size: formatBytes(Number(bytes[1])) } }
    const lines = out.match(/(\d+) lines?/)
    if (lines) return { key: 'steps.lines', values: { count: Number(lines[1]) } }
  }
  return null
}

// One tool call as a person reads it.
//   verb     the i18n verb (steps.verb.<verb>); "used" names the tool
//   arg      what to show after the verb ('' when nothing)
//   argTitle the full argument, for the title
//   file     the argument is a file the call wrote: shown as a chip
//   result   see toolResult
export function toolLabel(name, summary, output) {
  const verb = toolVerb(name)
  const full = toolArg(summary)
  const arg = full && FILE_VERBS.has(verb) ? baseName(full) : full
  return {
    verb,
    tool: String(name || ''),
    arg,
    argTitle: full,
    file: Boolean(arg) && (verb === 'write' || verb === 'create'),
    result: toolResult(verb, output),
  }
}

export function formatBytes(n) {
  if (!Number.isFinite(n) || n < 0) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

// A duration for a step or a turn: "0.4 s", "12 s", "1 min 5 s". The numbers
// follow the language's decimal sign; t gives the units (steps.seconds,
// steps.minutes).
export function formatDuration(ms, t, lng) {
  if (!Number.isFinite(ms) || ms < 0) return ''
  const sec = ms / 1000
  if (sec < 60) {
    const value = new Intl.NumberFormat(lng || undefined, {
      minimumFractionDigits: sec < 10 ? 1 : 0,
      maximumFractionDigits: sec < 10 ? 1 : 0,
    }).format(sec < 10 ? Math.max(0.1, Math.round(sec * 10) / 10) : Math.round(sec))
    return t('steps.seconds', { value })
  }
  const total = Math.round(sec)
  return t('steps.minutes', { min: Math.floor(total / 60), sec: total % 60 })
}

// How long each step of a stored run took, from the times the transcript
// wrote its lines. A reasoning line carries how long the model thought. A
// tool line is written when the call ends, so its time is the gap since the
// line before it; the first tool line of a run has no line before it in the
// run, so its time is not known (null). The total is known when the run starts
// with reasoning, whose start is its end minus its duration.
export function runTimes(items) {
  const list = items || []
  const ats = list.map((i) => Date.parse(i?.at))
  const steps = list.map((item, i) => {
    if (item?.role === 'reasoning') return item.dur_ms > 0 ? item.dur_ms : null
    if (i === 0 || !Number.isFinite(ats[i]) || !Number.isFinite(ats[i - 1])) return null
    const gap = ats[i] - ats[i - 1]
    return gap >= 0 ? gap : null
  })
  let total = null
  const first = list[0]
  if (first?.role === 'reasoning' && first.dur_ms > 0 && Number.isFinite(ats[0]) && Number.isFinite(ats[ats.length - 1])) {
    total = ats[ats.length - 1] - ats[0] + first.dur_ms
  }
  return { steps, total }
}
