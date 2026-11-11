import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The background jobs of a chat above the message box, with the API stubbed by
// a small model of the server: a running job, one that exited well, one that
// exited with a code, a stopped and a killed one, one lost with its node, one
// that stalled, a job of a sub-agent and a wait on a job. The server side
// (registry, routing, limits) is tested in Go.

const AT = '2026-11-10T09:00:00Z'
const SHOTS = process.env.JOB_SHOTS || ''
const ago = (s) => new Date(Date.now() - s * 1000).toISOString()

const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Build it and run the tests.', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'I started the build and the tests in the background.', at: AT },
]

const job = (extra = {}) => ({
  id: 'job-k3m9qa', label: 'build', chat: 'c1', provider: 'builtin', state: 'running', exit: 0,
  started: ago(252), last_output: ago(3), bytes: 5300, ...extra,
})

const everyState = () => [
  job(),
  job({ id: 'job-stal1d', label: 'dev server', last_output: ago(1500), started: ago(3000) }),
  job({ id: 'job-sbx0aa', label: 'sandbox tests', provider: 'sandbox', started: ago(90), last_output: ago(1) }),
  job({ id: 'job-agent1', label: 'lint', agent: true, chat: 'sub1', started: ago(40), last_output: ago(8) }),
  job({ id: 'job-ok0000', label: 'download', state: 'exited', end: 'exit', exit: 0, started: ago(900), ended: ago(600), last_output: ago(600) }),
  job({ id: 'job-bad000', label: 'tests', state: 'exited', end: 'exit', exit: 2, started: ago(1800), ended: ago(1500) }),
  job({ id: 'job-kill00', label: 'watcher', state: 'killed', end: 'killed', exit: 137, started: ago(1500), ended: ago(1400) }),
  job({ id: 'job-stop00', label: 'server', state: 'killed', end: 'stopped', exit: 143, started: ago(1500), ended: ago(1300) }),
  job({ id: 'job-lost00', label: 'import', state: 'lost', exit: -1, started: ago(1200), ended: ago(1000) }),
]

const jobWait = (extra = {}) => ({
  id: 'w1abcd', prompt: 'Wait for job job-k3m9qa to exit', interval: '1m', interval_seconds: 60, state: 'active', runs: 0, running: false,
  kind: 'wait', created: AT,
  watch: {
    phase: 'waiting', reason: 'The tests have to pass before I deploy.', source_kind: 'job', source: 'job-k3m9qa "build"',
    mode: 'exit', condition: 'the job exits', checks: 4, last_check: ago(20), outcome: 'unchanged',
    next_check: new Date(Date.now() + 40000).toISOString(), deadline: new Date(Date.now() + 3500000).toISOString(), errors: 0, max_errors: 3, ...extra,
  },
})

async function serve(page, { jobs = [job()], loops = [] } = {}) {
  const s = { jobs: jobs.map(j => ({ ...j })), loops, calls: [] }
  await mockChat(page)
  const detail = () => ({
    id: 'c1', title: 'Build and test', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done',
    pending: [], loops: s.loops, jobs: s.jobs,
  })
  await page.route(/\/api\/chats\/c1\/agents(\?.*)?$/, route => route.fulfill({ json: { sub_agents: [], running: 0, background_limit: 4 } }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: HISTORY } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail() }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail(), pending: undefined, loops: undefined, jobs: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route(/\/api\/chats\/c1\/loops(\/[a-z0-9]+)?$/, route => route.fulfill({ json: { loops: s.loops, jobs: s.jobs } }))
  await page.route(/\/api\/chats\/c1\/jobs\/[a-z0-9-]+\/kill$/, (route) => {
    const id = new URL(route.request().url()).pathname.split('/').slice(-2, -1)[0]
    s.calls.push(`POST kill ${id}`)
    s.jobs = s.jobs.map(j => (j.id === id ? { ...j, state: 'killed', end: 'stopped', exit: 143, ended: new Date().toISOString() } : j))
    return route.fulfill({ json: s.jobs.find(j => j.id === id) })
  })
  return s
}

async function open(page, opts) {
  const s = await serve(page, opts)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-message').nth(1)).toBeVisible()
  return s
}

