import test from 'node:test'
import assert from 'node:assert/strict'
import { chatText, nameOf, peopleFrom, personText } from '../../../internal/connector-ui/src/people.js'
import { toolOutcomeRows } from '../../../internal/connector-ui/src/access.js'

const LOG = [
  { id: 'U001', name: 'Ada Example', chat: 'C0123', chat_name: 'ops', allowed: true },
  { id: 'U002', name: 'U002', chat: 'D9', allowed: false, reason: 'not_listed' },
  { id: 'U003', name: '' },
]

test('people: the directory keeps the names of the sender log and drops a name that is only the ID', () => {
  const p = peopleFrom(LOG)
  assert.equal(nameOf(p, 'U001'), 'Ada Example')
  assert.equal(nameOf(p, 'U002'), '', 'a name equal to the ID adds nothing')
  assert.equal(nameOf(p, 'U003'), '')
  assert.equal(nameOf(p, 'U404'), '', 'an unknown ID has no name')
  assert.deepEqual(peopleFrom(undefined), new Map())
  assert.deepEqual(peopleFrom([null, {}]), new Map())
})

test('people: a name sits beside the ID, and the ID alone is the fallback', () => {
  const p = peopleFrom(LOG)
  assert.equal(personText(p, 'U001'), 'Ada Example · U001')
  assert.equal(personText(p, 'U002'), 'U002')
  assert.equal(personText(p, 'U404'), 'U404')
  assert.equal(personText(new Map(), 'U9', 'Grace'), 'Grace · U9', 'the name an event carries wins when the directory has none')
  assert.equal(personText(p, 'U001', 'Ada Renamed'), 'Ada Renamed · U001', 'the name of the event is the newer one')
  assert.equal(personText(undefined, 'U9'), 'U9')
})

test('people: a chat is its name and ID, or just its ID', () => {
  assert.equal(chatText('C0123', 'ops'), 'ops (C0123)')
  assert.equal(chatText('C0123', ''), 'C0123')
  assert.equal(chatText('C0123', 'C0123'), 'C0123')
  assert.equal(chatText('', 'ops'), 'ops')
  assert.equal(chatText('', ''), '')
})

test('people: a tool outcome row keeps the name of the sender the event carried', () => {
  const [row] = toolOutcomeRows({ tool_outcomes: [{ at: '2026-11-05T09:41:00Z', senders: ['U001'], sender_name: 'Ada Example', addon: 'x', tool: 't', outcome: 'ok' }] })
  assert.equal(row.senderName, 'Ada Example')
  assert.deepEqual(row.senders, ['U001'])
  assert.equal(toolOutcomeRows({ tool_outcomes: [{ at: 'a', tool: 't', outcome: 'ok' }] })[0].senderName, '')
})
