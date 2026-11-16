// Pure rules of the Projects pages. No React and no network, so a node test
// drives them directly.
import { startPick } from './defaults.js'
import { isSharedRef, pulledSharedEntries } from './sharedProjects.js'

export const MEMORY_INDEX = 'index.md'
// The names the server accepts for a memory file.
const MEMORY_FILE = /^[a-z0-9][a-z0-9._-]{0,63}\.md$/

// '' when name may be a memory file, 'empty' or 'invalid' otherwise.
export function memoryFileError(name) {
  if (!name) return 'empty'
  return MEMORY_FILE.test(name) ? '' : 'invalid'
}

// The server refuses a name longer than this many characters.
export const NAME_MAX = 120
// The counter under the name shows from this length on.
export const NAME_COUNTER_FROM = 100
// Chats get the first this many characters of the description as context.
export const DESCRIPTION_CONTEXT_CHARS = 200

export const PROJECT_STATUSES = Object.freeze(['all', 'active', 'archived', 'broken'])

export function projectCounts(projects) {
  const list = projects || []
  const n = (s) => list.filter(p => p.status === s).length
  return { all: list.length, active: n('active'), archived: n('archived'), broken: n('broken') }
}

// The rows a status and a search text leave. The text matches the name and the
// description, ignoring case.
export function filterProjects(projects, { status = 'all', query = '' } = {}) {
  const q = query.trim().toLowerCase()
  return (projects || []).filter(p => {
    if (status !== 'all' && p.status !== status) return false
    if (!q) return true
    return (p.name || '').toLowerCase().includes(q) || (p.description || '').toLowerCase().includes(q)
  })
}

// The rows in the order a person reads them: by name, ignoring case and
// accents, then by slug so two projects with one name keep a stable order.
// The list is copied; the argument is not changed.
export function sortProjects(projects) {
  const cmp = new Intl.Collator(undefined, { sensitivity: 'base', numeric: true })
  return [...(projects || [])].sort((a, b) => cmp.compare(a.name || '', b.name || '') || cmp.compare(a.slug || '', b.slug || ''))
}

// The memory topics of a project as the Overview counts them: the topic files
// and the index, but the index only when it says something. An index that holds
// only headings is the empty template.
export function memoryTopicCount(memory) {
  const files = (memory?.files || []).filter(f => f.name !== MEMORY_INDEX).length
  const text = (memory?.index || '').split('\n').some(l => l.trim() && !l.trim().startsWith('#'))
  return files + (text ? 1 : 0)
}

// The server answers 409 projects_off when the feature is switched off.
export function isProjectsOff(err) {
  return err?.status === 409 && err.body?.error?.type === 'projects_off'
}

// The folder of a project in the Files page, for a project whose manifest is
// damaged and that has no page of its own.
export function projectFolderLink(project) {
  return `/app/files?path=/Projects/${project.slug}`
}

// Where the files of a project are, in the Files page.
export function projectFilesPath(project) {
  return `/Projects/${project.slug}/files`
}

// What the chat create call needs to start a chat in a project.
export function startChatBody(project) {
  return { project_id: project.id }
}

export const DEFAULT_LISTS = Object.freeze(['skills', 'addons', 'mcp_servers', 'connectors', 'collections'])
// The lists the server applies to a new chat in the project. Connectors turn
// on only the named ones; collections restrict the chat to the named ones.
export const APPLIED_LISTS = Object.freeze(['skills', 'addons', 'mcp_servers', 'connectors', 'collections'])

// Trimmed, de-duplicated lists with the empty ones left out: an empty list
// means no override and is not sent.
export function cleanDefaults(defaults) {
  const out = {}
  for (const key of DEFAULT_LISTS) {
    const list = [...new Set((defaults?.[key] || []).map(v => String(v).trim()).filter(Boolean))]
    if (list.length) out[key] = list
  }
  return out
}

export function defaultsCount(defaults) {
  return DEFAULT_LISTS.reduce((n, key) => n + (defaults?.[key]?.length || 0), 0)
}

