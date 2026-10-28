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
