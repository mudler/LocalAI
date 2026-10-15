// Shared fixtures for the Jobs specs: tasks and jobs the API would return, the
// stubbed agent jobs endpoints, and the requests the page sent.

const MIN = 60 * 1000
const HOUR = 60 * MIN

// Times are built from the start of today, so "today" and "yesterday" mean the
// same thing whenever the suite runs.
function clockTimes() {
  const now = Date.now()
  const d = new Date(now)
  const midnight = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const sinceMidnight = now - midnight
  const todayAt = (ms) => new Date(Math.max(midnight + 1000, now - Math.min(ms, sinceMidnight - 1000))).toISOString()
  const dayAt = (daysAgo, hour) => new Date(midnight - (daysAgo - 1) * 24 * HOUR - (24 - hour) * HOUR).toISOString()
  return { todayAt, dayAt, now }
}

export function fixtureData() {
  const { todayAt, dayAt } = clockTimes()
  const tasks = [
    {
      id: 't1', name: 'daily-digest', description: 'Summarise overnight news.', model: 'qwen3-8b-instruct',
      prompt: 'Summarise overnight news about {{.topic}} in {{.format}} format. Keep it to {{.items}} items.',
      context: 'Audience: engineers who run models on their own hardware.',
      enabled: true, cron: '0 7 * * *', cron_parameters: { topic: 'local inference', format: 'bullet points', items: '8' },
      webhooks: [{ url: 'https://hooks.example.org/digest', method: 'POST', headers: { 'X-Token': 'abc' }, payload_template: '{"text": "{{.Status}}"}' }],
      multimedia_sources: [],
    },
    {
      id: 't2', name: 'repo-triage', description: 'Label and route new issues.', model: 'qwen3-coder-30b',
      prompt: 'Read the new issues in example/app and label each one.', context: '',
      enabled: true, cron: '*/30 * * * *', webhooks: [], multimedia_sources: [{ type: 'image', url: 'https://example.org/board.png', headers: {} }],
    },
    {
      id: 't3', name: 'weekly-report', description: 'Write the Monday status report.', model: 'qwen3-14b-instruct',
      prompt: 'Write the status report for the week.', context: '', enabled: true, cron: '0 9 * * 1', webhooks: [], multimedia_sources: [],
    },
    {
      id: 't4', name: 'ad-hoc-research', description: '', model: 'qwen3-14b-instruct',
      prompt: 'Research {{.subject}}.', context: '', enabled: false, cron: '', webhooks: [], multimedia_sources: [],
    },
  ]
  const jobs = [
    {
      id: 'job-fail-0001', task_id: 't1', status: 'failed', triggered_by: 'cron',
      parameters: { topic: 'local inference', format: 'bullet points', items: '8' },
      error: 'read_page: timeout after 20 s (3 of 3 attempts)',
      created_at: todayAt(10 * MIN), started_at: todayAt(10 * MIN), completed_at: new Date(Date.parse(todayAt(10 * MIN)) + 66 * 1000).toISOString(),
      traces: [
        { type: 'status', content: 'Started by the schedule', timestamp: todayAt(10 * MIN) },
        { type: 'tool_call', tool_name: 'web_search', content: 'Searching for local inference news', arguments: { query: 'local inference' }, timestamp: todayAt(10 * MIN) },
        { type: 'tool_result', tool_name: 'read_page', content: 'timeout after 20 s', timestamp: todayAt(10 * MIN) },
      ],
    },
    {
      id: 'job-done-0002', task_id: 't2', status: 'completed', triggered_by: 'cron', parameters: {},
      result: 'Labelled 12 issues and routed 3 of them to the person on call.\n\n| Issue | Label |\n| - | - |\n| #1204 | bug |\n| #1203 | question |',
      created_at: todayAt(40 * MIN), started_at: todayAt(40 * MIN), completed_at: new Date(Date.parse(todayAt(40 * MIN)) + 52 * 1000).toISOString(),
      webhook_sent: true, webhook_sent_at: todayAt(39 * MIN),
      traces: [
        { type: 'reasoning', content: 'I need to read the new issues first.', timestamp: todayAt(40 * MIN) },
        { type: 'tool_call', tool_name: 'list_issues', content: 'Listing issues', arguments: { repo: 'example/app' }, timestamp: todayAt(40 * MIN) },
      ],
    },
    {
      id: 'job-run-0003', task_id: 't2', status: 'running', triggered_by: 'manual', parameters: {},
      created_at: todayAt(2 * MIN), started_at: todayAt(2 * MIN),
    },
    {
      id: 'job-done-0004', task_id: 't1', status: 'completed', triggered_by: 'cron',
      parameters: { topic: 'local inference', format: 'bullet points', items: '8' },
      result: 'Wrote the digest, 7 items, saved.',
      created_at: dayAt(1, 7), started_at: dayAt(1, 7), completed_at: new Date(Date.parse(dayAt(1, 7)) + 49 * 1000).toISOString(),
    },
    {
      id: 'job-stop-0005', task_id: 't3', status: 'cancelled', triggered_by: 'api', parameters: {},
      created_at: dayAt(3, 9), started_at: dayAt(3, 9),
    },
  ]
  return { tasks, jobs }
}

