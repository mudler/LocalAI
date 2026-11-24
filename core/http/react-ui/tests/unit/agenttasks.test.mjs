import test from 'node:test'
import assert from 'node:assert/strict'
import {
  apiExample, jobActive, jobFilterCounts, jobInFilter, jobState, jobsOfTask, promptParams, startedBy, taskBody, taskErrors, taskForm,
  taskInFilter, taskMatches, taskMissing, withOwners,
} from '../../src/utils/agentTasks.js'

test('agent tasks: the parameters of a prompt template are found once each, in order', () => {
  assert.deepEqual(promptParams('Hi {{.who}}, see {{ .what }} and {{.who}} {{- .x -}}'), ['who', 'what', 'x'])
  assert.deepEqual(promptParams('no params {{ if .a }}'), [])
})

test('agent tasks: a job status maps to the Agents area state, and pending and running are active', () => {
  assert.equal(jobState('completed'), 'done')
  assert.equal(jobState('cancelled'), 'stopped')
  assert.equal(jobState('pending'), 'running')
  assert.equal(jobActive({ status: 'pending' }), true)
  assert.equal(jobActive({ status: 'failed' }), false)
})

test('agent tasks: an update sends back what the form does not edit and names one runner', () => {
  const task = { id: 't1', name: 'a', prompt: 'p', model: 'm', created_at: 'x', updated_at: 'y', webhooks: [{ url: 'https://example.org', headers: { Authorization: '[hidden]' } }] }
  const form = { ...taskForm(task), runner: 'agent', agent_id: 'ag1', name: ' b ' }
  const body = taskBody(form, task)
  assert.equal(body.name, 'b')
  assert.equal(body.model, '')
  assert.equal(body.agent_id, 'ag1')
  assert.deepEqual(body.webhooks, task.webhooks)
  assert.equal('id' in body || 'created_at' in body, false)
  assert.deepEqual(taskMissing(taskForm(null)), ['name', 'prompt', 'model'])
})

test('agent tasks: the administrator listing keeps the owner of the other users\' items', () => {
  assert.deepEqual(withOwners([{ id: 1 }], 'tasks'), [{ owner: '', item: { id: 1 } }])
  assert.deepEqual(withOwners({ tasks: [{ id: 1 }], user_groups: { bob: { tasks: [{ id: 2 }] } } }, 'tasks'), [{ owner: '', item: { id: 1 } }, { owner: 'bob', item: { id: 2 } }])
})

test('agent tasks: the run history groups waiting and running jobs as in progress', () => {
  const jobs = [{ status: 'pending' }, { status: 'running' }, { status: 'completed' }, { status: 'failed' }, { status: 'failed' }]
  assert.deepEqual(jobFilterCounts(jobs), { all: 5, active: 2, completed: 1, failed: 2, cancelled: 0 })
  assert.equal(jobInFilter({ status: 'pending' }, 'active'), true)
  assert.equal(jobInFilter({ status: 'cancelled' }, 'failed'), false)
})

test('agent tasks: the jobs of a task are newest first', () => {
  const jobs = [{ id: 'a', task_id: 't', created_at: '2026-10-01T10:00:00Z' }, { id: 'b', task_id: 't', created_at: '2026-10-02T10:00:00Z' }, { id: 'c', task_id: 'u', created_at: '2026-10-03T10:00:00Z' }]
  assert.deepEqual(jobsOfTask(jobs, 't').map(j => j.id), ['b', 'a'])
})

test('agent tasks: the list filters by on and off and searches what a row shows', () => {
  assert.equal(taskInFilter({ enabled: false }, 'off'), true)
  assert.equal(taskInFilter({}, 'on'), true)
  assert.equal(taskInFilter({}, 'off'), false)
  const task = { name: 'Digest', description: '', prompt: 'Summarise {{.x}}', model: 'qwen3-8b' }
  assert.equal(taskMatches(task, 'QWEN'), true)
  assert.equal(taskMatches(task, 'helper', 'release-helper'), true)
  assert.equal(taskMatches(task, 'nope'), false)
  assert.equal(taskMatches(task, '  '), true)
})

test('agent tasks: a save names each missing field; the start of a job is said in known words', () => {
  assert.deepEqual(taskErrors({ ...taskForm(null), runner: 'agent' }), { name: 'name', prompt: 'prompt', agent_id: 'agent' })
  assert.deepEqual(taskErrors({ ...taskForm(null), name: 'a', prompt: 'p', model: 'm' }), {})
  assert.equal(startedBy({ triggered_by: 'API' }), 'api')
  assert.equal(startedBy({ triggered_by: 'webhook' }), '')
})

test('agent tasks: the API example runs the task by name with its parameters as the body', () => {
  const out = apiExample({ name: 'my task', prompt: 'Hi {{.who}}' }, 'http://host')
  assert.match(out, /POST http:\/\/host\/api\/agent\/tasks\/my%20task\/execute/)
  assert.match(out, /-d '\{"who":"\.\.\."\}'/)
})
