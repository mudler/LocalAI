// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import * as THREE from 'three'
import { createRigInspection, jointWeightColors } from '../src/rig-inspection.js'

test('weight colors use joint indices and normalized weights, not slot order', () => {
  const indices = new THREE.Uint16BufferAttribute([1, 0, 0, 0, 0, 1, 0, 0], 4)
  const weights = new THREE.Uint8BufferAttribute([255, 0, 0, 0, 255, 0, 0, 0], 4, true)
  assert.deepEqual([...jointWeightColors(indices, weights, 1)], [1, 0, 0, 0, 0, 1])
  assert.deepEqual([...jointWeightColors(indices, weights, -1)], [0, 0, 1, 0, 0, 1])
})

test('rig inspection overlays bones and restores original geometry/materials', () => {
  const root = new THREE.Group(), hip = new THREE.Bone(), tip = new THREE.Bone()
  hip.name = 'Hip'; tip.name = 'Tip'; hip.add(tip); root.add(hip)
  const geometry = new THREE.BufferGeometry()
  geometry.setAttribute('position', new THREE.Float32BufferAttribute([0, 0, 0], 3))
  geometry.setAttribute('skinIndex', new THREE.Uint16BufferAttribute([1, 0, 0, 0], 4))
  geometry.setAttribute('skinWeight', new THREE.Float32BufferAttribute([1, 0, 0, 0], 4))
  const material = new THREE.MeshStandardMaterial()
  const mesh = new THREE.SkinnedMesh(geometry, material)
  mesh.bind(new THREE.Skeleton([hip, tip])); root.add(mesh)
  const rig = createRigInspection(root)
  assert.equal(rig.helper.material.depthTest, false)
  assert.equal(rig.state().skeleton, true)
  assert.deepEqual(rig.state().joints, ['1: Hip', '2: Tip'])
  rig.setMode('weights'); rig.setJoint(1)
  assert.deepEqual([...mesh.geometry.attributes.color.array], [1, 0, 0])
  assert.equal(geometry.attributes.color, undefined)
  rig.setJoint(0)
  assert.deepEqual([...mesh.geometry.attributes.color.array], [0, 0, 1])
  rig.setSkeleton(false)
  assert.equal(rig.helper.visible, false)
  rig.setMode('mesh')
  assert.equal(mesh.geometry, geometry); assert.equal(mesh.material, material)
  rig.setMode('weights')
  let disposed = false
  mesh.material.addEventListener('dispose', () => { disposed = true })
  rig.dispose()
  assert.equal(disposed, true)
  assert.equal(mesh.geometry, geometry); assert.equal(mesh.material, material)
  geometry.dispose(); material.dispose(); mesh.skeleton.dispose()
})
