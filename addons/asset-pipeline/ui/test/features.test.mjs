// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { animationIssue, isAnimationNode, readAnimationPreference, writeAnimationPreference } from '../src/features.js'

test('animation is an opt-in browser preference and survives unavailable storage', () => {
  const original = globalThis.localStorage
  try {
    globalThis.localStorage = { getItem() { throw Error('blocked') }, setItem() { throw Error('blocked') } }
    assert.equal(readAnimationPreference(), false)
    assert.doesNotThrow(() => writeAnimationPreference(true))
    let stored = null
    globalThis.localStorage = { getItem: () => stored, setItem: (_, value) => { stored = value } }
    assert.equal(readAnimationPreference(), false)
    writeAnimationPreference(true)
    assert.equal(readAnimationPreference(), true)
    writeAnimationPreference(false)
    assert.equal(readAnimationPreference(), false)
  } finally { globalThis.localStorage = original }
})

test('the flag gates animation dependencies without altering saved graphs', () => {
  const graph = { primary: 'mesh', nodes: [
    { id: 'mesh', kind: 'mesh', input: { value: { type: 'image' } } },
    { id: 'rig', kind: 'rig', input: { from: 'mesh' } },
    { id: 'remesh', kind: 'remesh', input: { from: 'rig' } },
  ] }
  const original = structuredClone(graph)
  assert.equal(animationIssue(graph, false), '')
  assert.match(animationIssue(graph, false, 'remesh'), /Enable Animation/)
  assert.equal(animationIssue(graph, true, 'remesh'), '')
  assert.deepEqual(graph, original)
  for (const kind of ['rig', 'skin', 'motion', 'retarget']) assert.equal(isAnimationNode({kind}), true)
  assert.equal(isAnimationNode({ id: 'motion-description', kind: 'source' }), true)
  assert.equal(isAnimationNode({ kind: 'review', params: {purpose: 'motion'} }), true)
  for (const kind of ['image','extract','mesh','remesh','source','review']) assert.equal(isAnimationNode({kind}), false)
})
