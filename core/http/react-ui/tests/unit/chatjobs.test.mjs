import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import test from 'node:test'

import i18next from 'i18next'

import { jobView, splitJobs, waitView } from '../../src/utils/chatLoops.js'

const en = JSON.parse(readFileSync(new URL('../../public/locales/en/chats.json', import.meta.url), 'utf8'))
const i18n = i18next.createInstance()
await i18n.init({ lng: 'en', resources: { en: { chats: en } }, ns: ['chats'], defaultNS: 'chats', interpolation: { escapeValue: false } })
const t = i18n.getFixedT('en', 'chats')

const NOW = Date.parse('2026-11-10T10:00:00Z')
const at = (s) => new Date(NOW + s * 1000).toISOString()
const job = (extra = {}) => ({ id: 'job-ab12cd', label: 'build', chat: 'c1', provider: 'builtin', state: 'running', exit: 0, started: at(-252), last_output: at(-3), ...extra })

test('job view: a running job says so, with its run time and last output', () => {
  const v = jobView(t, job(), NOW)
  assert.equal(v.kind, 'running')
  assert.equal(v.tone, 'run')
  assert.equal(v.live, true)
  assert.equal(v.title, 'build')
  assert.equal(v.status, 'Running')
  assert.deepEqual(v.facts.map(f => f.text), ['on this machine', 'running 4 min', 'last output 3 s ago'])
})

test('job view: a running job that wrote nothing for ten minutes is stalled, in words', () => {
  const v = jobView(t, job({ last_output: at(-1500) }), NOW)
  assert.equal(v.kind, 'stalled')
  assert.equal(v.tone, 'warn')
  assert.equal(v.status, 'Running, no output for 25 min')
  assert.equal(v.live, true)
})

test('job view: a job whose last output is not known is never shown stalled', () => {
  const v = jobView(t, job({ last_output: undefined, started: at(-7200) }), NOW)
  assert.equal(v.kind, 'running')
  assert.equal(v.status, 'Running')
  assert.ok(!v.facts.some(f => f.key === 'output'))
})

test('job view: exit 0 is ok, a code is an error and states its code', () => {
  const ok = jobView(t, job({ state: 'exited', end: 'exit', exit: 0, ended: at(-60) }), NOW)
  assert.equal(ok.kind, 'ok')
  assert.equal(ok.tone, 'ok')
  assert.equal(ok.live, false)
  assert.equal(ok.status, 'Exited, status 0')
  const bad = jobView(t, job({ state: 'exited', end: 'exit', exit: 2, ended: at(-60) }), NOW)
  assert.equal(bad.kind, 'failed')
  assert.equal(bad.tone, 'error')
  assert.equal(bad.status, 'Exited with status 2')
  assert.ok(bad.facts.some(f => f.text === 'ran for 3 min'))
})

test('job view: killed, stopped by the person, lost and ended by a condition each read differently', () => {
  assert.equal(jobView(t, job({ state: 'killed', end: 'killed', exit: 137, ended: at(-5) }), NOW).status, 'Killed')
  assert.equal(jobView(t, job({ state: 'killed', end: 'stopped', exit: 143, ended: at(-5) }), NOW).status, 'Stopped by you')
  const lost = jobView(t, job({ state: 'lost', exit: -1, ended: at(-5) }), NOW)
  assert.equal(lost.kind, 'lost')
  assert.equal(lost.tone, 'warn')
  assert.equal(lost.status, 'Lost: its node is gone')
  const gone = jobView(t, job({ state: 'exited', end: 'chat_deleted', exit: 143, ended: at(-5) }), NOW)
  assert.equal(gone.status, 'Ended: the chat was deleted')
  assert.equal(jobView(t, job({ state: 'failed', end: 'start_failed', exit: -1 }), NOW).status, 'Could not start')
})

test('job view: a job of a sub-agent and of the sandbox say so', () => {
  const v = jobView(t, job({ provider: 'sandbox', agent: true }), NOW)
  assert.equal(v.facts[0].text, 'in isolation')
  assert.ok(v.facts.some(f => f.key === 'agent'))
})

test('job view: the command never appears, whatever the job carries', () => {
  const v = jobView(t, job({ command: 'rm -rf secret' }), NOW)
  assert.ok(!JSON.stringify(v).includes('secret'))
})

test('split jobs: running ones apart from finished ones', () => {
  const { live, done } = splitJobs([job(), job({ id: 'job-2', state: 'exited' }), job({ id: 'job-3', state: 'ending' }), job({ id: 'job-4', state: 'lost' })])
  assert.deepEqual(live.map(j => j.id), ['job-ab12cd', 'job-3'])
  assert.deepEqual(done.map(j => j.id), ['job-2', 'job-4'])
  assert.deepEqual(splitJobs(undefined), { live: [], done: [] })
})

test('a wait on a job names the job, and a job gone with its node is not a failing source', () => {
  const wait = (watch) => ({ id: 'w1', state: 'active', kind: 'wait', created: at(-600), watch: { phase: 'waiting', source_kind: 'job', source: 'job-ab12cd "build"', mode: 'exit', condition: 'the job exits', checks: 1, deadline: at(3000), errors: 0, max_errors: 3, ...watch } })
  const w = waitView(t, wait({}), NOW)
  assert.equal(w.facts[0].text, 'Watching job-ab12cd "build"')
  const gone = waitView(t, { ...wait({ phase: 'ended', end: 'source_failing', errors: 3 }), state: 'ended' }, NOW)
  assert.equal(gone.title, 'Stopped: the job is gone with its node')
  assert.equal(gone.tone, 'error')
})

test('every locale has the job strings', () => {
  const dir = new URL('../../public/locales/', import.meta.url)
  const keys = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? keys(v, `${p}${k}.`) : [`${p}${k}`]))
  const want = keys(en.loops.job).sort()
  for (const loc of readdirSync(dir)) {
    const d = JSON.parse(readFileSync(new URL(`${loc}/chats.json`, dir), 'utf8'))
    assert.deepEqual(keys(d.loops.job).sort(), want, loc)
    assert.ok(d.loops.wait.sources.job && d.loops.wait.end.jobGone, loc)
    if (loc !== 'en') assert.notEqual(d.loops.job.status.lost, en.loops.job.status.lost, `${loc} is not an English copy`)
  }
})