// The state of one entry of a default list against what is installed:
//   'ok'       the source lists it and it can run
//   'off'      the source lists it but it cannot run now (for example a
//              connector that is switched off)
//   'missing'  the source loaded and does not list it
//   'unknown'  the source did not load, so nothing is claimed
// options are { value, usable }; ready says whether the source loaded.
export function entryState(options, ready, value) {
  if (!ready) return 'unknown'
  const hit = (options || []).find(o => o.value === value)
  if (!hit) return 'missing'
  return hit.usable === false ? 'off' : 'ok'
}

// How many entries of a list are not available (missing or off).
export function unavailableCount(options, ready, list) {
  return (list || []).filter(v => ['missing', 'off'].includes(entryState(options, ready, v))).length
}

// The options a picker offers for a typed text: those not chosen yet whose
// label or value contains the text (ignoring case), and whether the text is
// itself a new freeform entry (not empty, not chosen, no option has that value).
export function pickerChoices(options, chosen, text) {
  const q = (text || '').trim().toLowerCase()
  const taken = new Set(chosen || [])
  const list = (options || []).filter(o => !taken.has(o.value) && (!q || `${o.label} ${o.value}`.toLowerCase().includes(q)))
  const raw = (text || '').trim()
  const freeform = Boolean(raw) && !taken.has(raw) && !(options || []).some(o => o.value === raw)
  return { list, freeform: freeform ? raw : '' }
}

export function addChip(list, text) {
  const v = (text || '').trim()
  return v && !list.includes(v) ? [...list, v] : list
}

// "2 hours ago", "yesterday", in the person's language. Older than a month
// falls back to a short date.
export function whenAgo(iso, lang, now = Date.now()) {
  const at = Date.parse(iso)
  if (!iso || Number.isNaN(at)) return ''
  const sec = Math.round((at - now) / 1000)
  const abs = Math.abs(sec)
  const units = [['second', 60], ['minute', 3600, 60], ['hour', 86400, 3600], ['day', 2592000, 86400]]
  try {
    const rtf = new Intl.RelativeTimeFormat(lang || undefined, { numeric: 'auto' })
    for (const [unit, limit, size = 1] of units) {
      if (abs < limit) return rtf.format(Math.round(sec / size), unit)
    }
    return new Date(at).toLocaleDateString(lang || undefined, { month: 'short', day: 'numeric', ...(new Date(at).getFullYear() === new Date(now).getFullYear() ? {} : { year: 'numeric' }) })
  } catch {
    return new Date(at).toLocaleDateString()
  }
}

// ---- Chat controls: the composer chip, the chat header and chat settings.

// The most projects one chat can pull in. The server refuses more.
export const PULLED_MAX = 8
// The value of the All chats filter for chats that belong to no project.
export const NO_PROJECT = '__none__'

// The projects a new chat can start in: active ones only, matching the search
// text on the name (ignoring case).
export function composerProjects(projects, query = '') {
  const q = (query || '').trim().toLowerCase()
  return (projects || []).filter(p => p.status === 'active' && (!q || (p.name || '').toLowerCase().includes(q)))
}

// The active projects a chat can still pull in: not its home, not pulled yet.
export function pulledChoices(projects, chat) {
  const taken = new Set([chat?.project_id, ...(chat?.pulled_projects || [])].filter(Boolean))
  return composerProjects(projects).filter(p => !taken.has(p.id))
}

// The pulled list after adding or removing one id. An id is never in twice and
// the list never grows past PULLED_MAX.
export function withPulled(list, id, add) {
  const cur = list || []
  if (!add) return cur.filter(x => x !== id)
  if (cur.includes(id) || cur.length >= PULLED_MAX) return cur
  return [...cur, id]
}

export function projectNames(projects) {
  const out = {}
  for (const p of projects || []) out[p.id] = p.name
  return out
}

