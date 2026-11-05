import test from 'node:test'
import assert from 'node:assert/strict'
import {
  MAX_CHANNELS, canSave, channelsProblem, connectorForm, connectorPayload, emptyConnector, grantableFor, hasNoCaps, roleHas, setCap, setGrant, usualCaps,
  AGENT_GRANT_CAPS, unpairedSensitive, channelOnly, DEFAULT_CONNECTOR_GRANTS, agentHas, agentHeld, copyFromAgent, presetBlock, usualFor, withAddedCaps, withCopiedCaps,
} from '../../../internal/connector-ui/src/connector.js'

const CAPS = [
  { name: 'memory.write', title: 'Save to memory', description: 'x', sensitive: false, usual: true },
  { name: 'talkback.channels', title: 'Post', description: 'x', sensitive: false, usual: true },
  { name: 'tools.write', title: 'Tools', description: 'x', sensitive: false, usual: false },
  { name: 'profile.write', title: 'Profile', description: 'x', sensitive: true, usual: false },
]
const KIND = { roles: [{ id: 'admin', title: 'Admin' }], permissions: [{ name: 'memory.write', sensitive: false }, { name: 'profile.write', sensitive: true }, { name: 'talkback.channels', sensitive: false }] }

test('permissions: a new binding has no caps, grants or channels', () => {
  const c = emptyConnector()
  assert.deepEqual([c.caps, c.grants, c.channels], [[], [], []])
  assert.equal(hasNoCaps(c), true)
  assert.equal(hasNoCaps({ caps: ['memory.write'] }), false)
  assert.equal(hasNoCaps({}), true, 'a binding from an older host has no caps field')
})

test('permissions: the usual preset is the non-sensitive caps the host marks, and never a sensitive one', () => {
  assert.deepEqual(usualCaps(CAPS), ['memory.write', 'talkback.channels'])
  assert.deepEqual(usualCaps([{ name: 'profile.write', sensitive: true, usual: true }]), [], 'a sensitive cap is never usual, whatever the list says')
  assert.deepEqual(usualCaps(undefined), [])
})

test('permissions: turning a cap off removes it from every role grant and drops empty grants', () => {
  let f = { caps: ['memory.write', 'talkback.channels'], grants: [{ role: 'admin', caps: ['memory.write', 'talkback.channels'] }, { role: 'x', caps: ['memory.write'] }] }
  f = setCap(f, 'memory.write', false)
  assert.deepEqual(f.caps, ['talkback.channels'])
  assert.deepEqual(f.grants, [{ role: 'admin', caps: ['talkback.channels'] }])
  f = setCap(f, 'memory.write', true)
  assert.deepEqual(f.caps, ['talkback.channels', 'memory.write'])
  assert.equal(setCap(f, 'memory.write', true).caps.length, 2, 'no repeats')
})

test('permissions: a role is granted only a non-sensitive cap that is in the ceiling', () => {
  const f = { caps: ['memory.write', 'profile.write'], grants: [] }
  const g = setGrant(f, 'admin', 'memory.write', true, CAPS)
  assert.equal(roleHas(g, 'admin', 'memory.write'), true)
  assert.equal(setGrant(f, 'admin', 'profile.write', true, CAPS), f, 'sensitive: refused')
  assert.equal(setGrant(f, 'admin', 'talkback.channels', true, CAPS), f, 'not in the ceiling: refused')
  const off = setGrant(g, 'admin', 'memory.write', false, CAPS)
  assert.deepEqual(off.grants, [], 'the last cap of a role removes its grant')
})

test('permissions: the matrix lists what the kind can grant, inside the ceiling, never a sensitive cap', () => {
  assert.deepEqual(grantableFor(KIND, ['memory.write', 'profile.write']), ['memory.write'])
  assert.deepEqual(grantableFor(KIND, []), [])
  assert.deepEqual(grantableFor({}, ['memory.write']), [])
})

test('permissions: channels need a unique id with no space, at most ten', () => {
  assert.equal(channelsProblem([{ id: 'C1', label: 'ops', approved: true }]), '')
  assert.equal(channelsProblem([{ id: '', label: '' }]), 'id')
  assert.equal(channelsProblem([{ id: 'a b' }]), 'id')
  assert.equal(channelsProblem([{ id: 'C1' }, { id: 'C1' }]), 'repeat')
  assert.equal(channelsProblem([{ id: 'C'.repeat(65) }]), 'id')
  assert.equal(channelsProblem([{ id: 'C1', label: 'x'.repeat(61) }]), 'label')
  assert.equal(channelsProblem(Array.from({ length: MAX_CHANNELS + 1 }, (_, i) => ({ id: `C${i}` }))), 'count')
})

