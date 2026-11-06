// SPDX-License-Identifier: MIT
// The rules of sharing a saved agent that do not need React: who may share
// it, the reference rows of the Share dialog, how each choice becomes a
// grant on the thing itself, and the summary of what an agent needs.
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  agentShareEntry, canShareAgent, isSharedWithMe, referenceRows, applyReferenceChoices, needsSummary, AGENT_ROLES,
} from '../../src/utils/agentShare.js'

const t = (key, vars) => (vars ? `${key}:${JSON.stringify(vars)}` : key)

test('agent share: who may share, and the badge entry of a row', () => {
  const own = { id: 'a', access: { role: 'owner', sharedWith: 2, sharing: true, share_id: 'r1' } }
  const ownOff = { id: 'b', access: { role: 'owner', sharedWith: 0, sharing: false } }
  const shared = { id: 'c', access: { role: 'use', owner: 'Alice', everyone: true } }
  assert.equal(canShareAgent(own), true)
  assert.equal(canShareAgent(ownOff), false, 'local-owner mode or no policy: no Share action')
  assert.equal(canShareAgent(shared), false)
  assert.equal(canShareAgent({ id: 'old' }), false, 'a server without sharing sends no access')
  assert.equal(isSharedWithMe(shared), true)
  assert.equal(isSharedWithMe(own), false)
  assert.deepEqual(agentShareEntry(own), { role: 'owner', sharedWith: 2 })
  assert.deepEqual(agentShareEntry(shared), { role: 'use', owner: 'Alice', everyone: true })
  assert.equal(agentShareEntry(ownOff), null, 'a private agent on a server that shares nothing shows no line')
  assert.deepEqual(AGENT_ROLES, ['use', 'viewer'])
})

test('agent share: every reference is a row; only shareable ones ask for a choice', () => {
  const rows = referenceRows([
    { kind: 'collection', id: 'docs', label: 'docs', share: { kind: 'collection', resource_id: 'rc1', options: ['view', 'use'] } },
    { kind: 'connection', id: 'b1', addon_id: 'mail', label: 'mail connection 1', share: { kind: 'connection', resource_id: 'rk1', options: ['use'] } },
    { kind: 'addon-resource', id: 'rt1', addon_id: 'tables', label: 'tables resource 1', share: { kind: 'addon', resource_id: 'rt1', options: ['view'] } },
    { kind: 'folder', id: '12', label: 'Projects' },
    { kind: 'skill', id: 'review', label: 'review' },
  ], t)
  assert.equal(rows.length, 5)
  assert.deepEqual(rows[0], { id: 'collection:docs', label: 'docs', kind: 'agents:share.kind.collection', options: ['view', 'use', 'none'] })
  assert.deepEqual(rows[1].options, ['use', 'none'], 'a connection can only be use')
  assert.equal(rows[3].readOnly, true)
  assert.equal(rows[3].note, 'agents:share.ownAccess')
  assert.equal(rows[3].options, undefined)
  assert.deepEqual(referenceRows(null, t), [])
})

test('agent share: each choice becomes a grant on that thing for the same person', async () => {
  const calls = []
  const sharesApi = { setGrant: async (kind, id, principal, role) => { calls.push(['share', kind, id, principal, role]) } }
  const addonGrant = async (addonId, id, principal, role) => { calls.push(['addon', addonId, id, principal, role]) }
  const refs = [
    { kind: 'collection', id: 'docs', label: 'docs', share: { kind: 'collection', resource_id: 'rc1', options: ['view', 'use'] } },
    { kind: 'connection', id: 'b1', label: 'mail connection 1', share: { kind: 'connection', resource_id: 'rk1', options: ['use'] } },
    { kind: 'addon-resource', id: 'rt1', addon_id: 'tables', label: 'tables resource 1', share: { kind: 'addon', resource_id: 'rt1', options: ['view', 'use'] } },
    { kind: 'folder', id: '12', label: 'Projects' },
  ]
  const out = await applyReferenceChoices({
    principal: 'u-bob', references: refs, sharesApi, addonGrant,
    choices: { 'collection:docs': 'view', 'connection:b1': 'none', 'addon-resource:tables:rt1': 'use', 'folder:12': 'use' },
  })
  assert.deepEqual(calls, [
    ['share', 'collection', 'rc1', 'u-bob', 'viewer'],
    ['addon', 'tables', 'rt1', 'u-bob', 'editor'],
  ])
  assert.deepEqual(out, { applied: 2, failed: [], skipped: [] })
})

test('agent share: a failed or impossible grant is reported, never hidden', async () => {
  const sharesApi = { setGrant: async () => { throw new Error('denied') } }
  const addonGrant = async () => { throw new Error('should not be called for everyone') }
  const refs = [
    { kind: 'collection', id: 'docs', label: 'docs', share: { kind: 'collection', resource_id: 'rc1', options: ['view'] } },
    { kind: 'addon-resource', id: 'rt1', addon_id: 'tables', label: 'tables resource 1', share: { kind: 'addon', resource_id: 'rt1', options: ['view'] } },
  ]
  const out = await applyReferenceChoices({
    principal: '@everyone', references: refs, sharesApi, addonGrant,
    choices: { 'collection:docs': 'view', 'addon-resource:tables:rt1': 'view' },
  })
  assert.deepEqual(out, { applied: 0, failed: ['docs'], skipped: ['tables resource 1'] })
})

test('agent share: the needs summary counts what is missing', () => {
  const s = needsSummary([
    { kind: 'addon', label: 'office', state: 'ok' },
    { kind: 'collection', label: 'docs', state: 'missing' },
    { kind: 'addon', label: 'mail', state: 'unavailable' },
    { kind: 'weird', label: 'x', state: 'other' },
  ])
  assert.equal(s.total, 3)
  assert.equal(s.ok, 1)
  assert.deepEqual(s.lacking.map(n => n.label), ['docs', 'mail'])
  assert.equal(needsSummary(undefined).total, 0)
})
