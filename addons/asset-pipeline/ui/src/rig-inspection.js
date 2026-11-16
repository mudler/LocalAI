// SPDX-License-Identifier: MIT
import * as THREE from 'three'

export function jointWeightColors(indices, weights, joint) {
  const colors = new Float32Array(weights.count * 3)
  for (let vertex = 0; vertex < weights.count; vertex++) {
    let weight = 0
    for (let slot = 0; slot < weights.itemSize; slot++) {
      if (indices.getComponent(vertex, slot) === joint) weight += weights.getComponent(vertex, slot)
    }
    weight = Math.max(0, Math.min(1, weight))
    // Blue (zero) through cyan and yellow to red (one), without lighting/textures.
    colors[vertex * 3] = Math.max(0, Math.min(1, weight * 3 - 1))
    colors[vertex * 3 + 1] = Math.min(1, weight * 3, (1 - weight) * 3)
    colors[vertex * 3 + 2] = Math.max(0, Math.min(1, 2 - weight * 3))
  }
  return colors
}

export function createRigInspection(root) {
  const meshes = [], bones = []
  root.traverse(object => {
    if (!object.isSkinnedMesh) return
    meshes.push({ object, geometry: object.geometry, material: object.material })
    for (const bone of object.skeleton.bones) if (!bones.includes(bone)) bones.push(bone)
  })
  const weighted = meshes.filter(({ geometry }) => geometry.attributes.skinIndex && geometry.attributes.skinWeight)
  const helper = bones.length ? new THREE.SkeletonHelper(root) : null
  if (helper) {
    helper.material.depthTest = false
    helper.material.depthWrite = false
    helper.material.toneMapped = false
    helper.renderOrder = 10
  }
  let mode = 'mesh', joint = 0
  function restore() {
    for (const entry of meshes) {
      entry.object.geometry = entry.geometry
      entry.object.material = entry.material
    }
  }
  function apply() {
    restore()
    if (mode !== 'weights') return
    for (const entry of weighted) {
      if (!entry.preview) {
        entry.preview = entry.geometry.clone()
        entry.heatmap = new THREE.MeshBasicMaterial({ vertexColors: true, side: THREE.DoubleSide, toneMapped: false })
      }
      const index = entry.object.skeleton.bones.indexOf(bones[joint])
      const { skinIndex, skinWeight } = entry.geometry.attributes
      entry.preview.setAttribute('color', new THREE.BufferAttribute(jointWeightColors(skinIndex, skinWeight, index), 3))
      entry.object.geometry = entry.preview
      entry.object.material = entry.heatmap
    }
  }
  return {
    helper,
    bones,
    state: () => ({ mode, joint, skeleton: helper?.visible || false, hasWeights: weighted.length > 0, joints: bones.map((bone, index) => `${index + 1}: ${bone.name || 'Joint'}`) }),
    setSkeleton(value) { if (helper) helper.visible = !!value },
    setMode(value) { if (!['mesh', 'weights'].includes(value) || (value === 'weights' && !weighted.length)) return; mode = value; apply() },
    setJoint(index) { if (!Number.isInteger(index) || !bones[index]) return; joint = index; apply() },
    dispose() {
      restore()
      for (const entry of meshes) { entry.preview?.dispose(); entry.heatmap?.dispose() }
      if (helper) { helper.removeFromParent(); helper.geometry.dispose(); helper.material.dispose() }
    },
  }
}
