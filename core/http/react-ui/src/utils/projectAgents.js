// Pure rules of the agents that belong to a project. No React and no network,
// so a node test drives them directly.

export const isProjectAgent = (agent) => Boolean(agent?.project_id)

// The list a project sets for one kind of tool, or null when it sets none. A
// non-empty list is a ceiling for its agents; an empty one is no limit.
export function ceilingOf(project, key) {
  const list = project?.defaults?.[key]
  return Array.isArray(list) && list.length > 0 ? list : null
}

// The items inside the ceiling. No ceiling keeps every item. An item the agent
// holds that nothing defines (`gone`) stays, so the person can still remove it.
export function withinCeiling(items, allowed, keyOf) {
  if (!allowed || allowed.length === 0) return items
  const ok = new Set(allowed)
  return items.filter((i) => i.gone || ok.has(keyOf(i)))
}

const connectorKey = (c) => c.id
// The tool lists of the agent editor as a project lets its agents see them.
//   rows: { addons, connectors, skills, servers (names), collections (names) }
// `limited` says per list whether a ceiling applied, for the helper text.
export function limitedTools(project, rows) {
  const lists = {
    skills: ceilingOf(project, 'skills'),
    addons: ceilingOf(project, 'addons'),
    mcp_servers: ceilingOf(project, 'mcp_servers'),
    connectors: ceilingOf(project, 'connectors'),
    collections: ceilingOf(project, 'collections'),
  }
  return {
    addons: withinCeiling(rows.addons || [], lists.addons, (a) => a.id),
    connectors: withinCeiling(rows.connectors || [], lists.connectors, (c) => (lists.connectors?.includes(connectorKey(c)) ? connectorKey(c) : c.name)),
    skills: withinCeiling(rows.skills || [], lists.skills, (s) => s.name),
    servers: withinCeiling((rows.servers || []).map((name) => ({ name })), lists.mcp_servers, (s) => s.name).map((s) => s.name),
    collections: withinCeiling((rows.collections || []).map((name) => ({ name })), lists.collections, (s) => s.name).map((s) => s.name),
    limited: Object.fromEntries(Object.entries(lists).map(([k, v]) => [k, v !== null])),
  }
}

// Why an agent of a project cannot start: 'archived', 'deleted' (the project
// is gone or damaged), 'changed' (a project folder was replaced) or ''.
// flagged is the server's `unavailable` flag; gone is true when the project
// is known to be missing.
export function unavailableReason(project, flagged, gone = false) {
  if (!project) return flagged || gone ? 'deleted' : ''
  if (project.status === 'archived') return 'archived'
  if (project.status === 'broken') return flagged ? 'deleted' : ''
  return flagged ? 'changed' : ''
}

// The reason for a row of the agents list. projectsById is null while the
// projects are not known (the feature is off or the read failed): then
// nothing is claimed. flags maps an agent id to the server's flag.
export function agentIsUnavailable(agent, projectsById, flags) {
  if (!isProjectAgent(agent) || !projectsById) return ''
  const project = projectsById[agent.project_id]
  return unavailableReason(project, Boolean(flags?.[agent.id]), !project)
}

// The mapping an import sends as query parameters. The file never carries it.
export function importParams(choice) {
  if (choice?.projectId) return { project_id: choice.projectId }
  if (choice?.create) return { create_project: true }
  return null
}

// The 409 an import answers when the file belongs to a project: { name, message }.
export function projectRequiredOf(err) {
  if (err?.status !== 409 || err.body?.error?.type !== 'project_required') return null
  return { name: err.body.error.project_name || '', message: err.body.error.message || '' }
}

// A chat or a run refused because the project of the agent cannot take it:
// 'unavailable' (archived, deleted), 'changed' (folders replaced) or ''.
export function startRefusal(err) {
  if (err?.status !== 409) return ''
  const type = err.body?.error?.type
  if (type === 'project_unavailable') return 'unavailable'
  if (type === 'project_folders_changed') return 'changed'
  return ''
}

export const projectFilesFolder = (project) => `/Projects/${project.slug}/files`
export const projectMemoryFolder = (project) => `/Projects/${project.slug}/memory`

// The two fixed folders of a project agent: the files at a mode, the memory
// always read.
export function projectFolderRows(project, mode) {
  return [
    { path: projectFilesFolder(project), mode: mode === 'read' ? 'read' : 'write' },
    { path: projectMemoryFolder(project), mode: 'read' },
  ]
}

// How the agent uses the project files, from its stored rows: the row that is
// not the memory folder. Write when the rows say nothing.
export function filesModeOf(agent) {
  const rows = Array.isArray(agent?.folders) ? agent.folders : []
  const files = rows.find((r) => r.path && !/\/memory\/?$/.test(r.path))
  return files?.mode === 'read' ? 'read' : 'write'
}

// The `folders` of a create or replace of a project agent, or undefined to
// send none. The server fills the two roots (files at write) when a create has
// no list, and keeps the stored ones when a replace has none, so folders are
// sent only for a mode that differs from what the server would keep: a create
// at read, or an edit that changed the mode. Never any other folder.
export function projectAgentFolders(project, mode, stored) {
  return mode === stored ? undefined : projectFolderRows(project, mode)
}
