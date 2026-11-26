import assert from 'node:assert/strict'
import test from 'node:test'

import { curlExample } from './chatTrigger.js'

test('a match path cannot reach the prototype', () => {
  for (const m of ['__proto__.polluted=yes', 'a.constructor.prototype.polluted=yes', 'prototype.polluted=yes']) {
    curlExample('http://x/hook', 's', { matches: [m] })
  }
  assert.equal({}.polluted, undefined)
})

test('a match path still builds a nested body', () => {
  const out = curlExample('http://x/hook', 's', { matches: ['pull_request.state=open'] })
  assert.match(out, /pull_request/)
})