async function shot(page, name) {
  if (!SHOTS) return
  await page.getByTestId('chat-composer').scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${SHOTS}/${name}.png` })
}

for (const scheme of ['light', 'dark']) {
  for (const [name, size] of [['desktop', { width: 1440, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
    test.describe(`${name} ${scheme}`, () => {
      test.use({ viewport: size, colorScheme: scheme })
      test.beforeEach(async ({ page }) => {
        await page.addInitScript((mode) => { try { localStorage.setItem('localai-theme', mode) } catch { /* private window */ } }, scheme)
      })

      test('a running job shows its label, state, place and times, never a command, and Stop ends it', async ({ page }) => {
        const s = await open(page)
        const chip = page.getByTestId('job-chip')
        await expect(chip).toHaveAttribute('data-kind', 'running')
        await expect(chip.getByTestId('job-title')).toHaveText('build')
        await expect(chip.getByTestId('job-status')).toHaveText('Running')
        await expect(chip.getByTestId('job-where')).toHaveText('on this machine')
        await expect(chip.getByTestId('job-id')).toHaveText('job-k3m9qa')
        await expect(chip).toContainText('last output')
        await shot(page, `${name}-${scheme}-running`)
        await chip.getByTestId('job-stop').click()
        await expect(page.getByTestId('jobs-done')).toHaveCount(1)
        expect(s.calls).toEqual(['POST kill job-k3m9qa'])
      })

      test('every state reads differently in words: running, stalled, sandbox, sub-agent and the finished ones', async ({ page }) => {
        await open(page, { jobs: everyState() })
        const kinds = await page.getByTestId('job-chip').evaluateAll(els => els.map(e => e.dataset.kind))
        expect(kinds).toEqual(['running', 'stalled', 'running', 'running'])
        const stalled = page.locator('[data-job="job-stal1d"]')
        await expect(stalled.getByTestId('job-status')).toContainText('no output for')
        await expect(page.locator('[data-job="job-sbx0aa"]').getByTestId('job-where')).toHaveText('in isolation')
        await expect(page.locator('[data-job="job-agent1"]').getByTestId('job-agent')).toHaveText('started by a sub-agent')
        await shot(page, `${name}-${scheme}-live`)
        // The finished ones fold into one line, closed, and open on request.
        await expect(page.getByTestId('jobs-done')).toContainText('5 finished jobs')
        await expect(page.getByTestId('jobs-done-body')).toHaveCount(0)
        await page.getByTestId('jobs-done-toggle').click()
        const done = page.getByTestId('jobs-done-body')
        await expect(done.locator('[data-job="job-ok0000"]').getByTestId('job-status')).toHaveText('Exited, status 0')
        await expect(done.locator('[data-job="job-bad000"]').getByTestId('job-status')).toHaveText('Exited with status 2')
        await expect(done.locator('[data-job="job-kill00"]').getByTestId('job-status')).toHaveText('Killed')
        await expect(done.locator('[data-job="job-stop00"]').getByTestId('job-status')).toHaveText('Stopped by you')
        await expect(done.locator('[data-job="job-lost00"]').getByTestId('job-status')).toHaveText('Lost: its node is gone')
        await expect(done.getByTestId('job-stop')).toHaveCount(0)
        await page.getByTestId('jobs-done').scrollIntoViewIfNeeded()
        await shot(page, `${name}-${scheme}-finished`)
      })

      test('a wait on a job names the job, and a job that is gone ends the wait in red with its own words', async ({ page }) => {
        await open(page, { jobs: [job()], loops: [jobWait()] })
        const wait = page.getByTestId('wait-chip')
        await expect(wait.getByTestId('wait-source')).toHaveText('Watching job-k3m9qa "build"')
        await expect(wait.getByTestId('wait-until')).toContainText('the job exits')
        await shot(page, `${name}-${scheme}-wait`)
        await page.unroute('**/api/chats/c1')
        await page.route('**/api/chats/c1', route => route.fulfill({ json: {
          id: 'c1', title: 'Build and test', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [],
          jobs: [job({ state: 'lost', exit: -1, ended: ago(5) })],
          loops: [{ ...jobWait({ phase: 'ended', end: 'source_failing', errors: 3, last_error: 'the job is gone with its node' }), state: 'ended' }],
        } }))
        await page.reload()
        await expect(page.getByTestId('wait-title')).toHaveText('Stopped: the job is gone with its node')
        await shot(page, `${name}-${scheme}-wait-gone`)
      })
    })
  }
}
