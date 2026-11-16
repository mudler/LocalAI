import test from 'node:test'
import assert from 'node:assert/strict'
import { executionPreview, inputChoices, isProcessing, readiness, needsModel, ambiguousFailure, pointCoordinates, graphProblem, activeConnections, runPlan } from '../src/state.js'

test('input choices are earlier compatible outputs and description is not processing', () => {
 const graph = { nodes: ['source','review','image','extract','mesh'].map(kind => ({id:kind,kind})) }
 assert.deepEqual(inputChoices(graph, graph.nodes[0]), [])
 assert.deepEqual(inputChoices(graph, graph.nodes[1]).map(n => n.id), ['source'])
 assert.deepEqual(inputChoices(graph, graph.nodes[2]).map(n => n.id), ['source','review'])
 assert.deepEqual(inputChoices(graph, graph.nodes[3]).map(n => n.id), ['image'])
 assert.deepEqual(inputChoices(graph, graph.nodes[4]).map(n => n.id), ['image','extract'])
 assert.equal(isProcessing(graph.nodes[0]), false)
})

test('readiness prevents empty inputs and missing models without requiring passthrough models', () => {
 assert.deepEqual(readiness({kind:'source',input:{value:{type:'text',text:'  '}}}), ['Enter a description.'])
 const extract = {kind:'extract',params:{mode:'keep'},input:{value:{type:'image'}}}
 assert.equal(needsModel(extract), false)
 assert.deepEqual(readiness(extract), ['Upload or choose an image.'])
 extract.params.mode = 'segment'
 assert.deepEqual(readiness(extract), ['Upload or choose an image.', 'Select a model.'])
})

test('only uncertain failures offer reconciliation', () => {
 assert.equal(ambiguousFailure(new Error('input text is empty')), false)
 assert.equal(ambiguousFailure(new Error('Failed to fetch')), true)
 assert.equal(ambiguousFailure({type:'outcome_unknown'}), true)
})

test('segmentation points are numeric triples with foreground or background labels', () => {
 assert.deepEqual(pointCoordinates('[12,20,1,40,50,0]'), [12,20,1,40,50,0])
 assert.deepEqual(pointCoordinates(''), [])
 for (const raw of ['[1,2]', '[1,2,3]', '["x",2,1]', 'null', '{}', '[-1,2,1]', 'invalid']) assert.equal(pointCoordinates(raw), null)
})

test('segmentation guidance cannot silently move onto a different input image', () => {
 const node = { kind:'extract', model:'sam', params:{mode:'segment',points:'[1,2,1]',points_image:'image-a'}, input:{from:'image'} }
 assert.deepEqual(readiness(node,[{id:'sam'}],'image-a'), [])
 assert.match(readiness(node,[{id:'sam'}],'image-b')[0], /Input image changed/)
})

test('incompatible engines are reported before saving or invoking inference', () => {
 const graph = {primary:'mesh',target:{inputs:['text']},nodes:[{id:'mesh',kind:'mesh',input:{value:{type:'image',artifact:'x'}}}]}
 assert.match(graphProblem(graph), /incompatible image input/)
 graph.target.inputs = ['image']
 assert.equal(graphProblem(graph), '')
})

test('changing pause policy does not stale valid outputs', () => {
 const saved = snapshot(), draft = structuredClone(saved.graph)
 draft.nodes[1].pause = true
 assert.equal(executionPreview(saved,draft).dirty.size, 0)
})

const snapshot = () => ({ graph: { primary: 'mesh', target: { advice: 'old' }, nodes: [
 { id: 'source', input: { value: { text: 'tower' } } },
 { id: 'review', input: { from: 'source' } },
 { id: 'image', input: { from: 'review' } },
 { id: 'mesh', input: { from: 'image' } },
 { id: 'unused', input: { value: { text: 'other' } } },
] }, nodes: Object.fromEntries(['source','review','image','mesh','unused'].map(id => [id, { status: 'succeeded' }])) })

