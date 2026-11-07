import test from 'node:test'
import assert from 'node:assert/strict'
import { ASK, SKIP, TOOLS, declared, keyOf, valuesOf, approvalRevision, approvalsChanged, onApprovalsChanged } from '../src/approval.js'
import { approvalState } from '../../../internal/connector-ui/src/approvals.js'
import { readFileSync } from 'node:fs'

const manifest = JSON.parse(readFileSync(new URL('../../manifest.json', import.meta.url), 'utf8'))

test('pikvm approval: only power actions and key presses can stop asking, typed text never', () => {
  assert.deepEqual(TOOLS, ['power-action', 'send-key'])
  assert.equal(TOOLS.includes('type-text'), false)
  assert.equal(keyOf('power-action'), 'approval-power-action')
})

test('pikvm approval: the page names the settings the manifest declares, and only for the tools it marks', () => {
  const caps = manifest.capabilities
  const overridable = caps.toolDefinitions.filter((d) => d.approvalOverridable).map((d) => d.name)
  assert.deepEqual(overridable, TOOLS)
  for (const d of caps.toolDefinitions) assert.equal(d.approvalOverridable === true, TOOLS.includes(d.name), d.name)
  for (const tool of TOOLS) {
    const s = caps.userSettings.find((x) => x.key === keyOf(tool))
    assert.deepEqual(s.values, [ASK, SKIP], tool)
    assert.equal(s.default, ASK, tool)
  }
  assert.equal(caps.userSettings.some((x) => x.key === keyOf('type-text')), false)
})

test('pikvm approval: an answer the host does not report or does not know reads as ask', () => {
  assert.deepEqual(valuesOf(undefined), { 'power-action': ASK, 'send-key': ASK })
  assert.deepEqual(valuesOf({ settings: [{ key: 'approval-power-action', value: 'skip' }, { key: 'approval-send-key', value: 'always' }] }), { 'power-action': SKIP, 'send-key': ASK })
  assert.deepEqual(valuesOf({ settings: [{ key: 'approval-type-text', value: 'skip' }] }), { 'power-action': ASK, 'send-key': ASK })
})

test('pikvm approval: a package that declares no such setting shows nothing', () => {
  assert.equal(declared({ settings: [{ key: 'other', value: 'x' }] }), false)
  assert.equal(declared({ settings: [{ key: 'approval-send-key', value: 'ask' }] }), true)
  assert.equal(declared(null), false)
})

test('pikvm approval: a save tells the rows of the connections to read the view again', () => {
  let calls = 0
  const off = onApprovalsChanged(() => { calls += 1 })
  const before = approvalRevision()
  approvalsChanged()
  assert.equal(approvalRevision(), before + 1)
  assert.equal(calls, 1)
  off()
  approvalsChanged()
  assert.equal(calls, 1)
})

test('approvals view: the owner view says asks, runs or idle, from what the host reported', () => {
  assert.equal(approvalState({ tool: 'send-key', skip: false }), 'asks')
  assert.equal(approvalState(undefined), 'asks')
  assert.equal(approvalState({ tool: 'send-key', skip: true, runs_without_asking: ['owner', 'paired'] }), 'runs')
  assert.equal(approvalState({ tool: 'send-key', skip: true }), 'idle')
  assert.equal(approvalState({ tool: 'send-key', skip: true, runs_without_asking: [] }), 'idle')
})

test('approvals view: only a paired sender skips the card; a listed sender, anyone and a role are still asked', () => {
  const a = { tool: 'power-action', skip: true, runs_without_asking: ['owner', 'paired'] }
  assert.equal(approvalState(a, 'paired'), 'runs')
  for (const sender of ['listed', 'anyone', 'role:member', 'channel_member']) assert.equal(approvalState(a, sender), 'other', sender)
  assert.equal(approvalState({ tool: 'power-action', skip: false }, 'paired'), 'asks')
})
