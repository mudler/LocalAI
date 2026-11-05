import test from 'node:test'
import assert from 'node:assert/strict'
import {
  appliedReceiptKey, byteText, changeProposalIds, changeProposalView, estimatePassages, KNOWLEDGE_MAX_BYTES, knowledgeEntryText, KNOWN_KINDS, previewLines, sourceOfProposal, targetPath, timeLeft,
} from '../../src/utils/changeProposal.js'
import { diffLines, diffStats, splitLines, withContext } from '../../src/utils/lineDiff.js'
import { allFiles, groupKind, indexFits, indexUse } from '../../src/utils/memoryFiles.js'

const NOW = Date.parse('2026-11-03T10:00:00Z')
const proposal = (extra = {}) => ({
  v: 1, id: 'p1', kind: 'memory', scope: 'user', target: 'Memory/index.md', summary: 'Where you live', content: '- home: Rome\n',
  status: 'pending', created: '2026-11-03T09:00:00Z', expires: '2026-11-04T09:00:00Z', ...extra,
})

test('change proposals: the ids of the cards in a run of transcript lines', () => {
  const items = [
    { role: 'tool', card: { kind: 'change_proposal', id: 'a' } },
    { role: 'tool', card: { kind: 'trigger_proposal', id: 'b' } },
    { role: 'assistant', card: { kind: 'change_proposal', id: 'c' } },
    { role: 'tool', card: { kind: 'change_proposal' } },
    { role: 'tool', card: { kind: 'change_proposal', id: 'd' } },
  ]
  assert.deepEqual(changeProposalIds(items), ['a', 'd'])
  assert.deepEqual(changeProposalIds(null), [])
})

test('change proposals: every state the server keeps has a view, an unknown one is an error', () => {
  for (const status of ['pending', 'applied', 'dismissed', 'expired', 'reverted', 'stale']) {
    assert.equal(changeProposalView(proposal({ status }), null, NOW).kind, status)
  }
  assert.equal(changeProposalView(proposal({ status: 'bogus' }), null, NOW).kind, 'error')
  assert.equal(changeProposalView(null, { status: 404, message: 'x' }, NOW).kind, 'removed')
  assert.deepEqual(changeProposalView(null, { status: 500, message: 'boom' }, NOW), { kind: 'error', message: 'boom' })
})

test('change proposals: a pending proposal past its time is expired before the server says so', () => {
  assert.equal(changeProposalView(proposal({ expires: '2026-11-03T09:59:59Z' }), null, NOW).kind, 'expired')
  assert.equal(changeProposalView(proposal({ expires: '2026-11-03T10:00:01Z' }), null, NOW).kind, 'pending')
  // Only a pending proposal expires by the clock.
  assert.equal(changeProposalView(proposal({ status: 'applied', rev: 4, expires: '2026-11-03T09:00:00Z' }), null, NOW).kind, 'applied')
})

test('change proposals: Undo is offered for an applied change that has a change set', () => {
  assert.equal(changeProposalView(proposal({ status: 'applied', rev: 4 }), null, NOW).canUndo, true)
  assert.equal(changeProposalView(proposal({ status: 'applied' }), null, NOW).canUndo, false)
  assert.equal(changeProposalView(proposal({ status: 'reverted', rev: 4 }), null, NOW).canUndo, false)
})

test('change proposals: the source names no platform and shows the sender when the host gives one', () => {
  assert.deepEqual(sourceOfProposal(proposal({ source: { chat: 'c1' } })), { kind: 'chat', id: '', sender: '' })
  assert.deepEqual(sourceOfProposal(proposal({ source: { kind: 'connector', id: 'bind-1', chat: 'c1' } })), { kind: 'connector', id: 'bind-1', sender: '' })
  assert.equal(sourceOfProposal(proposal({ source: { kind: 'connector', id: 'b', sender: 'Ada' } })).sender, 'Ada')
  assert.equal(sourceOfProposal(null).kind, 'chat')
})

