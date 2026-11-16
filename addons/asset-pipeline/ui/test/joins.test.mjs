import test from 'node:test'
import assert from 'node:assert/strict'
import { activeConnections, executionPreview, graphProblem, inputChoices, readiness, connectionLanes } from '../src/state.js'
import { ancestors, executionIssue, workbookPlan } from '../src/workbook.js'

const snapshot = () => {
  const nodes = [
    { id: 'mesh', kind: 'mesh', model: 'generator', input: { value: { type: 'image', artifact: 'image' } } },
    { id: 'remesh', kind: 'remesh', model: 'remesher', input: { from: 'mesh' } },
    { id: 'rig', kind: 'rig', model: 'skinning', input: { from: 'remesh' } },
    { id: 'description', kind: 'source', input: { value: { type: 'text', text: 'Walk forward.' } } },
    { id: 'motion', kind: 'motion', model: 'animation', input: { from: 'description' } },
    { id: 'retarget', kind: 'retarget', model: 'skinning', input: { from: 'rig' }, inputs: { motion: { from: 'motion' } } },
  ]
  return { graph: { primary: 'retarget', target: { inputs: ['image'] }, nodes }, nodes: Object.fromEntries(nodes.map(n => [n.id, { status: 'succeeded', selected: { artifact: n.id } }])) }
}
const models = ['generator', 'remesher', 'skinning', 'animation'].map(id => ({ id }))

test('long join edges use a separate lane from the branch they bypass', () => {
  const edges = connectionLanes(snapshot().graph)
  const rigJoin = edges.find(e => e.from === 'rig' && e.to === 'retarget')
  const motionJoin = edges.find(e => e.from === 'motion' && e.to === 'retarget')
  const motionInput = edges.find(e => e.from === 'description')
  assert.notEqual(rigJoin.lane, motionJoin.lane)
  assert.notEqual(rigJoin.lane, motionInput.lane)
})

test('retarget joins both branches and only offers compatible earlier outputs', () => {
  const s = snapshot(), node = s.graph.nodes.at(-1)
  assert.equal(graphProblem(s.graph), '')
  assert.deepEqual(inputChoices(s.graph, node).map(n => n.id), ['mesh', 'remesh', 'rig'])
  assert.deepEqual(inputChoices(s.graph, node, 'motion').map(n => n.id), ['motion'])
  assert.equal(ancestors(s.graph, node.id).size, 6)
  assert.deepEqual(activeConnections(s.graph), [
    { from: 'mesh', to: 'remesh' }, { from: 'remesh', to: 'rig' },
    { from: 'description', to: 'motion' }, { from: 'rig', to: 'retarget' }, { from: 'motion', to: 'retarget' },
  ])
  assert.deepEqual(workbookPlan(s, null, true), ['mesh', 'remesh', 'rig', 'motion', 'retarget'])
  s.graph.primary = 'rig'
  assert.deepEqual(activeConnections(s.graph), [{ from: 'mesh', to: 'remesh' }, { from: 'remesh', to: 'rig' }])
})

test('editing the animation branch stales the join without staling its mesh branch', () => {
  const s = snapshot(), draft = structuredClone(s.graph)
  draft.nodes[3].input.value.text = 'Jump.'
  const preview = executionPreview(s, draft)
  assert.deepEqual([...preview.dirty], ['description', 'motion', 'retarget'])
  assert.equal(preview.next, 'description')
  assert.match(executionIssue(s, draft, 'retarget', models), /Save changes/)
  assert.equal(executionIssue(s, draft, 'rig', models), '')
})

test('a stale or unselected secondary input blocks retargeting', () => {
  const s = snapshot()
  assert.equal(executionIssue(s, null, 'retarget', models), '')
  for (const status of ['stale', 'waiting', 'failed']) {
    s.nodes.motion.status = status
    assert.match(executionIssue(s, null, 'retarget', models), /upstream input/)
  }
  s.nodes.motion = { status: 'succeeded' }
  assert.match(executionIssue(s, null, 'retarget', models), /upstream input/)
})

test('missing, future and mistyped secondary bindings invalidate the graph', () => {
  for (const binding of [undefined, { from: 'retarget' }, { from: 'rig' }, { value: { type: 'text', text: 'walk' } }, { from: 'motion', value: { type: 'motion' } }]) {
    const s = snapshot()
    s.graph.nodes.at(-1).inputs = binding ? { motion: binding } : {}
    assert.notEqual(graphProblem(s.graph), '')
    assert.deepEqual(activeConnections(s.graph), [])
  }
})

test('uploaded motion and mesh inputs need artifacts before running', () => {
  const s = snapshot(), node = s.graph.nodes.at(-1)
  node.input = { value: { type: 'mesh' } }
  node.inputs.motion = { value: { type: 'motion' } }
  assert.deepEqual(readiness(node, models), ['Upload or choose a 3D asset GLB.', 'Upload or choose a motion GLB.'])
})

test('text-to-mesh engines offer earlier text inputs instead of image inputs', () => {
  const graph = { target: { inputs: ['text'] }, nodes: [
    { id: 'source', kind: 'source', input: { value: { type: 'text', text: 'tower' } } },
    { id: 'image', kind: 'image', input: { from: 'source' } },
    { id: 'mesh', kind: 'mesh', input: { from: 'source' } },
  ], primary: 'mesh' }
  assert.deepEqual(inputChoices(graph, graph.nodes.at(-1)).map(n => n.id), ['source'])
  assert.equal(graphProblem(graph), '')
})

test('image editing accepts an optional image reference and tracks its dependency', () => {
  const reference = { id: 'ref', kind: 'extract', input: { value: { type: 'image', artifact: 'original' } } }
  const edit = { id: 'edit', kind: 'image', model: 'editor', input: { value: { type: 'text', text: 'Make red' } }, inputs: { reference: { from: 'ref' } } }
  const graph = { primary: 'edit', nodes: [reference, edit] }
  assert.equal(graphProblem(graph), '')
  assert.deepEqual(inputChoices(graph, edit, 'reference'), [reference])
  assert.deepEqual([...ancestors(graph, 'edit')], ['edit', 'ref'])
  edit.inputs.reference = { value: { type: 'image' } }
  assert.ok(readiness(edit, [{ id: 'editor', capabilities: ['image'] }]).includes('Upload or choose the reference image.'))
  edit.inputs.reference.value.type = 'mesh'
  assert.match(graphProblem(graph), /type-compatible reference/)
  delete edit.inputs.reference
  assert.equal(graphProblem(graph), '')
})
