import { graphProblem, inputType, isProcessing, readiness, ancestors } from './state.js'
export { ancestors } from './state.js'

export const same = (a, b) => JSON.stringify(a) === JSON.stringify(b)
const copy = value => value === undefined ? undefined : structuredClone(value)

export function nodeChanged(savedGraph, draft, id) {
  if (!draft) return false
  const node = draft.nodes.find(n => n.id === id)
  return !same(savedGraph.nodes.find(n => n.id === id), node)
    || (node?.kind === 'mesh' && !same(savedGraph.target, draft.target))
    || (draft.primary === id && draft.primary !== savedGraph.primary)
}

export function graphForNode(savedGraph, draft, id) {
  const graph = copy(savedGraph)
  if (!draft) return graph
  const node = draft.nodes.find(n => n.id === id)
  graph.nodes = graph.nodes.map(n => n.id === id ? copy(node) : n)
  if (node?.kind === 'mesh') graph.target = copy(draft.target)
  if (draft.primary === id) graph.primary = id
  return graph
}

// Rebase edits made while a request was in flight without reviving saved changes.
export function rebaseEdits(baseGraph, draftGraph, newSavedGraph) {
  if (!draftGraph) return null
  function overlay(base, draft, saved) {
    if (same(base, draft)) return copy(saved)
    if (!base || !draft || Array.isArray(draft) || typeof draft !== 'object') return copy(draft)
    const result = copy(saved) || {}
    for (const key of new Set([...Object.keys(base), ...Object.keys(draft)])) {
      if (same(base[key], draft[key])) continue
      if (!(key in draft)) delete result[key]
      else result[key] = overlay(base[key], draft[key], saved?.[key])
    }
    return result
  }
  const result = overlay({ ...baseGraph, nodes: undefined }, { ...draftGraph, nodes: undefined }, newSavedGraph)
  result.nodes = newSavedGraph.nodes.map(node => overlay(baseGraph.nodes.find(n => n.id === node.id), draftGraph.nodes.find(n => n.id === node.id), node))
  return same(result, newSavedGraph) ? null : result
}

function structuralIssue(graph) {
  if (!graph?.nodes?.length) return 'Configure a pipeline first.'
  if (!graph.nodes.some(n => n.id === graph.primary && isProcessing(n))) return 'Choose a processing stage as the primary output.'
  if (new Set(graph.nodes.map(n => n.id)).size !== graph.nodes.length) return 'Each stage must have a unique identifier.'
  for (const node of graph.nodes) {
    if (!node.input || (!!node.input.from === !!node.input.value)) return 'Choose exactly one input for each stage.'
    const types = node.kind === 'mesh' ? graph.target?.inputs || ['image'] : [inputType(node)]
    if (node.input.value && !types.includes(node.input.value.type)) return 'Choose a type-compatible input for each stage.'
  }
  return graphProblem(graph)
}

function busy(snapshot) {
  return Object.values(snapshot?.nodes || {}).some(n => n.status === 'running') ? 'Wait for the running stage to finish.' : ''
}

function inputArtifact(snapshot, node) {
  return node.input.value?.artifact || snapshot.nodes?.[node.input.from]?.selected?.artifact || ''
}

export function executionIssue(snapshot, draft, nodeId, models = []) {
  const graph = snapshot?.graph
  const issue = structuralIssue(graph) || busy(snapshot)
  if (issue) return issue
  if (draft && draft.primary !== graph.primary) return 'Save the primary output change before running.'
  const node = graph.nodes.find(n => n.id === nodeId)
  if (!node || !isProcessing(node)) return 'This stage supplies input and does not run.'
  if (!ancestors(graph, graph.primary).has(nodeId)) return 'This stage is not part of the active chain.'
  const inputs = ancestors(graph, nodeId)
  if ([...inputs].some(id => nodeChanged(graph, draft, id))) return 'Save changes to this stage and its inputs before running.'
  for (const id of inputs) {
    const input = graph.nodes.find(n => n.id === id)
    const problem = readiness(input, models, inputArtifact(snapshot, input))[0]
    if (problem) return problem
    if (id !== nodeId && isProcessing(input)) {
      const state = snapshot.nodes?.[id]
      if (state?.status !== 'succeeded' || !state.selected) return 'Run and select the upstream input before running this stage.'
    }
  }
  return ''
}

export function runAllIssue(snapshot, draft, models = []) {
  const graph = snapshot?.graph
  const issue = structuralIssue(graph) || busy(snapshot)
  if (issue) return issue
  const active = ancestors(graph, graph.primary)
  if (draft && (draft.primary !== graph.primary || [...active].some(id => nodeChanged(graph, draft, id)))) return 'Save changes to the active chain before running.'
  for (const node of graph.nodes.filter(n => active.has(n.id))) {
    const problem = readiness(node, models, inputArtifact(snapshot, node))[0]
    if (problem) return problem
  }
  return ''
}

export function workbookPlan(snapshot, draft, restart = false) {
  const graph = snapshot?.graph
  if (structuralIssue(graph) || busy(snapshot)) return []
  const active = ancestors(graph, graph.primary)
  if (draft && (draft.primary !== graph.primary || [...active].some(id => nodeChanged(graph, draft, id)))) return []
  const result = []
  for (const node of graph.nodes) {
    if (!active.has(node.id) || !isProcessing(node)) continue
    const status = snapshot.nodes?.[node.id]?.status
    if (!restart && status === 'succeeded') continue
    if (!restart && ['waiting', 'failed', 'interrupted'].includes(status)) break
    result.push(node.id)
    if (node.pause || (node.kind === 'image' && node.count > 1)) break
  }
  return result
}
