// Stubbed API of the agent tasks and their jobs (/api/agent/tasks and
// /api/agent/jobs), for the specs of the agent tasks page. Every write is
// recorded in `s.calls` so a spec can assert what the page sent.

const iso = (m) => new Date(Date.now() - m * 60_000).toISOString()

export const TASKS = [
  { id: 'k1', name: 'Summarise a support ticket', description: 'Reads a ticket and writes a three-line summary for the on-call channel.', model: 'qwen3-8b', prompt: 'Summarise ticket {{.ticket}} in three lines. Say who should act next.', enabled: true, created_at: iso(9000), updated_at: iso(600), webhooks: [{ url: 'https://hooks.example/oncall', method: 'POST' }] },
  { id: 'k2', name: 'Translate release notes', description: '', model: '', agent_id: 'a4', prompt: 'Translate the release notes of version {{.version}} into {{.language}}. Keep the headings and the links as they are.', enabled: true, created_at: iso(9000), updated_at: iso(600) },
  { id: 'k3', name: 'Nightly test report', description: 'Turns the CI log into a short list of failing tests and the likely cause of each.', model: 'phi-4-mini', prompt: 'Read the CI log and list the failing tests.', enabled: false, created_at: iso(9000), updated_at: iso(600) },
  { id: 'k4', name: 'Draft a reply', description: 'Writes a polite first reply to a customer email.', model: 'gemma-3-12b', prompt: 'Draft a reply to: {{.email}}', enabled: true, created_at: iso(9000), updated_at: iso(600) },
]
const job = (id, task, status, mins, extra = {}) => ({
  id, task_id: task, status, parameters: {}, created_at: iso(mins), triggered_by: 'manual',
  ...(status !== 'pending' ? { started_at: iso(mins) } : {}),
  ...(['completed', 'failed', 'cancelled'].includes(status) ? { completed_at: iso(mins - 0.6) } : {}),
  ...extra,
})
export const JOBS = [
  job('j1', 'k1', 'running', 0.5, { parameters: { ticket: 'SUP-4821' }, triggered_by: 'api' }),
  job('j2', 'k4', 'pending', 0.2, { parameters: { email: 'Hi, my order has not arrived yet.' } }),
  job('j3', 'k1', 'completed', 42, { parameters: { ticket: 'SUP-4817' }, result: 'Login fails for SSO users after the 2.4 upgrade.\nWorkaround: clear the session cookie.\nNext: the identity team should check the callback URL.', webhook_sent: true, traces: [{ type: 'reasoning', content: 'The ticket names the 2.4 upgrade.' }, { type: 'tool_call', tool_name: 'search_tickets', content: 'query=SSO login' }] }),
  job('j4', 'k2', 'failed', 130, { parameters: { version: '4.2.0', language: 'Italian' }, error: 'model "phi-4-mini" is not loaded and could not be started: out of memory (needs 6.1 GB, 4.0 GB free)', triggered_by: 'api' }),
  job('j5', 'k1', 'cancelled', 300, { parameters: { ticket: 'SUP-4790' } }),
  job('j6', 'k4', 'completed', 1500, { parameters: { email: 'Can I change the delivery address?' }, result: 'Hello, and thank you for writing. Yes, you can change the address until the parcel ships. Reply with the new address and we update it today.', webhook_error: 'POST https://hooks.example/crm answered 502' }),
]
for (let i = 0; i < 14; i++) JOBS.push(job(`jh${i}`, i % 2 ? 'k1' : 'k4', i % 5 === 2 ? 'failed' : 'completed', 2000 + i * 600, { parameters: { ticket: `SUP-47${i}` }, result: 'Done.', error: i % 5 === 2 ? 'context window exceeded (8192 tokens)' : undefined }))

export async function installTasks(page, { tasks = TASKS, jobs = JOBS, fail = false } = {}) {
  const s = { tasks: structuredClone(tasks), jobs: structuredClone(jobs), calls: [] }
  await page.route(/\/api\/agent\/tasks(\?.*)?$/, route => {
    const req = route.request()
    if (fail) return route.fulfill({ status: 500, json: { error: { message: 'database is locked' } } })
    if (req.method() === 'POST') { const b = req.postDataJSON(); const t = { id: `k${s.tasks.length + 9}`, ...b }; s.tasks.push(t); s.calls.push({ m: 'POST', b }); return route.fulfill({ status: 201, json: { id: t.id } }) }
    return route.fulfill({ json: s.tasks })
  })
  await page.route(/\/api\/agent\/tasks\/[^/?]+$/, route => {
    const req = route.request(); const id = new URL(req.url()).pathname.split('/').pop()
    if (req.method() === 'DELETE') { s.tasks = s.tasks.filter(t => t.id !== id); return route.fulfill({ json: { message: 'ok' } }) }
    if (req.method() === 'PUT') { const b = req.postDataJSON(); Object.assign(s.tasks.find(t => t.id === id), b); s.calls.push({ m: 'PUT', id, b }); return route.fulfill({ json: { message: 'ok' } }) }
    return route.fulfill({ json: s.tasks.find(t => t.id === id) })
  })
  await page.route(/\/api\/agent\/jobs\/[^/?]+\/(cancel|rerun)$/, route => { const parts = new URL(route.request().url()).pathname.split('/'); s.calls.push({ m: parts.pop(), id: parts.pop() }); return route.fulfill({ json: { message: 'ok' } }) })
  await page.route(/\/api\/agent\/jobs\/[^/?]+$/, route => { const id = new URL(route.request().url()).pathname.split('/').pop(); if (route.request().method() === 'DELETE') { s.jobs = s.jobs.filter(j => j.id !== id); return route.fulfill({ json: { message: 'ok' } }) } return route.fulfill({ json: s.jobs.find(j => j.id === id) }) })
  await page.route(/\/api\/agent\/jobs(\?.*)?$/, route => {
    if (fail) return route.fulfill({ status: 500, json: { error: { message: 'database is locked' } } })
    const u = new URL(route.request().url()); const tid = u.searchParams.get('task_id')
    return route.fulfill({ json: tid ? s.jobs.filter(j => j.task_id === tid) : s.jobs })
  })
  // Registered last: Playwright tries the newest route first, and the job route above also matches /execute.
  await page.route(/\/api\/agent\/jobs\/execute$/, route => { const b = route.request().postDataJSON(); s.calls.push({ m: 'RUN', b }); const j = job(`jn${s.jobs.length}`, b.task_id, 'pending', 0, { parameters: b.parameters }); s.jobs.unshift(j); return route.fulfill({ status: 201, json: { job_id: j.id, status: 'pending' } }) })
  return s
}
