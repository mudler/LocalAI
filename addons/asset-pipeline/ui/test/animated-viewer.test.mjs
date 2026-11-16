import test from 'node:test'
import assert from 'node:assert/strict'
import { inspectGLB, playableClips } from '../src/animated-viewer.js'

test('static rig clips do not expose playback and do not hide real motion clips', () => {
  const motion = { name: 'walk', duration: 2 }
  assert.deepEqual(playableClips([{ duration: 0 }, { duration: -1 }, { duration: NaN }, { duration: Infinity }, motion]), [motion])
  assert.deepEqual(playableClips([{ duration: 0 }]), [])
})

function glb(doc) {
  let json = JSON.stringify(doc)
  while (json.length % 4) json += ' '
  const out = new ArrayBuffer(20 + json.length), view = new DataView(out)
  for (const [offset, value] of [[0, 0x46546c67], [4, 2], [8, out.byteLength], [12, json.length], [16, 0x4e4f534a]]) view.setUint32(offset, value, true)
  new Uint8Array(out, 20).set(new TextEncoder().encode(json))
  return out
}

test('preview inspection recognizes skeleton-only and skinned GLBs', () => {
  const doc = { asset: { version: '2.0' }, nodes: [{ children: [1] }, { translation: [0, 1, 0] }], animations: [{ name: 'Walk' }] }
  assert.deepEqual(inspectGLB(glb(doc)), doc)
  doc.skins = [{ joints: [0, 1] }]
  assert.equal(inspectGLB(glb(doc)).skins.length, 1)
})

test('preview inspection blocks external resources before the GLTF loader runs', () => {
  for (const uri of ['https://example.com/private.png', '/api/settings', 'file:///private', 'data:image/svg+xml;base64,AAAA']) {
    assert.throws(() => inspectGLB(glb({ asset: { version: '2.0' }, images: [{ uri }] })), /External/)
  }
  assert.doesNotThrow(() => inspectGLB(glb({ asset: { version: '2.0' }, images: [{ uri: 'data:image/png;base64,AAAA' }] })))
})

test('preview inspection bounds metadata and scene size', () => {
  for (const buffer of [new ArrayBuffer(0), glb({ asset: { version: '1.0' } }), glb({ asset: { version: '2.0' }, nodes: Array(4097).fill({}) })]) assert.throws(() => inspectGLB(buffer))
  const buffer = glb({ asset: { version: '2.0' } })
  new DataView(buffer).setUint32(12, 0xffffffff, true)
  assert.throws(() => inspectGLB(buffer), /length/)
})
