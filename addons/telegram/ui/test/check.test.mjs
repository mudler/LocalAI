import test from 'node:test'
import assert from 'node:assert/strict'
import { placeFor, refusalActions, refusalRows, tryAnswer, tryChoices } from '../../../internal/connector-ui/src/check.js'

const FORM = { accept_any: false, allow: ['U001', 'U002', 'C0123'], allow_trust: { C0123: 'act' } }
const PAIRED = [{ sender_id: 'U001', expires_at: '2099-01-01T00:00:00Z' }, { sender_id: 'U009', expires_at: '2000-01-01T00:00:00Z' }]
const isChannel = (x) => x.startsWith('C')
const VIEW = {
  subjects: [
    { id: 'listed', decisions: [{ capability: 'files.upload', allowed: false, message: 'Not for listed people.', then_check: ['sender', 'approval'], fix: { kind: 'pair_sender' } }] },
    { id: 'paired', decisions: [{ capability: 'files.upload', allowed: true, via: 'paired' }] },
    { id: 'anyone', decisions: [] },
  ],
}

test('check: the choices of Try it are the people, the channels and anyone, each with the kind of sender the preview answers for', () => {
  const c = tryChoices({ form: FORM, people: new Map([['U002', { name: 'Grace Sample' }]]), chatNames: new Map([['C0123', 'ops']]), paired: PAIRED, isChannel })
  assert.deepEqual(c.who.map((x) => [x.value, x.subject, x.name]), [['person:U001', 'paired', ''], ['person:U002', 'listed', 'Grace Sample'], ['channel:C0123', 'channel_acts', 'ops']])
  assert.deepEqual(c.places.map((p) => p.value), ['dm', 'channel:C0123'])
  const any = tryChoices({ form: { accept_any: true, allow: ['U001'] }, paired: [], isChannel })
  assert.deepEqual(any.who.map((x) => x.value), ['anyone'])
  assert.deepEqual(any.places.map((p) => p.value), ['dm'])
  const level = tryChoices({ form: { allow: ['C1'], allow_trust: { C1: 'answer' } }, paired: [], isChannel })
  assert.equal(level.who[0].subject, 'anyone')
  const roles = tryChoices({ form: {}, paired: [], isChannel, roles: [{ id: 'admin', title: 'Admin' }] })
  assert.deepEqual(roles.who.map((x) => x.subject), ['role:admin'])
})

test('check: a verified person that ended is only a listed person', () => {
  const c = tryChoices({ form: { allow: ['U009'] }, paired: PAIRED, isChannel })
  assert.equal(c.who[0].subject, 'listed')
})

test('check: a kind with one list offers each entry as a person and as a channel', () => {
  const c = tryChoices({ form: { allow: ['123'] }, paired: [], isChannel: () => true, mixed: true })
  assert.deepEqual(c.who.map((x) => x.value), ['person:123', 'channel:123'])
})

test('check: someone in a channel writes in that channel', () => {
  assert.equal(placeFor({ kind: 'channel', id: 'C1' }), 'channel:C1')
  assert.equal(placeFor({ kind: 'person', id: 'U1' }), '')
})

test('check: the answer is the decision of the host for the kind of sender, and nothing else', () => {
  const listed = tryAnswer({ view: VIEW, who: { subject: 'listed' }, capability: 'files.upload' })
  assert.equal(listed.verdict, 'refused')
  assert.equal(listed.message, 'Not for listed people.')
  assert.deepEqual(listed.then, ['approval'])
  assert.deepEqual(listed.fix, { kind: 'pair_sender' })
  const paired = tryAnswer({ view: VIEW, who: { subject: 'paired' }, capability: 'files.upload' })
  assert.deepEqual([paired.verdict, paired.message, paired.fix], ['allowed', '', null])
  assert.equal(tryAnswer({ view: VIEW, who: { subject: 'anyone' }, capability: 'files.upload' }).verdict, 'unknown')
  assert.equal(tryAnswer({ view: VIEW, who: { subject: 'role:x' }, capability: 'files.upload' }).verdict, 'unknown')
  assert.equal(tryAnswer({ view: null, who: { subject: 'paired' }, capability: 'files.upload' }), null)
})

test('check: an action that asks first reads as asking', () => {
  const view = { proposal_mode: 'ask', subjects: [{ id: 'paired', decisions: [{ capability: 'triggers.propose', allowed: true }] }] }
  assert.equal(tryAnswer({ view, who: { subject: 'paired' }, capability: 'triggers.propose' }).verdict, 'asks')
})

test('check: refusals come from both logs, newest first, and never hold an allowed event', () => {
  const rows = refusalRows({
    recent: [
      { at: '2026-11-03T09:30:00Z', senders: ['U001'], capability: 'memory.write', allowed: true },
      { at: '2026-11-03T09:10:00Z', senders: ['U002'], sender_name: 'Grace Sample', chat: 'C0123', chat_name: 'ops', capability: 'files.upload', allowed: false, code: 'channel_not_trusted', reason: 'Not trusted.' },
    ],
    senders: [
      { id: 'U777', name: 'Stranger Example', allowed: false, reason: 'not_listed', last: '2026-11-03T09:20:00Z' },
      { id: 'U001', name: 'Ada Example', allowed: true, last: '2026-11-03T09:40:00Z' },
    ],
    titleOf: (n) => `title of ${n}`,
  })
  assert.deepEqual(rows.map((r) => r.sender), ['U777', 'U002'])
  assert.equal(rows[1].action, 'title of files.upload')
  assert.equal(rows[0].action, '')
  assert.equal(rows[0].code, 'not_listed')
  assert.equal(refusalRows({ recent: Array.from({ length: 30 }, (_, i) => ({ at: `2026-11-03T09:${String(i).padStart(2, '0')}:00Z`, capability: 'x', allowed: false })) }).length, 20)
})

test('check: a refusal offers Allow for a person who is not listed, and Let members act for a channel set to answer only', () => {
  const row = { sender: 'U002', chat: 'C0123', code: 'channel_not_trusted' }
  assert.deepEqual(refusalActions(row, { allow: ['C0123'], allow_trust: {} }), [{ type: 'allow', sender: 'U002' }, { type: 'act', chat: 'C0123' }])
  assert.deepEqual(refusalActions(row, { allow: ['C0123', 'U002'], allow_trust: { C0123: 'act' } }), [])
  assert.deepEqual(refusalActions({ ...row, code: 'sender_not_listed' }, { allow: ['C0123'] }), [{ type: 'allow', sender: 'U002' }])
  assert.deepEqual(refusalActions(row, { accept_any: true, allow: [] }), [])
})
