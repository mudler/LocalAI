import test from 'node:test'
import assert from 'node:assert/strict'
import { jobActive, jobState, promptParams, taskBody, taskForm, taskMissing, withOwners } from '../../src/utils/agentTasks.js'

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
