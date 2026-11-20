import test from 'node:test'
import assert from 'node:assert/strict'
import { compatibleModel, stageParameters, title, parameterIssues } from '../src/stages.js'
import { executionIssue, runAllIssue } from '../src/workbook.js'
import { readiness } from '../src/state.js'

test('model selection distinguishes generation, remeshing, skinning and motion operations', () => {
  const trellis = { id: 'trellis', capabilities: ['3d'], three_d_operations: [{ id: 'generate' }, { id: 'remesh' }] }
  const remesh = { id: 'geometry', capabilities: ['3d'], three_d_operations: [{ id: 'remesh' }] }
  const skin = { id: 'weights', capabilities: ['3d_animation'], three_d_operations: [{ id: 'rig' }, { id: 'skin' }, { id: 'skin_motion' }, { id: 'retarget' }] }
  const motion = { id: 'kimodo', three_d_operations: [{ id: 'animate', inputs: [{ type: 'text' }], parameters: [{ name: 'frames' }, { name: 'seed' }] }] }
  for (const [kind, expected] of [['mesh', [true, false, false, false]], ['remesh', [false, true, false, false]], ['rig', [false, false, true, false]], ['skin', [false, false, true, false]], ['motion', [false, false, false, true]], ['retarget', [false, false, true, false]]]) {
    assert.deepEqual([trellis, remesh, skin, motion].map(m => compatibleModel(m, { kind })), expected)
  }
  assert.equal(compatibleModel({ capabilities: ['chat'] }, { kind: 'review' }), true)
  assert.equal(compatibleModel({ capabilities: ['FLAG_CHAT'] }, { kind: 'review' }), true)
  assert.equal(compatibleModel({ ...trellis, disabled: true }, { kind: 'mesh' }), false)
  assert.deepEqual(stageParameters(motion, { kind: 'motion' }), [{ name: 'frames' }])
  assert.match(readiness({ kind: 'motion', model: 'weights', input: { value: { type: 'text', text: 'Walk.' } } }, [skin])[0], /does not support/)
})

test('motion description and review are distinct from image conditioning', () => {
  assert.equal(title({ id: 'motion-description', kind: 'source' }), 'Motion description')
  assert.equal(title({ kind: 'review', params: { purpose: 'motion' } }), 'Motion prompt review')
  assert.equal(title({ kind: 'review' }), 'Prompt review')
})

test('invalid numeric settings disable execution, not just HTML validity', () => {
  const model = { id: 'motion', three_d_operations: [{ id: 'animate', inputs: [{ type: 'text' }], parameters: [{ name: 'frames', label: 'Frames', type: 'integer', min: 60, max: 150, default: '150' }] }] }
  const node = { id: 'motion', kind: 'motion', model: 'motion', input: { value: { type: 'text', text: 'Walk.' } }, params: { frames: '1' } }
  const snapshot = { graph: { primary: 'motion', nodes: [node] }, nodes: {} }
  assert.match(executionIssue(snapshot, null, 'motion', [model]), /Frames.*60 to 150/)
  assert.match(runAllIssue(snapshot, null, [model]), /Frames.*60 to 150/)
  for (const frames of ['1', '151', '90.5', 'NaN', 'Infinity', '']) {
    node.params.frames = frames
    assert.equal(parameterIssues(model, node).length, 1)
  }
  delete node.params.frames
  assert.deepEqual(parameterIssues(model, node), [])
  assert.equal(runAllIssue(snapshot, null, [model]), '')
  assert.equal(parameterIssues(null, { kind: 'remesh', params: { mode: 'unknown' } }).length, 1)
})
