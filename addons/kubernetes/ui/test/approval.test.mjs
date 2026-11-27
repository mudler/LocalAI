import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { ASK, SKIP, TOOLS, declared, keyOf, valuesOf } from '../src/approval.js'

const manifest = JSON.parse(readFileSync(new URL('../../manifest.json', import.meta.url), 'utf8'))

test('kubernetes approval: lists the ten write tools, as the manifest declares their settings', () => {
  assert.deepEqual(TOOLS, ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec'])
  const caps = manifest.capabilities
  assert.deepEqual(caps.toolDefinitions.filter((d) => d.approvalOverridable).map((d) => d.name), TOOLS)
  for (const tool of TOOLS) {
    const s = caps.userSettings.find((x) => x.key === keyOf(tool))
    assert.deepEqual(s.values, [ASK, SKIP], tool)
    assert.equal(s.default, ASK, tool)
  }
})

test('kubernetes approval: an unknown or missing answer reads as ask', () => {
  const all = Object.fromEntries(TOOLS.map((t) => [t, ASK]))
  assert.deepEqual(valuesOf(undefined), all)
  assert.deepEqual(valuesOf({ settings: [{ key: 'approval-scale', value: 'skip' }, { key: 'approval-exec', value: 'always' }] }), { ...all, scale: SKIP })
  assert.equal(declared({ settings: [{ key: 'approval-drain', value: 'ask' }] }), true)
  assert.equal(declared({ settings: [] }), false)
})
