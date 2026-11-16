import { compatibleModel, parameterIssues } from './stages.js'

export function inputType(node) {
  if (['remesh', 'rig', 'skin', 'retarget'].includes(node.kind)) return 'mesh'
  return ['extract', 'mesh'].includes(node.kind) ? 'image' : 'text'
}

export function outputType(node) {
  if (['mesh', 'remesh', 'rig', 'skin', 'retarget'].includes(node.kind)) return 'mesh'
  if (node.kind === 'motion') return 'motion'
  return ['source', 'review'].includes(node.kind) ? 'text' : 'image'
}

export function inputTypes(graph, node, name = '') {
  if (node.kind === 'image' && name === 'reference') return ['image']
  if (name) return node.kind === 'skin' && name === 'skeleton' ? ['mesh', 'motion'] : node.kind === 'retarget' && name === 'motion' ? ['motion'] : []
  return node.kind === 'mesh' ? graph.target?.inputs || ['image'] : [inputType(node)]
}

export function dependencies(node) {
  return [...new Set([node?.input, ...Object.values(node?.inputs || {})].map(binding => binding?.from).filter(Boolean))]
}

export function ancestors(graph, id) {
  const result = new Set()
  function visit(next) {
    if (!next || result.has(next)) return
    result.add(next)
    dependencies(graph.nodes.find(n => n.id === next)).forEach(visit)
  }
  visit(id)
  return result
}

// Connections follow the visible stage order and never offer an invalid type.
export function inputChoices(graph, node, name = '') {
  if (node.kind === 'source') return []
  return graph.nodes.slice(0, graph.nodes.findIndex(n => n.id === node.id))
    .filter(n => inputTypes(graph, node, name).includes(outputType(n)))
}

export function isProcessing(node) { return node.kind !== 'source' }

export function needsModel(node) {
  return isProcessing(node) && !(node.kind === 'extract' && (node.params?.mode || 'keep') === 'keep')
}

export function readiness(node, models = [], imageArtifact = '') {
  const issues = []
  if (node.input.value?.type === 'text' && !node.input.value.text?.trim()) issues.push('Enter a description.')
  if (node.input.value?.type === 'image' && !node.input.value.artifact) issues.push('Upload or choose an image.')
  for (const [name, binding] of Object.entries(node.inputs || {})) {
    if (binding.value?.type === 'image' && !binding.value.artifact) issues.push(`Upload or choose the ${name} image.`)
  }
  for (const binding of [node.input, ...Object.values(node.inputs || {})]) {
    if (['mesh', 'motion'].includes(binding.value?.type) && !binding.value.artifact) issues.push(`Upload or choose a ${binding.value.type === 'motion' ? 'motion' : '3D asset'} GLB.`)
  }
  if (needsModel(node) && !node.model) issues.push('Select a model.')
  else if (needsModel(node) && !models.some(m => m.id === node.model)) issues.push('The selected model is no longer available. Choose another model.')
  else if (needsModel(node)) {
    const model = models.find(m => m.id === node.model)
    if ((model?.capabilities || model?.three_d_operations) && !compatibleModel(model, node)) issues.push('The selected model does not support this stage. Choose a compatible model.')
  }
  if (node.kind === 'extract' && node.params?.mode === 'segment' && !pointCoordinates(node.params?.points)) issues.push('Correct the point coordinates or clear them.')
  if (node.kind === 'extract' && node.params?.mode === 'segment' && pointCoordinates(node.params?.points)?.length && node.params.points_image && node.params.points_image !== imageArtifact) issues.push('Input image changed. Clear the old segmentation points before continuing.')
  issues.push(...parameterIssues(models.find(m => m.id === node.model), node))
  return issues
}

export function ambiguousFailure(error) {
  return error?.type === 'outcome_unknown' || /outcome_unknown|failed to fetch|network|timeout|timed out/i.test(error?.message || '')
}

export function pointCoordinates(raw) {
  try {
    const values = JSON.parse(raw || '[]')
    if (!Array.isArray(values) || values.length % 3 || values.some((n, i) => !Number.isFinite(n) || n < 0 || (i % 3 === 2 && n !== 0 && n !== 1))) return null
    return values
  } catch { return null }
}

