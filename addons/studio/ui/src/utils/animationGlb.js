// SPDX-License-Identifier: MIT

// Skeleton GLBs contain animated nodes but no mesh. Keep their reader separate
// from the dense TRELLIS mesh path, whose buffer layouts and rendering differ.
export function parseAnimationGlb(buffer) {
  const view = new DataView(buffer)
  if (buffer.byteLength < 20 || view.getUint32(0, true) !== 0x46546c67 || view.getUint32(4, true) !== 2 || view.getUint32(8, true) !== buffer.byteLength) {
    throw new Error('Invalid animation GLB')
  }
  let document, binary
  for (let offset = 12; offset < buffer.byteLength;) {
    if (offset + 8 > buffer.byteLength) throw new Error('Truncated GLB chunk')
    const length = view.getUint32(offset, true)
    const type = view.getUint32(offset + 4, true)
    offset += 8
    if (length % 4 || offset + length > buffer.byteLength) throw new Error('Invalid GLB chunk length')
    if (type === 0x4e4f534a) document = JSON.parse(new TextDecoder().decode(new Uint8Array(buffer, offset, length)))
    if (type === 0x004e4942) binary = buffer.slice(offset, offset + length)
    offset += length
  }
  if (!document?.nodes?.length || document.nodes.length > 1024 || !binary) throw new Error('GLB has no supported skeleton')
  const nodes = document.nodes.map(node => {
    const translation = node.translation || [0, 0, 0]
    const rotation = node.rotation || [0, 0, 0, 1]
    if (!Array.isArray(translation) || translation.length !== 3 || !translation.every(Number.isFinite) ||
        !Array.isArray(rotation) || rotation.length !== 4 || !rotation.every(Number.isFinite) || Math.hypot(...rotation) < 1e-8) {
      throw new Error('Invalid skeleton transform')
    }
    if (node.matrix || (node.scale !== undefined && (!Array.isArray(node.scale) || node.scale.length !== 3 || node.scale.some(value => value !== 1)))) {
      throw new Error('Unsupported skeleton transform')
    }
    const length = Math.hypot(...rotation)
    return { name: node.name || '', parent: -1, translation, rotation: rotation.map(value => value / length) }
  })
  document.nodes.forEach((node, parent) => {
    for (const child of node.children || []) {
      if (!Number.isInteger(child) || !nodes[child] || nodes[child].parent !== -1 || child === parent) throw new Error('Invalid skeleton hierarchy')
      nodes[child].parent = parent
    }
  })
  const order = [], visiting = new Set(), visited = new Set()
  function visit(index) {
    if (visited.has(index)) return
    if (visiting.has(index)) throw new Error('Cyclic skeleton hierarchy')
    visiting.add(index)
    if (nodes[index].parent >= 0) visit(nodes[index].parent)
    visiting.delete(index)
    visited.add(index)
    order.push(index)
  }
  nodes.forEach((_, index) => visit(index))

  function readAccessor(index, type, components) {
    const accessor = document.accessors?.[index]
    const bytes = document.bufferViews?.[accessor?.bufferView]
    if (!accessor || accessor.type !== type || accessor.componentType !== 5126 || accessor.sparse || !bytes || bytes.buffer !== 0 || bytes.byteStride || !Number.isInteger(accessor.count) || accessor.count < 1 || accessor.count > 100000) {
      throw new Error('Unsupported animation accessor')
    }
    const relative = accessor.byteOffset ?? 0
    const offset = bytes.byteOffset ?? 0
    if (![relative, offset, bytes.byteLength].every(value => Number.isInteger(value) && value >= 0)) throw new Error('Invalid animation buffer view')
    const start = offset + relative
    const size = accessor.count * components * 4
    if (start < 0 || start % 4 || relative < 0 || relative + size > bytes.byteLength || start + size > binary.byteLength) throw new Error('Animation accessor exceeds buffer')
    const values = new Float32Array(binary, start, accessor.count * components)
    if (!values.every(Number.isFinite)) throw new Error('Animation contains non-finite values')
    return values
  }

  const clip = document.animations?.[0]
  if (!clip?.channels?.length) throw new Error('GLB has no animation tracks')
  const tracks = clip.channels.map(channel => {
    const sampler = clip.samplers?.[channel.sampler]
    const path = channel.target?.path
    if (!sampler || !nodes[channel.target?.node] || !['rotation', 'translation'].includes(path) || !['LINEAR', 'STEP'].includes(sampler.interpolation || 'LINEAR')) {
      throw new Error('Unsupported animation track')
    }
    const times = readAccessor(sampler.input, 'SCALAR', 1)
    if (times[0] < 0 || times.some((time, index) => index > 0 && time <= times[index-1])) throw new Error('Invalid animation timeline')
    const components = path === 'rotation' ? 4 : 3
    const values = readAccessor(sampler.output, path === 'rotation' ? 'VEC4' : 'VEC3', components)
    if (values.length !== times.length * components) throw new Error('Animation track length mismatch')
    return { node: channel.target.node, path, times, values, components, interpolation: sampler.interpolation }
  })
  return { nodes, order, tracks, duration: Math.max(...tracks.map(track => track.times.at(-1))) }
}

function multiplyQuaternion(a, b) {
  return [a[3]*b[0]+a[0]*b[3]+a[1]*b[2]-a[2]*b[1], a[3]*b[1]-a[0]*b[2]+a[1]*b[3]+a[2]*b[0], a[3]*b[2]+a[0]*b[1]-a[1]*b[0]+a[2]*b[3], a[3]*b[3]-a[0]*b[0]-a[1]*b[1]-a[2]*b[2]]
}

function rotate(q, vector) {
  const result = multiplyQuaternion(multiplyQuaternion(q, [...vector, 0]), [-q[0], -q[1], -q[2], q[3]])
  return result.slice(0, 3)
}

export function animationPose(animation, time) {
  const local = animation.nodes.map(node => ({ translation: node.translation, rotation: node.rotation }))
  for (const track of animation.tracks) {
    let frame = 0
    while (frame + 1 < track.times.length && track.times[frame+1] <= time) frame++
    const next = Math.min(frame+1, track.times.length-1)
    const fraction = next === frame || track.interpolation === 'STEP' ? 0 : Math.max(0, (time-track.times[frame]) / (track.times[next]-track.times[frame]))
    const a = track.values.subarray(frame*track.components, (frame+1)*track.components)
    const b = track.values.subarray(next*track.components, (next+1)*track.components)
    const sign = track.path === 'rotation' && a.reduce((sum, value, index) => sum + value*b[index], 0) < 0 ? -1 : 1
    let value = Array.from(a, (component, index) => component*(1-fraction) + sign*b[index]*fraction)
    if (track.path === 'rotation') {
      const length = Math.hypot(...value)
      if (length < 1e-8) throw new Error('Invalid animation quaternion')
      value = value.map(component => component/length)
    }
    local[track.node][track.path] = value
  }
  const positions = [], rotations = []
  for (const index of animation.order) {
    const parent = animation.nodes[index].parent
    if (parent < 0) {
      positions[index] = local[index].translation
      rotations[index] = local[index].rotation
    } else {
      rotations[index] = multiplyQuaternion(rotations[parent], local[index].rotation)
      const offset = rotate(rotations[parent], local[index].translation)
      positions[index] = offset.map((value, axis) => value+positions[parent][axis])
    }
  }
  return positions
}