test('change proposals: the target path, the time left and the sizes', () => {
  assert.equal(targetPath({ target: 'Memory/index.md' }), '/Memory/index.md')
  assert.equal(targetPath({ target: '/Memory/index.md' }), '/Memory/index.md')
  assert.deepEqual(timeLeft(proposal({ expires: '2026-11-03T12:30:10Z' }), NOW), { hours: 2, minutes: 31 })
  assert.deepEqual(timeLeft(proposal({ expires: '2026-11-03T10:05:00Z' }), NOW), { hours: 0, minutes: 5 })
  assert.equal(timeLeft(proposal({ expires: '2026-11-03T10:00:00Z' }), NOW), null)
  assert.equal(byteText(512), '512 B')
  assert.equal(byteText(2048), '2.0 KiB')
  assert.equal(byteText(16384), '16 KiB')
})

test('line diff: lines split without the empty tail', () => {
  assert.deepEqual(splitLines('a\nb\n'), ['a', 'b'])
  assert.deepEqual(splitLines('a\r\nb'), ['a', 'b'])
  assert.deepEqual(splitLines(''), [])
  assert.deepEqual(splitLines(null), [])
})

test('line diff: a new file is all added, a deleted text is all removed', () => {
  assert.deepEqual(diffStats(diffLines('', '- a\n- b\n')), { added: 2, removed: 0 })
  assert.deepEqual(diffStats(diffLines('- a\n- b\n', '')), { added: 0, removed: 2 })
  assert.deepEqual(diffStats(diffLines('same\n', 'same\n')), { added: 0, removed: 0 })
})

test('line diff: a changed line is one removal and one addition, in reading order', () => {
  const ops = diffLines('- home: Rome\n- job: dev\n', '- home: Milan\n- job: dev\n- pet: cat\n')
  assert.deepEqual(ops.map((o) => `${o.type}:${o.text}`), ['del:- home: Rome', 'add:- home: Milan', 'same:- job: dev', 'add:- pet: cat'])
})

test('line diff: moved and repeated lines do not break the order', () => {
  const ops = diffLines('a\nb\nc\nb\n', 'b\nc\nb\nd\n')
  const keep = ops.filter((o) => o.type !== 'add').map((o) => o.text)
  const next = ops.filter((o) => o.type !== 'del').map((o) => o.text)
  assert.deepEqual(keep, ['a', 'b', 'c', 'b'])
  assert.deepEqual(next, ['b', 'c', 'b', 'd'])
})

test('line diff: a very long text still gives a diff, as one removal and one addition block', () => {
  const a = Array.from({ length: 700 }, (_, i) => `a${i}`).join('\n')
  const b = Array.from({ length: 700 }, (_, i) => `b${i}`).join('\n')
  assert.deepEqual(diffStats(diffLines(a, b)), { added: 700, removed: 700 })
})

test('line diff: context keeps a few lines around a change and folds the rest', () => {
  const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join('\n')
  const after = before.replace('l10', 'changed')
  const rows = withContext(diffLines(before, after), 2)
  assert.deepEqual(rows.map((r) => r.type), ['gap', 'same', 'same', 'del', 'add', 'same', 'same', 'gap'])
  assert.equal(rows[0].count, 8)
  assert.equal(rows[7].count, 7)
})

test('memory files: the use of an index against its caps', () => {
  const group = { max_bytes: 2048, max_lines: 40, files: [{ name: 'home.md', size: 10 }, { name: 'index.md', index: true, size: 600, lines: 12 }] }
  const u = indexUse(group)
  assert.equal(u.bytes, 600)
  assert.equal(u.lines, 12)
  assert.equal(u.over, false)
  assert.equal(indexUse({ ...group, files: [{ index: true, size: 3000, lines: 5 }] }).over, true)
  assert.equal(indexUse({ ...group, files: [{ index: true, size: 100, lines: 41 }] }).over, true)
  assert.equal(indexUse({ max_bytes: 2048, files: [] }).index, undefined)
})

