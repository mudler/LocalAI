import test from 'node:test'
import assert from 'node:assert/strict'
import { same, nodeChanged, graphForNode, rebaseEdits, ancestors, executionIssue, runAllIssue, workbookPlan } from '../src/workbook.js'

const models = [{ id: 'llm' }, { id: 'image-model' }, { id: 'mesh-model' }]
const snapshot = () => ({ graph: { primary: 'mesh', target: { inputs: ['image'], advice: 'game asset' }, nodes: [
  { id: 'source', kind: 'source', input: { value: { type: 'text', text: 'tower' } } },
  { id: 'review', kind: 'review', model: 'llm', input: { from: 'source' }, pause: true },
  { id: 'image', kind: 'image', model: 'image-model', input: { from: 'review' }, count: 1 },
  { id: 'extract', kind: 'extract', input: { from: 'image' }, params: { mode: 'keep' } },
  { id: 'mesh', kind: 'mesh', model: 'mesh-model', input: { from: 'image' } },
] }, nodes: {
  source: { status: 'stale' },
  review: { status: 'succeeded', selected: { type: 'text', text: 'tower' } },
  image: { status: 'succeeded', selected: { type: 'image', artifact: 'picture' } },
  mesh: { status: 'stale' },
} })

test('saving one card excludes edits on other cards', () => {
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.nodes[0].input.value.text = 'castle'
  draft.nodes[1].pause = false
  assert.equal(nodeChanged(s.graph, draft, 'source'), true)
  assert.equal(nodeChanged(s.graph, draft, 'image'), false)
  const saved = graphForNode(s.graph, draft, 'review')
  assert.equal(saved.nodes[1].pause, false)
  assert.equal(saved.nodes[0].input.value.text, 'tower')
  assert.equal(s.graph.nodes[1].pause, true)
})

test('primary change belongs to selected card and target profile belongs to mesh', () => {
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.primary = 'image'; draft.target.advice = 'black background'
  assert.equal(nodeChanged(s.graph, draft, 'image'), true)
  assert.equal(nodeChanged(s.graph, draft, 'mesh'), true)
  assert.equal(graphForNode(s.graph, draft, 'review').primary, 'mesh')
  assert.equal(graphForNode(s.graph, draft, 'image').primary, 'image')
  assert.equal(graphForNode(s.graph, draft, 'image').target.advice, 'game asset')
  assert.equal(graphForNode(s.graph, draft, 'mesh').target.advice, 'black background')
})

test('rebasing keeps unsaved cards and concurrent edits without resurrecting saved edits', () => {
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.nodes[0].input.value.text = 'castle'; draft.nodes[1].pause = false
  const saved = graphForNode(s.graph, draft, 'review')
  const remaining = rebaseEdits(saved, draft, saved)
  assert.equal(remaining.nodes[0].input.value.text, 'castle')
  assert.equal(nodeChanged(saved, remaining, 'review'), false)
  const concurrent = structuredClone(draft); concurrent.nodes[1].model = 'new-model'
  assert.equal(rebaseEdits(draft, concurrent, saved).nodes[1].model, 'new-model')
  assert.equal(rebaseEdits(draft, draft, saved), null)
  assert.equal(rebaseEdits(s.graph, draft, s.graph).nodes[0].input.value.text, 'castle')
  assert.equal(same(s.graph, structuredClone(s.graph)), true)
})

test('rebasing overlays changed fields without discarding new saved fields', () => {
  const s = snapshot(), draft = structuredClone(s.graph), saved = structuredClone(s.graph)
  draft.nodes[1].pause = false; saved.nodes[1].model = 'new-model'
  const merged = rebaseEdits(s.graph, draft, saved)
  assert.equal(merged.nodes[1].pause, false)
  assert.equal(merged.nodes[1].model, 'new-model')
})

test('saved direct source text is available without a source execution', () => {
  const s = snapshot()
  assert.equal(executionIssue(s, null, 'review', models), '')
  assert.equal(executionIssue(s, null, 'mesh', models), '')
  assert.match(executionIssue(s, null, 'source', models), /does not run/)
  assert.match(executionIssue(s, null, 'extract', models), /not part/)
  assert.deepEqual([...ancestors(s.graph, 'mesh')], ['mesh', 'image', 'review', 'source'])
})

test('unsaved ancestors including pause changes block execution but unrelated cards do not', () => {
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.nodes[3].params.mode = 'segment'
  assert.equal(executionIssue(s, draft, 'mesh', models), '')
  assert.equal(runAllIssue(s, draft, models), '')
  draft.nodes[1].pause = false
  assert.match(executionIssue(s, draft, 'mesh', models), /Save changes/)
  assert.match(runAllIssue(s, draft, models), /Save changes/)
})

test('stale or unselected processing dependencies block individual runs', () => {
  for (const status of ['stale', 'waiting', 'failed', 'interrupted']) {
    const s = snapshot(); s.nodes.review.status = status
    assert.match(executionIssue(s, null, 'mesh', models), /upstream input/)
    assert.equal(runAllIssue(s, null, models), '')
  }
  const s = snapshot(); delete s.nodes.image.selected
  assert.match(executionIssue(s, null, 'mesh', models), /upstream input/)
})

test('waiting outputs can be regenerated without accepting them first', () => {
  const s = snapshot()
  s.nodes.review = { status: 'waiting' }
  assert.equal(executionIssue(s, null, 'review', models), '')
  assert.match(executionIssue(s, null, 'image', models), /upstream input/)
  s.nodes.review = { status: 'succeeded', selected: { type: 'text', text: 'tower' } }
  s.nodes.image = { status: 'waiting' }
  assert.equal(executionIssue(s, null, 'image', models), '')
})

test('invalid graph, unavailable model, unsaved primary and running state block inference', () => {
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.primary = 'review'
  assert.match(executionIssue(s, draft, 'review', models), /primary/)
  assert.match(runAllIssue(s, draft, models), /Save changes/)
  assert.match(executionIssue(s, null, 'review', []), /no longer available/)
  s.nodes.image.status = 'running'
  assert.match(executionIssue(s, null, 'review', models), /running stage/)
  assert.match(runAllIssue(s, null, models), /running stage/)
  s.nodes.image.status = 'succeeded'; s.graph.nodes[3].input = { from: 'mesh' }
  assert.match(executionIssue(s, null, 'review', models), /earlier/)
  assert.match(runAllIssue(s, null, models), /earlier/)
})

test('run all starts first processing stage while continue skips completed output and inactive nodes', () => {
  const s = snapshot()
  assert.deepEqual(workbookPlan(s, null, true), ['review'])
  assert.deepEqual(workbookPlan(s, null, false), ['mesh'])
  s.graph.nodes[1].pause = false
  assert.deepEqual(workbookPlan(s, null, true), ['review', 'image', 'mesh'])
  s.graph.nodes[2].count = 4
  assert.deepEqual(workbookPlan(s, null, true), ['review', 'image'])
})

test('continue stops at a decision or failure while Run all restarts from the beginning', () => {
  for (const status of ['waiting', 'failed', 'interrupted']) {
    const s = snapshot(); s.nodes.review.status = status
    assert.deepEqual(workbookPlan(s, null, false), [])
    assert.deepEqual(workbookPlan(s, null, true), ['review'])
  }
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.nodes[0].input.value.text = 'castle'
  assert.deepEqual(workbookPlan(s, draft, true), [])
})