test('permissions: the request carries caps, grants and channels, and a legacy binding reads back unchanged', () => {
  const saved = { id: 'b1', name: 'B', agent_id: 'a', caps: ['memory.write'], grants: [{ role: 'admin', caps: ['memory.write'] }], channels: [{ id: 'C1', label: 'ops', approved: true }] }
  const body = connectorPayload(connectorForm(saved), 'slack')
  assert.deepEqual(body.caps, ['memory.write'])
  assert.deepEqual(body.grants, [{ role: 'admin', caps: ['memory.write'] }])
  assert.deepEqual(body.channels, [{ id: 'C1', label: 'ops', approved: true }])
  const legacy = connectorPayload(connectorForm({ id: 'b2', name: 'B', agent_id: 'a' }), 'slack')
  assert.deepEqual([legacy.caps, legacy.grants, legacy.channels], [[], [], []])
})

test('permissions: a bad channel list blocks saving', () => {
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }
  assert.equal(canSave(ok, false), true)
  assert.equal(canSave({ ...ok, channels: [{ id: '' }] }, false), false)
})

const ALL = [
  { name: 'proposals.confirm', title: 'Confirm', description: 'x', sensitive: false, usual: true },
  { name: 'memory.write', title: 'Save to memory', description: 'x', sensitive: false, usual: true },
  { name: 'files.upload', title: 'Receive files', description: 'x', sensitive: false, usual: true },
  { name: 'talkback.channels', title: 'Post', description: 'x', sensitive: false, usual: true },
  { name: 'talkback.new_channel', title: 'New channel', description: 'x', sensitive: true, usual: false },
  { name: 'profile.write', title: 'Profile', description: 'x', sensitive: true, usual: false },
  { name: 'tools.write', title: 'Tools', description: 'x', sensitive: false, usual: false },
]

test('presets: the grants of an agent are read from its list, an empty list means the messaging defaults, and no list means unreadable', () => {
  assert.deepEqual(agentHeld({ builtins: ['memory.write'] }), ['memory.write'])
  assert.deepEqual(agentHeld({ builtins: [] }), DEFAULT_CONNECTOR_GRANTS)
  assert.equal(agentHeld({}), null)
  assert.equal(agentHeld(undefined), null)
  assert.equal(agentHas(null, 'memory.write'), true, 'an agent that cannot be read gates nothing')
  assert.equal(agentHas(['files.upload'], 'memory.write'), false)
  assert.equal(agentHas(['files.upload'], 'proposals.confirm'), true, 'not a grant of an agent')
  assert.equal(AGENT_GRANT_CAPS.includes('proposals.confirm') || AGENT_GRANT_CAPS.includes('tools.write'), false)
})

test('presets: the buttons are blocked with a reason when there is no agent, no readable permissions or nothing to use', () => {
  assert.equal(presetBlock('', ['memory.write'], ALL), 'noAgent')
  assert.equal(presetBlock('a1', null, ALL), 'unreadable')
  assert.equal(presetBlock('a1', ['unknown.grant'], ALL), 'nothing')
  assert.equal(presetBlock('a1', ['memory.write'], ALL), '')
})

test('presets: the usual set is limited to what the agent has, and never holds a sensitive cap', () => {
  const r = usualFor(ALL, ['memory.write', 'talkback.new_channel', 'profile.write'])
  assert.deepEqual(r.caps, ['proposals.confirm', 'memory.write'])
  assert.deepEqual(r.skipped, ['files.upload', 'talkback.channels'])
  assert.deepEqual(usualFor(ALL, null).skipped, [], 'an agent that cannot be read skips nothing')
  assert.deepEqual(withAddedCaps({ caps: ['tools.write', 'memory.write'] }, r.caps).caps, ['tools.write', 'memory.write', 'proposals.confirm'])
})