test('memory files: an edited index must fit the caps, counting non-blank lines like the host', () => {
  const group = { max_bytes: 100, max_lines: 3 }
  assert.deepEqual(indexFits(group, '- a\n\n- b\n'), { bytes: 9, lines: 2, fits: true })
  assert.equal(indexFits(group, '- a\n- b\n- c\n- d').fits, false)
  assert.equal(indexFits(group, 'x'.repeat(101)).fits, false)
  // A profile has a byte cap and no line cap.
  assert.equal(indexFits({ max_bytes: 100 }, 'a\nb\nc\nd\ne\n').fits, true)
})

test('memory files: the kind of a group and the files for Clear all', () => {
  assert.equal(groupKind({ source: 'profile', scope: 'agent' }), 'profile')
  assert.equal(groupKind({ source: 'notes-agent', scope: 'agent' }), 'agent')
  assert.equal(groupKind({}), 'user')
  assert.equal(allFiles([{ files: [1, 2] }, { files: [3] }, {}]).length, 3)
})

test('change proposals: a note the host saved at once says so, a confirmed one says applied, both can be undone', () => {
  assert.equal(appliedReceiptKey(proposal({ status: 'applied', auto: true })), 'changes.receipt.savedAuto')
  assert.equal(appliedReceiptKey(proposal({ status: 'applied' })), 'changes.receipt.applied')
  assert.equal(appliedReceiptKey(proposal({ status: 'applied', auto: false })), 'changes.receipt.applied')
  assert.equal(appliedReceiptKey(null), 'changes.receipt.applied')
  const v = changeProposalView(proposal({ status: 'applied', auto: true, rev: 5 }), null, NOW)
  assert.equal(v.kind, 'applied')
  assert.equal(v.canUndo, true, 'an automatic note offers Undo')
})

test('change proposals: a knowledge change has no change set but can still be undone', () => {
  const k = proposal({ kind: 'knowledge', target: 'manuals', status: 'applied', rev: 0 })
  assert.equal(changeProposalView(k, null, NOW).canUndo, true)
  assert.equal(changeProposalView(proposal({ kind: 'memory', status: 'applied', rev: 0 }), null, NOW).canUndo, false)
  assert.equal(changeProposalView({ ...k, status: 'reverted' }, null, NOW).canUndo, false)
  assert.ok(KNOWN_KINDS.includes('knowledge'))
})

test('change proposals: the preview of a knowledge text shows 20 lines and counts the rest', () => {
  const text = Array.from({ length: 25 }, (_, i) => `line ${i + 1}`).join('\n') + '\n'
  const { shown, more } = previewLines(text)
  assert.equal(shown.length, 20)
  assert.equal(shown[19], 'line 20')
  assert.equal(more, 5)
  assert.deepEqual(previewLines('one\n\n'), { shown: ['one'], more: 0 })
  assert.deepEqual(previewLines(null), { shown: [''], more: 0 })
  assert.equal(KNOWLEDGE_MAX_BYTES, 65536)
})

test('change proposals: the passages of a knowledge text follow the chunking of the server', () => {
  assert.equal(estimatePassages('short'), 1)
  assert.equal(estimatePassages('x'.repeat(400)), 1)
  assert.equal(estimatePassages('x'.repeat(401)), 2)
  assert.equal(estimatePassages('x'.repeat(1200), 400, 0), 3)
  // Overlap makes each passage add fewer new characters.
  assert.equal(estimatePassages('x'.repeat(1200), 400, 100), 4)
  // A bad value from the server falls back to the default size.
  assert.equal(estimatePassages('x'.repeat(401), 0, -5), 2)
  assert.equal(estimatePassages('', 400, 0), 1)
  assert.equal(estimatePassages('é'.repeat(300), 400, 0), 2)
})

test('change proposals: the stored knowledge text is the summary as a title, then the text', () => {
  assert.equal(knowledgeEntryText({ summary: 'Reset', content: '  steps \n\n' }), '# Reset\n\nsteps\n')
  assert.equal(knowledgeEntryText(null), '# \n\n\n')
})
