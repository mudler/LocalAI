// Agent tasks and their jobs (/api/agent/tasks and /api/agent/jobs, the
// mcp_jobs feature). A task is a prompt template, run in the background by a
// model or a saved agent; a job is one run. Pure rules, so a node test drives
// them.

// The parameters a prompt template names ({{.name}} or {{ .name }}), in order.
export function promptParams(prompt) {
  const out = []
  for (const m of String(prompt || '').matchAll(/\{\{-?\s*\.([A-Za-z_][A-Za-z0-9_]*)\s*-?\}\}/g)) {
    if (!out.includes(m[1])) out.push(m[1])
  }
  return out
}

// The state the Agents area draws (StatusMark) for a job status of the server:
// pending, running, completed, failed or cancelled.
const MARK = { pending: 'running', running: 'running', completed: 'done', failed: 'failed', cancelled: 'stopped' }
export const jobState = (status) => MARK[status] || 'ready'
export const jobActive = (job) => job?.status === 'pending' || job?.status === 'running'

// The form of a task, from a task of the server or empty.
export function taskForm(task) {
  const t = task || {}
  return {
    name: t.name || '',
    description: t.description || '',
    runner: t.agent_id ? 'agent' : 'model',
    model: t.model || '',
    agent_id: t.agent_id || '',
    prompt: t.prompt || '',
    enabled: t.enabled !== false,
  }
}

// The body of a create or an update. An update replaces every field, so what
// the form does not edit (webhooks, whose headers come back as "[hidden]" and
// keep their value that way) is sent back as the server gave it.
export function taskBody(form, task) {
  const body = {
    ...(task || {}),
    name: form.name.trim(),
    description: form.description.trim(),
    prompt: form.prompt,
    enabled: form.enabled,
    model: form.runner === 'model' ? form.model : '',
    agent_id: form.runner === 'agent' ? form.agent_id : '',
  }
  delete body.id
  delete body.created_at
  delete body.updated_at
  return body
}

// What is missing before a task can be saved: a list of field names.
export function taskMissing(form) {
  const missing = []
  if (!form.name.trim()) missing.push('name')
  if (!form.prompt.trim()) missing.push('prompt')
  if (form.runner === 'model' && !form.model) missing.push('model')
  if (form.runner === 'agent' && !form.agent_id) missing.push('agent_id')
  return missing
}

// A list answer of the server: the caller's own items, plus, for an
// administrator who asked with all_users, the other users' items keyed by owner.
// It returns [{ owner, item }] with owner '' for the caller's own.
export function withOwners(answer, key) {
  if (Array.isArray(answer)) return answer.map(item => ({ owner: '', item }))
  const own = (answer?.[key] || []).map(item => ({ owner: '', item }))
  const others = Object.entries(answer?.user_groups || {}).flatMap(([owner, g]) => (g?.[key] || []).map(item => ({ owner, item })))
  return [...own, ...others]
}

// The time a job is ordered by: when it was asked for.
const jobAt = (job) => {
  const t = Date.parse(job?.created_at || '')
  return Number.isNaN(t) ? 0 : t
}

// The jobs of one task, newest first.
export function jobsOfTask(jobs, taskId) {
  return jobs.filter(j => j.task_id === taskId).sort((a, b) => jobAt(b) - jobAt(a))
}

// The filter groups of the run history. "active" holds the jobs still waiting
// or running, so a person looks in one place for what is going on.
export const JOB_FILTERS = ['all', 'active', 'completed', 'failed', 'cancelled']
export function jobInFilter(job, filter) {
  if (filter === 'all') return true
  if (filter === 'active') return jobActive(job)
  return job?.status === filter
}
export function jobFilterCounts(jobs) {
  const out = {}
  for (const f of JOB_FILTERS) out[f] = jobs.filter(j => jobInFilter(j, f)).length
  return out
}

// The task filter of the task list: all, the ones that may run, the ones off.
export const TASK_FILTERS = ['all', 'on', 'off']
export function taskInFilter(task, filter) {
  if (filter === 'on') return task?.enabled !== false
  if (filter === 'off') return task?.enabled === false
  return true
}

// A search over what a person sees on a task row: name, description, prompt,
// model and the name of the agent that runs it.
export function taskMatches(task, query, agentName = '') {
  const q = String(query || '').trim().toLowerCase()
  if (!q) return true
  return [task?.name, task?.description, task?.prompt, task?.model, agentName]
    .some(v => String(v || '').toLowerCase().includes(q))
}

// What a person must fix before a save, as { field: message key }. The keys
// are under tasks.errors in the agents namespace.
export function taskErrors(form) {
  const out = {}
  for (const f of taskMissing(form)) out[f] = f === 'agent_id' ? 'agent' : f
  return out
}

// How a job started, as a key under tasks.startedBy (manual, api, cron), or ''
// for a value the page does not know.
export function startedBy(job) {
  const by = String(job?.triggered_by || '').toLowerCase()
  return ['manual', 'api', 'cron'].includes(by) ? by : ''
}

// The request that runs a task by name over the API. Its body is the values of
// the parameters the prompt asks for.
// `origin` is the address of this server as the browser sees it.
export function apiExample(task, origin = '') {
  const params = Object.fromEntries(promptParams(task?.prompt).map(p => [p, '...']))
  const body = JSON.stringify(params)
  return `curl -X POST ${origin}/api/agent/tasks/${encodeURIComponent(task?.name || '')}/execute \\\n  -H "Content-Type: application/json" \\\n  -d '${body}'`
}
