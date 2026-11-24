// Pure rules of the Project field of an agent and of the move of an agent
// between projects. No React and no network, so a node test drives them.
import { composerProjects } from './projects.js'

// The value of the "create a new project" option of the field.
export const CREATE_PROJECT = '__create__'

// The options of the Project field: the active projects of the person, by name,
// plus the current project when it is not among them (an archived one), so a
// select never shows a value that has no option.
export function projectOptions(projects, currentId = '') {
  const active = composerProjects(projects)
    .slice()
    .sort((a, b) => (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' }))
  if (currentId && !active.some((p) => p.id === currentId)) {
    const cur = (projects || []).find((p) => p.id === currentId)
    if (cur) return [cur, ...active]
  }
  return active
}

// The body of the move request for a choice made in the dialog.
//   target     the project id, '' to leave a project
//   opts       { filesMode, folders: [{path, mode}], wholeHome, revokeShares,
//                disableTriggers }
//   dryRun     true asks for the report only
export function moveBody(target, opts = {}, dryRun = false) {
  const body = { project_id: target || '', dry_run: dryRun }
  if (target) {
    body.files_mode = opts.filesMode === 'read' ? 'read' : 'write'
  } else if (opts.wholeHome) {
    body.whole_home = true
  } else {
    body.folders = (opts.folders || []).filter((f) => f.path).map((f) => ({ path: f.path, mode: f.mode === 'write' ? 'write' : 'read' }))
  }
  if (opts.revokeShares) body.revoke_shares = true
  if (opts.disableTriggers) body.disable_triggers = true
  return body
}

// Whether the choice of the dialog is complete enough to ask the server: a
// move out of a project needs a folder or the whole home.
export function moveReady(target, opts = {}) {
  if (target) return true
  return Boolean(opts.wholeHome) || (opts.folders || []).some((f) => f.path)
}

// What the dialog shows from a report: one entry per effect that is not
// zero, in a stable order. Each entry is { key, count?, names? } and the
// dialog turns the key into a sentence.
export function effectsOf(report) {
  if (!report) return []
  const out = []
  if (report.chats > 0) out.push({ key: 'chats', count: report.chats })
  if (report.loops > 0) out.push({ key: 'loops', count: report.loops })
  if ((report.triggers_outside || []).length > 0) out.push({ key: 'triggers', count: report.triggers_outside.length, names: report.triggers_outside.map((t) => t.name) })
  if (report.shares > 0) out.push({ key: 'shares', count: report.shares })
  if (report.proposals > 0) out.push({ key: 'proposals', count: report.proposals })
  const d = report.dropped_tools || {}
  const dropped = [...(d.skills || []), ...(d.addons || []), ...(d.mcp_servers || []), ...(d.collections || [])]
  if (dropped.length > 0) out.push({ key: 'dropped', count: dropped.length, names: dropped })
  // attached_old stops applying (what the old project attached and the new one
  // does not); attached_new starts. Same lists as dropped_tools, with connectors.
  const flat = (x) => ['skills', 'addons', 'mcp_servers', 'connectors', 'collections'].flatMap((k) => (x?.[k] || []))
  const lost = flat(report.attached_old)
  const gained = flat(report.attached_new)
  if (lost.length > 0) out.push({ key: 'attachedLost', count: lost.length, names: lost })
  if (gained.length > 0) out.push({ key: 'attachedGained', count: gained.length, names: gained })
  if (report.project_skills_switch) out.push({ key: 'projectSkills' })
  if (report.memory_grant_dropped) out.push({ key: 'grant' })
  return out
}

// The blockers of a report that the person cannot lift in the dialog (a turn
// that runs), and the ones a switch lifts.
export const hardBlockers = (report) => (report?.blockers || []).filter((b) => b === 'busy')
export const needsRevoke = (report) => (report?.blockers || []).includes('shared')
export const needsDisable = (report) => (report?.blockers || []).includes('triggers_outside')

// The folder a person most likely keeps when an agent leaves its project: the
// files folder of the project it leaves, when that project still exists.
export function suggestedFolder(project) {
  return project?.slug && project.status !== 'broken' ? { path: `/Projects/${project.slug}/files`, mode: 'read' } : null
}

// The agent's chip of the project field: the name, or the "deleted" state.
export const projectNameOf = (projects, id) => (projects || []).find((p) => p.id === id)?.name || ''

// The error of a move as a code the dialog can name: 'busy', 'blocked' or ''.
export function moveRefusal(err) {
  if (err?.status !== 409) return ''
  return err.body?.error?.type === 'agent_move_blocked' ? 'blocked' : ''
}