export function graphProblem(graph) {
  if (!graph) return ''
  if (graph.nodes.find(n => n.id === graph.primary)?.kind === 'source') return 'Choose a processing stage as the primary output.'
  for (const node of graph.nodes) {
    if (node.kind === 'source' && node.input.value?.type !== 'text') return 'Asset description must be text. Upload this image on Object extraction or 3D generation instead.'
    const parent = graph.nodes.find(n => n.id === node.input.from)
    const type = parent ? outputType(parent) : node.input.value?.type
    if (node.kind === 'mesh' && graph.target?.inputs && !graph.target.inputs.includes(type)) return `3D generation: incompatible ${type} input for the selected engine.`
    if (node.input.from && !inputChoices(graph, node).some(n => n.id === parent?.id)) return 'Choose an earlier, type-compatible input for each stage.'
    if (!inputTypes(graph, node).includes(type)) return 'Choose a type-compatible input for each stage.'
    const required = node.kind === 'skin' ? 'skeleton' : node.kind === 'retarget' ? 'motion' : ''
    if (required && !node.inputs?.[required]) return `Choose a ${required} input for this stage.`
    for (const [name, binding] of Object.entries(node.inputs || {})) {
      if (!inputTypes(graph, node, name).length) return `Unsupported input: ${name}.`
      if (!binding || (!!binding.from === !!binding.value)) return `Choose exactly one ${name} input.`
      if (binding.from && !inputChoices(graph, node, name).some(n => n.id === binding.from)) return `Choose an earlier, type-compatible ${name} input.`
      if (binding.value && !inputTypes(graph, node, name).includes(binding.value.type)) return `Choose a type-compatible ${name} input.`
    }
  }
  return ''
}

// The primary output owns the visible chain; unused stages never imply a link.
export function activeConnections(graph) {
  if (!graph || graphProblem(graph)) return []
  const active = ancestors(graph, graph.primary)
  return graph.nodes.filter(node => active.has(node.id))
    .flatMap(node => dependencies(node).map(from => ({ from, to: node.id })))
}

// Give overlapping edges distinct lanes. Adjacent chain edges can reuse a lane,
// while a long join bypasses the shorter branch visibly on the outside.
export function connectionLanes(graph) {
  const positions = new Map((graph?.nodes || []).map((node, index) => [node.id, index]))
  const edges = activeConnections(graph).map(edge => ({ ...edge, start: positions.get(edge.from), end: positions.get(edge.to) }))
  const assigned = []
  for (const edge of [...edges].sort((a, b) => (a.end - a.start) - (b.end - b.start))) {
    const occupied = new Set(assigned.filter(other => edge.start < other.end && other.start < edge.end).map(other => other.lane))
    edge.lane = 0
    while (occupied.has(edge.lane)) edge.lane++
    assigned.push(edge)
  }
  return edges.map(({ from, to, lane }) => ({ from, to, lane }))
}

// Run continues through reusable successes, but stops at the first decision.
export function runPlan(snapshot, draft) {
  const graph = draft || snapshot?.graph
  if (!graph || graphProblem(graph)) return []
  const preview = executionPreview(snapshot, draft)
  if (!preview.next || preview.blocked) return []
  const result = []
  for (const node of graph.nodes) {
    if (!preview.active.has(node.id)) continue
    const status = preview.dirty.has(node.id) ? 'stale' : snapshot?.nodes?.[node.id]?.status
    if (status === 'succeeded') continue
    if (['running', 'waiting', 'failed', 'interrupted'].includes(status)) break
    result.push(node.id)
    if (node.pause || (node.kind === 'image' && node.count > 1)) break
  }
  return result
}

// Preview execution order for unsaved edits without changing durable history.
export function executionPreview(snapshot, draft) {
  const graph = draft || snapshot?.graph
  const nodes = snapshot?.nodes || {}
  const active = graph ? ancestors(graph, graph.primary) : new Set(), dirty = new Set()
  if (draft && snapshot) {
    for (const node of graph.nodes) {
      const previous = snapshot.graph.nodes.find(n => n.id === node.id)
      if (JSON.stringify({ ...node, pause: false }) !== JSON.stringify({ ...previous, pause: false })) dirty.add(node.id)
    }
    for (let changed = true; changed;) {
      changed = false
      for (const node of graph.nodes) {
        if (dependencies(node).some(id => dirty.has(id)) && !dirty.has(node.id)) { dirty.add(node.id); changed = true }
      }
    }
  }
  let next = '', blocked = false
  for (const { id } of graph?.nodes || []) {
    if (!active.has(id)) continue
    const status = dirty.has(id) ? 'stale' : nodes[id]?.status
    if (status === 'running' || status === 'waiting') break
    if (status !== 'succeeded') { next = id; blocked = status === 'failed' || status === 'interrupted'; break }
  }
  return { active, dirty, next, blocked }
}