test('presets: copying the agent takes every cap it holds, names the sensitive ones, and keeps the caps that are not grants', () => {
  const c = copyFromAgent(ALL, ['memory.write', 'talkback.new_channel', 'profile.write', 'act.send', 'saved_agents'])
  assert.deepEqual(c.caps, ['memory.write', 'talkback.new_channel', 'profile.write'])
  assert.deepEqual(c.sensitive, ['talkback.new_channel', 'profile.write'])
  assert.deepEqual(copyFromAgent(ALL, ['memory.write']).sensitive, [])
  const form = { caps: ['proposals.confirm', 'files.upload', 'tools.write'], grants: [{ role: 'admin', caps: ['files.upload', 'tools.write'] }] }
  const next = withCopiedCaps(form, ['memory.write'])
  assert.deepEqual(next.caps, ['proposals.confirm', 'tools.write', 'memory.write'], 'files.upload is replaced, the others stay')
  assert.deepEqual(next.grants, [{ role: 'admin', caps: ['tools.write'] }], 'a role never keeps a cap the ceiling lost')
  assert.deepEqual(form.caps, ['proposals.confirm', 'files.upload', 'tools.write'], 'the form is not changed')
})

test('pairing notice: only a ticked sensitive cap needs a pairing, and only while nobody has a live one', () => {
  const now = Date.parse('2026-11-04T10:00:00Z')
  const live = [{ sender_id: 'U1', expires_at: '2026-12-01T00:00:00Z' }]
  const ended = [{ sender_id: 'U1', expires_at: '2026-11-01T00:00:00Z' }]
  const form = { caps: ['memory.write', 'triggers.propose', 'profile.write', 'talkback.new_channel'] }
  assert.deepEqual(unpairedSensitive(form, [], CAPS, now), ['profile.write'], 'a non-sensitive cap needs no pairing')
  assert.deepEqual(unpairedSensitive(form, live, CAPS, now), [])
  assert.deepEqual(unpairedSensitive(form, ended, CAPS, now), ['profile.write'], 'an ended pairing counts for nothing')
  assert.deepEqual(unpairedSensitive({ caps: ['memory.write'] }, undefined, CAPS, now), [])
  assert.deepEqual(unpairedSensitive({ caps: [] }, [], CAPS, now), [])
})

test('channel-only allowlist: shown when the page has a people list and every entry is a channel', () => {
  const lists = [{ match: (x) => !x.startsWith('C') }, { match: (x) => x.startsWith('C') }]
  assert.equal(channelOnly({ allow: ['C1', 'C2'] }, lists), true)
  assert.equal(channelOnly({ allow: ['C1', 'U1'] }, lists), false)
  assert.equal(channelOnly({ allow: [] }, lists), false)
  assert.equal(channelOnly({ allow: ['C1'], accept_any: true }, lists), false)
  assert.equal(channelOnly({ allow: ['C1'] }, [{}]), false, 'one list: nothing to tell apart')
})

test('channel trust: an entry that is added starts as act, one stored before the setting stays answer only', async () => {
  const { trustOf, trustAfter, channelEntries } = await import('../../../internal/connector-ui/src/connector.js')
  const isChannel = (x) => x.startsWith('C')
  const stored = { allow: ['U1', 'C1'], allow_trust: {} }
  assert.equal(trustOf(stored, 'C1'), 'answer', 'no level is answer only')
  assert.deepEqual(channelEntries(stored, isChannel), ['C1'])
  const added = trustAfter(stored, ['U1', 'C1', 'C2'], isChannel)
  assert.deepEqual(added, { C2: 'act' }, 'only the new channel is act, the stored one is not widened')
  assert.deepEqual(trustAfter({ allow: ['C2'], allow_trust: { C2: 'answer' } }, ['C2', 'U9'], isChannel), { C2: 'answer' }, 'a choice is kept, a person gets none')
  assert.deepEqual(trustAfter({ allow: ['C2'], allow_trust: { C2: 'act' } }, [], isChannel), {}, 'the level of a removed entry goes')
})

test('channel trust: the request carries the levels of the entries on the list only, and a channel set to act is not channel-only', async () => {
  const f = { ...emptyConnector(), allow: ['U1', 'C1'], allow_trust: { C1: 'act', C9: 'act' } }
  assert.deepEqual(connectorPayload(f, 'slack', '').allow_trust, { C1: 'act' })
  assert.deepEqual(connectorPayload({ ...f, accept_any: true }, 'slack', '').allow_trust, {})
  assert.deepEqual(connectorForm({ allow: ['C1'], allow_trust: { C1: 'act' } }).allow_trust, { C1: 'act' })
  const lists = [{ match: (x) => !x.startsWith('C') }, { match: (x) => x.startsWith('C') }]
  assert.equal(channelOnly({ allow: ['C1'], allow_trust: { C1: 'answer' } }, lists), true)
  assert.equal(channelOnly({ allow: ['C1'], allow_trust: { C1: 'act' } }, lists), false)
})
