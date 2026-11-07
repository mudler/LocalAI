import test from 'node:test'
import assert from 'node:assert/strict'
import { attentionOf, applyPreset, checkRecord, connectionState, CHECKS_KEY, dropCheck, fixOf, looksLikeTokenFailure, presetOf, readChecks, standardTitles, writeChecks } from '../../../internal/connector-ui/src/connection.js'
import { emptyConnector } from '../../../internal/connector-ui/src/connector.js'

const CAPS = [
  { name: 'files.upload', title: 'Receive files', usual: true, sensitive: false },
  { name: 'memory.write', title: 'Save to memory', usual: true, sensitive: false },
  { name: 'talkback.new_channel', title: 'Post to a new channel', usual: false, sensitive: true },
  { name: 'tools.write', title: 'Change things', usual: false, sensitive: false },
]
const agent = (builtins) => ({ id: 'a', builtins })
const base = { id: 'b1', name: 'Bot', enabled: true, active: true, has_token: true }

test('connection state: running, off, and needs attention with its cause', () => {
  assert.equal(connectionState(base, undefined).state, 'running')
  assert.equal(connectionState({ ...base, enabled: false }, undefined).state, 'off')
  assert.deepEqual(connectionState({ ...base, has_token: false }, undefined).attention, { cause: 'noToken', reason: '' })
  const inactive = connectionState({ ...base, active: false, reason: 'install the telegram add-on' }, undefined)
  assert.equal(inactive.state, 'attention')
  assert.equal(inactive.attention.reason, 'install the telegram add-on')
  assert.equal(connectionState({ ...base, duplicate: { others: [{ id: 'b2', name: 'Other' }] } }, undefined).attention.cause, 'duplicate')
})

test('connection state: a switched-off connection is off even when its last test failed', () => {
  assert.equal(connectionState({ ...base, enabled: false }, { ok: false, reason: 'nope' }).state, 'off')
  assert.equal(connectionState({ ...base, enabled: false, active: false, reason: 'install it' }, undefined).state, 'attention')
})

test('connection state: a failed test names a token cause only when the words say so', () => {
  assert.equal(attentionOf(base, { ok: false, reason: 'Unauthorized: the token was refused' }).cause, 'tokenRejected')
  assert.equal(attentionOf(base, { ok: false, reason: 'timeout after 20s' }).cause, 'testFailed')
  assert.equal(attentionOf(base, { ok: true }), null)
  assert.equal(looksLikeTokenFailure('HTTP 401'), true)
  assert.equal(looksLikeTokenFailure('connection reset'), false)
})

test('connection state: credentials-only connections are ready, not running, and need no token to be saved as such', () => {
  const c = { ...base, enabled: false, credentials_only: true }
  assert.equal(connectionState(c, undefined, { messaging: false }).state, 'ready')
  assert.equal(connectionState(c, { ok: false, reason: 'refused' }, { messaging: false }).state, 'attention')
})

test('the fix of a row: a token cause opens the token, a twin turns this one off, other causes have none', () => {
  assert.equal(fixOf({ cause: 'noToken' }), 'token')
  assert.equal(fixOf({ cause: 'tokenRejected' }), 'token')
  assert.equal(fixOf({ cause: 'duplicate' }), 'duplicate')
  assert.equal(fixOf({ cause: 'testFailed' }), null)
  assert.equal(fixOf({ cause: 'inactive' }), null)
  assert.equal(fixOf(null), null)
})

test('presets: Answer only ticks nothing and clears the role grants', () => {
  const f = { ...emptyConnector(CAPS, null), grants: [{ role: 'member', caps: ['files.upload'] }] }
  const out = applyPreset(f, 'answer', CAPS, agent(['files.upload']))
  assert.deepEqual(out.caps, [])
  assert.deepEqual(out.grants, [])
  assert.equal(presetOf(out, CAPS, agent([])), 'answer')
})

test('presets: Standard is exactly the usual set the agent holds, and never a sensitive permission', () => {
  const held = agent(['files.upload', 'talkback.new_channel'])
  const out = applyPreset(emptyConnector(CAPS, null), 'standard', CAPS, held)
  assert.deepEqual(out.caps, ['files.upload'])
  assert.equal(out.caps.includes('talkback.new_channel'), false)
  assert.equal(presetOf(out, CAPS, held), 'standard')
  // An agent that holds both usual grants gets both.
  assert.deepEqual(applyPreset(emptyConnector(CAPS, null), 'standard', CAPS, agent(['files.upload', 'memory.write'])).caps.sort(), ['files.upload', 'memory.write'])
  // A sensitive permission marked usual by a host is still left out.
  const odd = [{ name: 'talkback.new_channel', usual: true, sensitive: true }, ...CAPS]
  assert.equal(applyPreset(emptyConnector(odd, null), 'standard', odd, null).caps.includes('talkback.new_channel'), false)
})

test('presets: anything else is Custom, and Custom leaves the form as it is', () => {
  const f = { ...emptyConnector(CAPS, null), caps: ['tools.write'] }
  assert.equal(presetOf(f, CAPS, null), 'custom')
  assert.equal(applyPreset(f, 'custom', CAPS, null), f)
  assert.equal(presetOf({ ...f, caps: ['files.upload'] }, CAPS, agent(['files.upload', 'memory.write'])), 'custom')
})

test('presets: the titles Standard names are the ones the agent holds, in the order of the host list', () => {
  assert.deepEqual(standardTitles(CAPS, agent(['memory.write'])), ['Save to memory'])
  assert.deepEqual(standardTitles(CAPS, null), ['Receive files', 'Save to memory'])
})

test('last test: kept in the browser, newest first, and a damaged store reads as empty', () => {
  const mem = new Map()
  const store = { getItem: (k) => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, v) }
  assert.deepEqual(readChecks(store), {})
  writeChecks({ b1: checkRecord({ ok: true, text: 'Connected' }, 5), b2: checkRecord({ ok: false, reason: 'refused' }, 9) }, store)
  const back = readChecks(store)
  assert.equal(back.b1.ok, true)
  assert.equal(back.b2.reason, 'refused')
  assert.deepEqual(Object.keys(dropCheck(back, 'b1')), ['b2'])
  mem.set(CHECKS_KEY, '{not json')
  assert.deepEqual(readChecks(store), {})
  mem.set(CHECKS_KEY, JSON.stringify({ x: 'bad', y: { ok: true } }))
  assert.deepEqual(Object.keys(readChecks(store)), ['y'])
  assert.deepEqual(readChecks(null), {})
})
