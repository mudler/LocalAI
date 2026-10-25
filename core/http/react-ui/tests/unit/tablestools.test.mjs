import test from 'node:test'
import assert from 'node:assert/strict'
import { isAddonToolName, proposalReference, storedResult } from '../../src/addons/proposalResult.js'
import { approvalState, confirmCall, proposalFacts } from '../../src/addons/approval.js'
import { availableChatActions } from '../../src/components/chat/chatActions.js'

const proposal = (over = {}) => ({
  id: 'tabc123', addonId: 'tables', resourceId: 'db-one', generation: 1, action: 'write',
  arguments: { tool: 'rows-insert', table: 'T', rows: [{ a: 1 }] }, digest: 'ab'.repeat(32), state: 'pending',
  effects: { count: 3, sample: [{ a: 1 }] }, expiresAt: new Date(Date.now() + 60_000).toISOString(), ...over,
})
const result = (over = {}) => JSON.stringify({ kind: 'proposal', tool: 'addon__tables__rows-insert', proposal: { id: 'tabc123', digest: 'x', count: 99, summary: 'model text' }, ...over })

test('tools: the namespace is the brokered add-on tool names only', () => {
  assert.equal(isAddonToolName('addon__tables__rows-insert'), true)
  for (const name of ['rows-insert', 'tables__rows-insert', 'addon__tables', 'addon____x', 'addon__Tables__x', 'addon__tables__rows_insert', '', null, 5]) {
    assert.equal(isAddonToolName(name), false, String(name))
  }
})

test('tools: a proposal result of an add-on tool gives its identifier and nothing else', () => {
  const ref = proposalReference('addon__tables__rows-insert', result())
  assert.deepEqual(ref, { proposalId: 'tabc123', tool: 'rows-insert' })
  assert.deepEqual(Object.keys(ref).sort(), ['proposalId', 'tool'])
  // An object result is read the same way as its JSON text.
  assert.deepEqual(proposalReference('addon__tables__rows-insert', JSON.parse(result())), ref)
})

test('tools: other kinds, other tools and malformed identifiers give no card', () => {
  assert.equal(proposalReference('get_weather', result()), null, 'another tool source is never a proposal')
  assert.equal(proposalReference('addon__tables__rows-query', result({ kind: 'result' })), null)
  assert.equal(proposalReference('addon__tables__rows-query', JSON.stringify({ kind: 'artifact', artifacts: [] })), null)
  for (const id of ['', ' x', '../x', 'a'.repeat(300), 5, null, '<img src=x>']) {
    assert.equal(proposalReference('addon__tables__rows-insert', result({ proposal: { id } })), null, String(id))
  }
  for (const raw of ['', 'not json', '[]', 'null', '"proposal"', 5, undefined]) {
    assert.equal(proposalReference('addon__tables__rows-insert', raw), null, String(raw))
  }
})

test('tools: the stored history keeps the reference of a proposal and no row data', () => {
  const kept = storedResult('addon__tables__rows-insert', result({ proposal: { id: 'tabc123', digest: 'DIGEST', summary: [{ Memo: 'ROW-SECRET' }] }, result: { rows: [{ Memo: 'ROW-SECRET' }] } }))
  assert.deepEqual(JSON.parse(kept), { kind: 'proposal', proposal: { id: 'tabc123' } })
  assert.equal(kept.includes('ROW-SECRET'), false)
  assert.equal(kept.includes('DIGEST'), false)
  // A read result is never stored. A file result keeps only well formed host
  // identifiers: no name, size or path the add-on or the model supplied.
  assert.equal(storedResult('addon__tables__rows-query', JSON.stringify({ kind: 'result', result: { rows: [[1]] } })), null)
  assert.equal(storedResult('addon__tables__rows-export', JSON.stringify({ kind: 'artifact', artifacts: [{ id: 'a' }] })), null)
  const fileId = 'a' + '0123456789abcdef'.repeat(2)
  assert.equal(
    storedResult('addon__tables__rows-export', JSON.stringify({ kind: 'artifact', artifacts: [{ id: fileId, name: '../../x', url: 'https://evil.example/', size: 9 }, { id: '../x' }] })),
    JSON.stringify({ kind: 'artifact', artifacts: [{ id: fileId }] }),
  )
  assert.equal(storedResult('some_other_tool', result()), null)
})

test('tools: the card facts come from the stored proposal and never from model text', () => {
  const f = proposalFacts(proposal())
  assert.equal(f.addonId, 'tables')
  assert.equal(f.database, 'db-one')
  assert.equal(f.operation, 'rows-insert')
  assert.equal(f.count, 3)
  assert.equal(f.state, 'pending')
  assert.equal(f.canDecide, true)
  assert.equal(f.settled, false)
  // A create proposal shows the requested name, not the reservation.
  const c = proposalFacts(proposal({ action: 'create', resourceId: 'db-reserved', arguments: { tool: 'databases-create', name: 'Ledger' }, effects: { count: 1, sample: { action: 'create', name: 'Ledger' } } }))
  assert.equal(c.requestedName, 'Ledger')
  assert.equal(c.database, 'db-reserved')
  // A proposal without a valid identifier decides nothing.
  const bad = proposalFacts({ id: '../x', state: 'pending', digest: 'd', effects: { count: 1 } })
  assert.equal(bad.canDecide, false)
  assert.equal(bad.count, 0)
})

test('tools: stale, expired, blocked and settled proposals cannot be decided', () => {
  const now = Date.now()
  assert.equal(approvalState(proposal({ expiresAt: new Date(now - 1000).toISOString() }), now).canDecide, false)
  assert.equal(proposalFacts(proposal({ expiresAt: new Date(Date.now() - 1000).toISOString() })).settled, true)
  assert.equal(approvalState(proposal({ blocked: true }), now).canDecide, false)
  for (const state of ['applied', 'cancelled', 'expired', 'executing', 'rejected', 'outcome-unknown', 'abandoned-unknown']) {
    assert.equal(approvalState(proposal({ state }), now).canDecide, false, state)
  }
  assert.equal(proposalFacts(proposal({ state: 'outcome-unknown' })).unknownOutcome, true)
  assert.equal(proposalFacts(proposal({ state: 'applied' })).settled, true)
  assert.equal(approvalState(proposal({ digest: '' }), now).canDecide, false)
})

test('tools: a confirmation names the displayed proposal and its digest and nothing a model supplied', () => {
  const call = confirmCall(proposal({ arguments: { tool: 'rows-insert', approved: true, digest: 'forged' } }))
  assert.equal(call.method, 'POST')
  assert.equal(call.path, '/api/addons/proposals/tabc123/confirm')
  assert.deepEqual(call.headers, { 'X-LocalAI-Confirm': 'tabc123' })
  assert.deepEqual(call.body, { digest: 'ab'.repeat(32) })
  assert.equal(confirmCall(proposal({ digest: '' })), null)
  assert.equal(confirmCall(proposal({ id: 'a/../b' })), null)
})

test('tools: the add-on tools switch is offered only when an add-on offers tools', () => {
  const ids = (ctx) => availableChatActions(ctx).map(a => a.id)
  assert.equal(ids({ isAdmin: false, hasModels: true, hasThread: false }).includes('addon-tools'), false)
  assert.equal(ids({ isAdmin: false, hasModels: true, hasThread: false, hasAddonTools: true }).includes('addon-tools'), true)
  assert.equal(ids({ isAdmin: false, hasModels: true, hasThread: false, hasAddonTools: true }).includes('assistant'), false)
})
