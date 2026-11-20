export const labels = { source: 'Asset description', review: 'Prompt review', image: 'Image generation', extract: 'Object extraction', mesh: '3D generation', remesh: 'Remeshing', rig: 'Generate rig and weights', skin: 'Skin to skeleton', motion: 'Motion generation', retarget: 'Apply motion' }
export const title = node => node.id === 'motion-description' ? 'Motion description' : node.kind === 'review' && node.params?.purpose === 'motion' ? 'Motion prompt review' : labels[node.kind] || node.kind
export const descriptions = { source: 'Describe the asset you want. This text can feed prompt review or image generation.', review: 'Check and refine the description for your chosen 3D engine.', image: 'Generate candidates, then choose the image to use.', extract: 'Use the image as it is, or isolate an object before 3D generation.', mesh: 'Turn an image into a downloadable 3D asset.', remesh: 'Rebuild topology without loading generation weights. Quad counts are density hints; GLB stores triangles and may have open boundaries.', rig: 'Generate a skeleton and learned skinning weights for the mesh. Material resources are retained where supported.', skin: 'Alternative to generating a rig: learn weights for a supplied skeleton or motion skeleton.', motion: 'Generate a short human-motion clip from text. This produces a skeleton animation, not an animated mesh.', retarget: 'Apply SOMA30 motion to a compatible humanoid rig. Unsupported skeletons fail explicitly; arbitrary creatures are not supported.' }

export function stageOperation(model, node) {
  const id = { mesh: 'generate', remesh: 'remesh', rig: 'rig', motion: 'animate', retarget: 'retarget' }[node.kind]
  return model?.three_d_operations?.find(o => node.kind === 'skin' ? ['skin', 'skin_motion'].includes(o.id) : o.id === id)
}

export function compatibleModel(model, node) {
  if (model.disabled || model.alias_error) return false
  const cap = { review: 'chat', image: 'image', extract: 'detection' }[node.kind]
  if (cap) return model.capabilities?.some(c => c.toLowerCase().replace(/^flag_/, '') === cap) || false
  const op = stageOperation(model, node)
  if (node.kind === 'remesh') return !!op && !model.three_d_operations.some(o => o.id === 'generate')
  if (node.kind === 'motion') return !!op && op.inputs?.length === 1 && op.inputs[0].type === 'text'
  if (node.kind === 'mesh' && !model.three_d_operations?.length) return model.capabilities?.some(c => ['FLAG_3D', '3d'].includes(c)) || false
  return !!op
}

export function stageParameters(model, node) {
  if (node.kind === 'remesh') return [
    { name: 'mode', label: 'Remeshing mode', type: 'enum', default: 'wrap_quad', options: ['wrap_quad', 'wrap', 'quad'] },
    { name: 'alpha_ratio', label: 'Wrap detail (fraction of diagonal)', type: 'number', default: '0.005', min: 0.0035, max: 0.025 },
    { name: 'target_quads', label: 'Target quads (density hint)', type: 'integer', default: '20000', min: 100, max: 100000 },
    { name: 'adaptivity', label: 'Curvature adaptivity', type: 'number', default: '1', min: 0, max: 1 },
    { name: 'texture_size', label: 'Texture size', type: 'integer', default: '2048', min: 256, max: 4096 },
  ]
  return (stageOperation(model, node)?.parameters || []).filter(p => p.name !== 'seed')
}

export function parameterIssues(model, node) {
  const issues = []
  if (['image', 'mesh', 'rig', 'skin', 'motion'].includes(node.kind) && (!Number.isSafeInteger(node.seed ?? 0) || (node.seed ?? 0) < 0 || (node.seed ?? 0) > 2147483647)) issues.push('Seed must be a whole number from 0 to 2147483647.')
  for (const p of stageParameters(model, node)) {
    const raw = node.params?.[p.name] ?? p.default
    if (raw === undefined) continue
    if (p.type === 'enum') {
      if (!p.options?.includes(raw)) issues.push(`${p.label || p.name}: choose a supported option.`)
      continue
    }
    const number = Number(raw), min = p.min ?? 0
    if (String(raw).trim() === '' || !Number.isFinite(number) || number < min || (p.max && number > p.max) || (p.type === 'integer' && (!/^[+-]?\d+$/.test(String(raw)) || !Number.isSafeInteger(number)))) {
      issues.push(`${p.label || p.name}: enter ${p.type === 'integer' ? 'a whole number' : 'a number'} ${p.max ? `from ${min} to ${p.max}` : `of at least ${min}`}.`)
    }
  }
  return issues
}