// The chats a project filter leaves: '' is all, NO_PROJECT those with no home.
// A chat is in a project only while that project exists. When the loaded
// projects are given, a chat whose project_id names none of them (the project
// was deleted) counts as having no project. Without the list (not loaded yet)
// the id alone decides.
export function chatsInProject(chats, filter, projects) {
  const list = chats || []
  if (!filter) return list
  const known = Array.isArray(projects) ? new Set(projects.map(p => p.id)) : null
  const homed = (c) => Boolean(c.projectId) && (!known || known.has(c.projectId))
  if (filter === NO_PROJECT) return list.filter(c => !homed(c))
  return list.filter(c => c.projectId === filter)
}

// The home project of a chat as { id, name }, or null when the chat has none or
// the project cannot be resolved (deleted, or the feature is off).
export function homeProject(chat, projects) {
  const p = chat?.project_id && (projects || []).find(x => x.id === chat.project_id)
  return p ? { id: p.id, name: p.name } : null
}

// What a pulled project is to this chat: 'mounted' for an active project,
// 'archived' for an archived one and 'unavailable' for a broken one. Only a
// mounted one is readable in the chat; the server drops the others each turn.
export function pulledState(project) {
  if (project?.status === 'active') return 'mounted'
  return project?.status === 'archived' ? 'archived' : 'unavailable'
}

// The pulled projects of a chat, in order. An own id is resolved in the owner's
// list and dropped when it is gone; a shared ref ("s:<number>") is resolved in
// the shared list (see sharedProjects.js) and is shown as removed when that
// list no longer names it.
export function pulledProjects(chat, projects, shared = null) {
  const sharedByRef = new Map(pulledSharedEntries((chat?.pulled_projects || []).filter(isSharedRef), shared).map(e => [e.id, e]))
  return (chat?.pulled_projects || [])
    .map((id) => {
      if (isSharedRef(id)) return sharedByRef.get(id)
      const p = (projects || []).find(x => x.id === id)
      return p ? { id: p.id, name: p.name, state: pulledState(p) } : undefined
    })
    .filter(Boolean)
}

// The model a new chat starts with when the page has no picker: the default of
// defaults.json, else the last model used, else the first usable one. '' when
// no chat model is usable.
export function pickStartModel(models, choice, remembered) {
  const usable = (models || []).filter(m => m && !m.disabled && !m.alias_error).map(m => m.id)
  const picked = startPick(choice, usable, remembered).model
  return picked || (remembered && usable.includes(remembered) ? remembered : usable[0]) || ''
}

// The server refuses to change a chat while a turn runs with a 409.
export function isBusy(err) {
  return err?.status === 409 && err.body?.error?.type !== 'projects_off'
}

// The index a tab list moves to for a key, or -1 when the key moves nothing.
export function wrapKey(key, index, count) {
  if (key === 'ArrowRight') return (index + 1) % count
  if (key === 'ArrowLeft') return (index - 1 + count) % count
  if (key === 'Home') return 0
  if (key === 'End') return count - 1
  return -1
}

// The attached lists have the shape of the defaults, so one clean step serves both.
export const cleanAttached = (attached) => cleanDefaults(attached)
export const attachedCount = (attached) => defaultsCount(attached)

// The attached entries that a non-empty limit leaves out. The server refuses
// such a project (HTTP 400), so the form says it before Save. Returns
// [{ key, name }] in list order; an empty limit list limits nothing.
export function outsideLimit(attached, limit) {
  const out = []
  for (const key of DEFAULT_LISTS) {
    const cap = limit?.[key] || []
    if (cap.length === 0) continue
    for (const name of attached?.[key] || []) if (!cap.includes(name)) out.push({ key, name })
  }
  return out
}

// The path of the skill editor for one skill of the project layer, or for a
// new one. The editor reads the layer and the project from the URL.
export const projectSkillPath = (project, name) => (name
  ? `/app/skills/edit/project/${encodeURIComponent(name)}?project=${encodeURIComponent(project.id)}`
  : `/app/skills/new?layer=project&project=${encodeURIComponent(project.id)}`)
