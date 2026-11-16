import test from 'node:test'
import assert from 'node:assert/strict'
import { unpack } from '../src/response.js'

test('real resource apply exposes the newly forked branch without rereading the original', () => {
 const payload = { view: { branch: 'retry-branch', next: 'review' } }
 assert.equal(unpack({ resourceId: 'pipeline', revision: { data: 4 }, result: payload }), payload)
 assert.equal(unpack({ payload }), payload)
 assert.equal(unpack({ result: { payload } }), payload)
 assert.equal(unpack(payload), payload)
})

test('real upload apply exposes the artifact ID', () => {
 assert.equal(unpack({ result: { id: 'uploaded-image' } }).id, 'uploaded-image')
})