// Stub the models and the agent jobs endpoints. Returns { seen, state }: seen
// lists what the page sent, state is the tasks and jobs the stub serves.
export async function mockJobs(page, {
  tasks, jobs, mcp = true, models = ['qwen3-8b-instruct', 'qwen3-coder-30b', 'qwen3-14b-instruct'],
  updateStatus = 200, newJobId = 'job-new-0006',
} = {}) {
  const data = fixtureData()
  const state = { tasks: structuredClone(tasks ?? data.tasks), jobs: structuredClone(jobs ?? data.jobs) }
  const seen = { puts: [], posts: [], deletes: [], executes: [], jobExecutes: [], cancels: [] }

  await page.route('**/api/features', route => route.fulfill({
    json: { distributed: false, localai_assistant: true, agents: true, mcp: true, mcp_jobs: true },
  }))
  await page.route('**/api/models/capabilities', route => route.fulfill({
    json: { data: models.map(id => ({ id, capabilities: ['chat'] })) },
  }))
  await page.route('**/api/models/config-json/**', route => route.fulfill({
    json: mcp ? { mcp: { stdio: { mcpServers: {} } } } : {},
  }))
  await page.route('**/api/agent/**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const parts = url.pathname.split('/').filter(Boolean) // api agent tasks|jobs ...
    const kind = parts[2]
    const method = req.method()
    if (kind === 'tasks') {
      const rest = parts.slice(3)
      if (rest.length === 0) {
        if (method === 'POST') { seen.posts.push(req.postDataJSON()); return route.fulfill({ status: 201, json: { id: 't-new' } }) }
        return route.fulfill({ json: state.tasks })
      }
      if (rest[1] === 'execute' && method === 'POST') {
        let body = {}
        try { body = req.postDataJSON() || {} } catch { body = {} }
        seen.executes.push({ name: decodeURIComponent(rest[0]), body })
        return route.fulfill({ status: 201, json: { job_id: newJobId, status: 'pending', url: '' } })
      }
      const id = decodeURIComponent(rest[0])
      const task = state.tasks.find(t => t.id === id || t.name === id)
      if (method === 'GET') return task ? route.fulfill({ json: task }) : route.fulfill({ status: 404, json: { error: 'task not found' } })
      if (method === 'PUT') {
        const body = req.postDataJSON()
        seen.puts.push({ id, body })
        if (updateStatus !== 200) return route.fulfill({ status: updateStatus, json: { error: 'could not update' } })
        state.tasks = state.tasks.map(t => (t.id === id ? { ...t, ...body, id } : t))
        return route.fulfill({ json: { message: 'Task updated' } })
      }
      if (method === 'DELETE') {
        seen.deletes.push(id)
        state.tasks = state.tasks.filter(t => t.id !== id)
        return route.fulfill({ json: { message: 'Task deleted' } })
      }
    }
    if (kind === 'jobs') {
      const rest = parts.slice(3)
      if (rest.length === 0) return route.fulfill({ json: state.jobs })
      if (rest[0] === 'execute' && method === 'POST') {
        seen.jobExecutes.push(req.postDataJSON())
        return route.fulfill({ status: 201, json: { job_id: newJobId, status: 'pending', url: '' } })
      }
      const id = decodeURIComponent(rest[0])
      if (rest[1] === 'cancel' && method === 'POST') {
        seen.cancels.push(id)
        state.jobs = state.jobs.map(j => (j.id === id ? { ...j, status: 'cancelled' } : j))
        return route.fulfill({ json: { message: 'Job cancelled' } })
      }
      const job = state.jobs.find(j => j.id === id)
      if (id === newJobId && !job) {
        return route.fulfill({ json: { id: newJobId, task_id: 't1', status: 'pending', triggered_by: 'api', parameters: {}, created_at: new Date().toISOString() } })
      }
      return job ? route.fulfill({ json: job }) : route.fulfill({ status: 404, json: { error: 'job not found' } })
    }
    return route.fallback()
  })
  return { seen, state }
}