test('editing an ancestor marks its descendants stale and makes it next immediately', () => {
 const saved = snapshot(), draft = structuredClone(saved.graph)
 draft.nodes[1].model = 'changed'
 const view = executionPreview(saved, draft)
 assert.deepEqual([...view.dirty], ['review','image','mesh'])
 assert.equal(view.next, 'review')
 assert.equal(view.active.has('unused'), false)
 assert.equal(saved.nodes.review.status, 'succeeded')
})
test('an interaction pause blocks downstream inference until a decision', () => {
 const saved = snapshot(); saved.nodes.review.status = 'waiting'; saved.nodes.image.status = 'stale'
 assert.equal(executionPreview(saved).next, '')
 const draft = structuredClone(saved.graph); draft.nodes[0].input.value.text = 'castle'
 assert.equal(executionPreview(saved, draft).next, 'source')
})
test('primary output limits execution to its dependencies', () => {
 const saved = snapshot(), draft = structuredClone(saved.graph)
 draft.primary = 'unused'; draft.nodes[0].input.value.text = 'changed'
 assert.deepEqual([...executionPreview(saved, draft).active], ['unused'])
 assert.equal(executionPreview(saved, draft).next, '')
})
test('advisory profile changes do not invent reverse dependencies', () => {
 const saved = snapshot(), draft = structuredClone(saved.graph); draft.target.advice = 'new'
 assert.equal(executionPreview(saved, draft).dirty.size, 0)
 assert.equal(executionPreview(saved, draft).next, '')
})
test('restoring an edit clears staleness and incomplete loading stays safe', () => {
 const saved = snapshot()
 assert.equal(executionPreview(saved, structuredClone(saved.graph)).dirty.size, 0)
 assert.equal(executionPreview(undefined).next, '')
})
test('failed or interrupted execution requires an explicit fork and retry', () => {
 for (const status of ['failed', 'interrupted']) {
  const saved = snapshot(); saved.nodes.image.status = status
  const view = executionPreview(saved)
  assert.equal(view.next, 'image')
  assert.equal(view.blocked, true)
 }
})

const typedSnapshot = () => ({graph:{primary:'mesh',nodes:[
 {id:'source',kind:'source',input:{value:{type:'text',text:'castle'}}},
 {id:'review',kind:'review',input:{from:'source'},pause:true},
 {id:'image',kind:'image',input:{from:'review'},count:1},
 {id:'extract',kind:'extract',input:{from:'image'}},
 {id:'mesh',kind:'mesh',input:{from:'image'}},
]},nodes:{}})

test('connectors follow the complete primary chain including skipped cards', () => {
 const s=typedSnapshot()
 assert.deepEqual(activeConnections(s.graph),[{from:'source',to:'review'},{from:'review',to:'image'},{from:'image',to:'mesh'}])
 s.graph.nodes[2].input={value:{type:'text',text:'castle'}}
 assert.deepEqual(activeConnections(s.graph),[{from:'image',to:'mesh'}])
 s.graph.nodes[4].input={value:{type:'image',artifact:'upload'}}
 assert.deepEqual(activeConnections(s.graph),[])
})

test('invalid chains do not display misleading arrows or an executable run plan', () => {
 for (const mutate of [s=>s.graph.nodes[1].input={from:'mesh'},s=>s.graph.primary='missing',s=>s.graph.primary='source']) {
  const s=typedSnapshot(); mutate(s)
  assert.deepEqual(activeConnections(s.graph),[])
  assert.deepEqual(runPlan(s),[])
 }
})

test('Run preview stops after the next interactive stage and skips reusable output', () => {
 const s=typedSnapshot()
 assert.deepEqual(runPlan(s),['source','review'])
 s.nodes.source={status:'succeeded'}; s.nodes.review={status:'succeeded'}
 assert.deepEqual(runPlan(s),['image','mesh'])
 s.graph.nodes[2].count=4
 assert.deepEqual(runPlan(s),['image'])
 for(const status of ['waiting','running','failed','interrupted']) {
  s.nodes.image={status}
  assert.deepEqual(runPlan(s),[])
 }
})

test('editing an ancestor updates the Run preview before save', () => {
 const s=typedSnapshot()
 s.nodes=Object.fromEntries(s.graph.nodes.map(n=>[n.id,{status:'succeeded'}]))
 const draft=structuredClone(s.graph); draft.nodes[0].input.value.text='tower'
 assert.deepEqual(runPlan(s,draft),['source','review'])
 assert.deepEqual(runPlan(s),[])
})
