import test from 'node:test'
import assert from 'node:assert/strict'
import { proposalView } from '../src/proposal.js'
import en from '../src/i18n/gdrive.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

test('gdrive proposal: the card shows the full stored text and the names', () => {
  const v = proposalView('create-file', { tool: 'create-file', name: 'notes.md', content: 'Full text\nsecond line', parent: 'abc_123' },
    { name: 'notes.md', content: 'Full', contentCut: true, contentBytes: 21, parentName: 'Projects' })
  assert.equal(v.content, 'Full text\nsecond line')
  assert.equal(v.cut, false, 'the stored text is whole')
  assert.equal(v.parent, 'abc_123')
  assert.equal(v.parentName, 'Projects')
  assert.equal(v.hasContent, true)
})

test('gdrive proposal: a cut preview says so, and an ID that is not one is dropped', () => {
  const v = proposalView('update-file', null, { fileId: '../x', content: 'x', contentCut: true, contentBytes: 99, targetName: 'a.txt' })
  assert.equal(v.fileId, '')
  assert.equal(v.cut, true)
  assert.equal(v.bytes, 99)
  assert.equal(v.targetName, 'a.txt')
})

test('gdrive proposal: a move and a trash have no text', () => {
  const m = proposalView('move-file', { fileId: 'f1', newParent: 'd2' }, { newParentName: 'Archive', targetName: 'a.txt' })
  assert.equal(m.hasContent, false)
  assert.equal(m.newParent, 'd2')
  assert.equal(proposalView('trash-file', {}, undefined).hasContent, false)
})

test('gdrive proposal: every write tool of the manifest has a title and a card', () => {
  const writes = manifest.capabilities.toolDefinitions.filter((d) => d.kind === 'write').map((d) => d.name)
  assert.ok(writes.length > 0)
  const cards = manifest.capabilities.ui.renderers.filter((r) => r.kind === 'approval-card')
  for (const name of writes) {
    assert.ok(en.approval.action[name], `title of ${name}`)
    assert.ok(cards.some((r) => r.tool === name && r.component === 'ProposalCard'), `card of ${name}`)
  }
})
