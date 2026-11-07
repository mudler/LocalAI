import test from 'node:test'
import assert from 'node:assert/strict'
import { proposalView } from '../src/proposal.js'
import en from '../src/i18n/gmail.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

test('gmail proposal: a draft shows the full stored text and the recipients', () => {
  const v = proposalView('create-draft', { to: ['a@example.test', 'b@example.test'], cc: ['c@example.test'], subject: 'Hi', body: 'Full text\nsecond line' },
    { body: 'Full', bodyCut: true, bodyBytes: 21 })
  assert.equal(v.to, 'a@example.test, b@example.test')
  assert.equal(v.cc, 'c@example.test')
  assert.equal(v.body, 'Full text\nsecond line')
  assert.equal(v.cut, false, 'the stored text is whole')
  assert.equal(v.hasBody, true)
})

test('gmail proposal: a send shows the draft from the preview, and says when the text is cut', () => {
  const v = proposalView('send-draft', { draftId: 'r123' }, { to: 'bob@example.test', bcc: 'x@example.test', subject: 'Lunch', body: 'noon?', bodyCut: true, bodyBytes: 5000, attachments: ['a.pdf', 7] })
  assert.equal(v.draftId, 'r123')
  assert.equal(v.to, 'bob@example.test')
  assert.equal(v.bcc, 'x@example.test')
  assert.equal(v.cut, true)
  assert.equal(v.bytes, 5000)
  assert.deepEqual(v.attachments, ['a.pdf'])
})

test('gmail proposal: an ID that is not one is dropped, and a wrong type is empty', () => {
  const v = proposalView('create-draft', null, { replyToMessageId: '../x', replyToFrom: 5, to: 7 })
  assert.equal(v.replyToMessageId, '')
  assert.equal(v.replyToFrom, '')
  assert.equal(v.to, '')
  assert.equal(proposalView('send-draft', {}, undefined).hasBody, false)
})

test('gmail proposal: every write tool of the manifest has a title and a card', () => {
  const writes = manifest.capabilities.toolDefinitions.filter((d) => d.kind === 'write').map((d) => d.name)
  assert.ok(writes.length > 0)
  const cards = manifest.capabilities.ui.renderers.filter((r) => r.kind === 'approval-card')
  for (const name of writes) {
    assert.ok(en.approval.action[name], `title of ${name}`)
    assert.ok(cards.some((r) => r.tool === name && r.component === 'ProposalCard'), `card of ${name}`)
  }
})
