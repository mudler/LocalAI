// Pure rules that tie a trigger to a project and to the folders of its agent.
// No React and no network, so a node test drives them directly.
//
// A trigger belongs to a project through its agent: the agent has a project_id.
// An agent with a project or a folder works inside a fence, and the server
// refuses a file trigger whose folder lies outside the agent's folders. These
// helpers say the same thing before Save, so the form never offers a folder the
// server would refuse.

import { folderRows } from './chatAgents.js'

const MEMORY = /\/memory$/

/** The project of an agent as { id, name, status }, or null for an agent of no project. */
export function projectOfAgent(agent, projects) {
  if (!agent?.project_id) return null
  const p = (projects || []).find((x) => x.id === agent.project_id)
  return p ? { id: p.id, name: p.name, status: p.status } : null
}

/** The project of a trigger, through its agent, or null. */
export function projectOfTrigger(trigger, agents, projects) {
  const agent = (agents || []).find((a) => a.id === trigger?.agent_id)
  return projectOfAgent(agent, projects)
}

/** An option label for the agent list: a project agent says its project. */
export function agentLabel(agent, projects, withProject) {
  const p = projectOfAgent(agent, projects)
  return p ? withProject(agent.name, p.name) : agent.name
}

/**
 * The folders a file trigger of this agent may watch, as paths, or null when
 * the agent is not fenced (any folder of the person's files). A folder row with
 * no public path or in a bad state is not offered.
 */
export function allowedFolders(agent) {
  if (!agent) return null
  const rows = folderRows(agent).filter((r) => r.path && (r.state || 'ok') === 'ok')
  if (rows.length === 0) return agent.project_id ? [] : null
  return rows.map((r) => r.path)
}

/** The folder a new file trigger starts with: the project files, else the first folder. */
export function defaultFolder(agent) {
  const list = allowedFolders(agent)
  if (!list || list.length === 0) return ''
  return list.find((p) => !MEMORY.test(p)) || list[0]
}

const holds = (outer, inner) => outer === '/' || inner === outer || inner.startsWith(`${outer}/`)

/** The folder of the list that holds path, the innermost one, or '' when none does. */
export function baseOf(path, allowed) {
  let best = ''
  for (const a of allowed || []) {
    if (holds(a, path) && a.length >= best.length) best = a
  }
  return best
}

/** The part of path under its base folder, without a leading slash. */
export function subOf(path, base) {
  if (!base || base === '/') return (path || '').replace(/^\/+/, '')
  return path === base ? '' : path.slice(base.length + 1)
}

/** The folder a base and a subfolder name together. */
export function joinFolder(base, sub) {
  const s = (sub || '').trim().replace(/^\/+|\/+$/g, '')
  if (!s) return base
  return base === '/' ? `/${s}` : `${base}/${s}`
}

/**
 * Why a folder cannot be used, as a code, or '' when it can: 'empty', 'dots'
 * (a ".." that would leave the folder), 'outside' (not inside any folder of
 * the agent; only when the agent is fenced). The server decides in the end; this
 * only stops what it would refuse for sure.
 */
export function folderProblem(path, allowed) {
  const p = (path || '').trim()
  if (!p) return 'empty'
  if (p.split('/').includes('..')) return 'dots'
  if (allowed && !baseOf(p, allowed)) return 'outside'
  return ''
}

/** The triggers a project filter keeps. filter is 'all', 'none' (no project) or a project id. */
export function filterByProject(triggers, agents, filter) {
  if (!filter || filter === 'all') return triggers
  const projectOf = (tr) => (agents || []).find((a) => a.id === tr.agent_id)?.project_id || ''
  return triggers.filter((tr) => (filter === 'none' ? !projectOf(tr) : projectOf(tr) === filter))
}

/** The projects that have a trigger, for the filter: [{ id, name }] sorted by name. */
export function projectsWithTriggers(triggers, agents, projects) {
  const seen = new Map()
  for (const tr of triggers) {
    const p = projectOfTrigger(tr, agents, projects)
    if (p) seen.set(p.id, p.name)
  }
  return [...seen].map(([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))
}

/** The agent a "new trigger" of a project starts with: the first available agent by name, or null. */
export function firstProjectAgent(rows) {
  return [...(rows || [])].filter((r) => !r.unavailable).sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))[0] || null
}
