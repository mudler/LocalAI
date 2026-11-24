import test from 'node:test'
import assert from 'node:assert/strict'
import { connectionsFor, proposalView } from '../src/proposal.js'
import { finishTest, forget, resultOf, startTest, subscribe } from '../src/results.js'
import en from '../src/i18n/github.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

test('github proposal: the card shows the full stored text, the place and the link', () => {
  const v = proposalView('comment-issue', { tool: 'comment-issue', repo: 'acme/widgets', number: 12, body: 'Full text\nsecond line' },
    { repo: 'acme/widgets', number: 12, targetTitle: 'Crash on start', body: 'Full', bodyCut: true, bodyBytes: 21 })
  assert.equal(v.url, 'https://github.com/acme/widgets/issues/12')
  assert.equal(v.body, 'Full text\nsecond line')
  assert.equal(v.cut, false, 'the stored text is whole')
  assert.equal(v.targetTitle, 'Crash on start')
})

test('github proposal: a repository that is not owner/name gets no link, and a cut preview says so', () => {
  const v = proposalView('comment-issue', null, { repo: 'javascript:alert(1)', number: 1, body: 'x', bodyCut: true, bodyBytes: 99 })
  assert.equal(v.repo, '')
  assert.equal(v.url, '')
  assert.equal(v.cut, true)
  assert.equal(v.bytes, 99)
})

test('github proposal: labels, state and line comments are read', () => {
  assert.deepEqual(proposalView('add-labels', { labels: ['bug', 3] }, {}).labels, ['bug'])
  const e = proposalView('edit-issue', { state: 'closed', reason: 'not_planned' }, {})
  assert.equal(e.state, 'closed')
  assert.equal(e.reason, 'not_planned')
  const r = proposalView('review-comment-pr', { path: 'a.go', line: 4, side: 'LEFT' }, {})
  assert.deepEqual([r.path, r.line, r.side], ['a.go', 4, 'LEFT'])
})

test('github proposal: the connection is found by the list, in order, any case', () => {
  const list = [{ kind: 'github', name: 'Docs', resources: ['Acme/Widgets'] }, { kind: 'telegram', name: 'Bot', resources: ['acme/widgets'] }, { kind: 'github', name: 'Work', resources: ['acme/widgets'] }]
  assert.deepEqual(connectionsFor(list, 'acme/widgets'), ['Docs', 'Work'])
  assert.deepEqual(connectionsFor(list, 'x/y'), [])
  assert.deepEqual(connectionsFor(null, 'x/y'), [])
})

test('github proposal: every write tool has a card and a title', () => {
  const ui = manifest.capabilities.ui
  const writes = manifest.capabilities.toolDefinitions.filter((d) => d.kind === 'write').map((d) => d.name)
  assert.equal(writes.length, 6)
  for (const tool of writes) {
    assert.ok(ui.renderers.some((r) => r.kind === 'approval-card' && r.tool === tool && r.component === 'ProposalCard'), tool)
    assert.ok(en.approval.action[tool], tool)
  }
})

test('github test results: a new run clears the old one and the row hears about it', () => {
  let calls = 0
  const off = subscribe(() => { calls++ })
  finishTest('b1', [{ repo: 'a/b', tone: 'ok', text: 'x' }], 5)
  assert.equal(resultOf('b1').busy, false)
  startTest('b1')
  assert.deepEqual(resultOf('b1'), { busy: true, items: [], at: 0 })
  forget('b1')
  assert.equal(resultOf('b1'), null)
  assert.equal(calls, 3)
  off()
})
