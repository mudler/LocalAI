// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { upscaleModels, sourceImageFile } from './upscale.js'
import { toWorkItem, EDGE_KINDS } from './studioWork.js'

test('only enabled upscalers with positive finite scale are offered', () => {
  assert.deepEqual(upscaleModels([
    { id: 'yes', capabilities: ['FLAG_UPSCALE'], upscaleScale: 4 },
    { id: 'image', capabilities: ['FLAG_IMAGE'], upscaleScale: 4 },
    ...[0, -1, undefined, Infinity].map(upscaleScale => ({ capabilities: ['FLAG_UPSCALE'], upscaleScale })),
    { capabilities: ['FLAG_UPSCALE'], upscaleScale: 2, disabled: true },
  ]), [{ id: 'yes', upscaleScale: 4 }])
})

test('URL source is same-origin, checked and named', async () => {
  let called = false
  const fetcher = async (url, options) => {
    called = true
    assert.equal(url, 'https://local.test/generated/source.png')
    assert.equal(options.credentials, 'same-origin')
    return new Response('png', { headers: { 'Content-Type': 'image/png' } })
  }
  const file = await sourceImageFile('/generated/source.png', 'https://local.test/app/studio', fetcher)
  assert.equal(file.name, 'source.png')
  assert.equal(file.type, 'image/png')
  called = false
  await assert.rejects(sourceImageFile('https://other.test/a.png', 'https://local.test/', fetcher), /origin/)
  assert.equal(called, false)
  for (const response of [new Response('bad', { status: 500 }), new Response('html', { headers: { 'Content-Type': 'text/html' } })]) {
    await assert.rejects(sourceImageFile('/a', 'https://local.test/', async () => response))
  }
  await assert.rejects(sourceImageFile('/a', 'https://local.test/', async () => { throw new Error('offline') }))
})

test('upscale history normalizes as a child image', () => {
  assert.ok(EDGE_KINDS.includes('upscale'))
  const item = toWorkItem('images', { id: 'child', parentId: 'source', edge: 'upscale', results: [{ url: '/out.png' }] })
  assert.equal(item.type, 'images')
  assert.equal(item.parentId, 'source')
  assert.equal(item.edge, 'upscale')
})
