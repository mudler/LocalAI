import test from 'node:test'
import assert from 'node:assert/strict'
import { basePathFromHref } from '../../src/utils/basePath.js'

test('basePath: a path-only base href resolves against the page', () => {
  assert.equal(basePathFromHref('/', 'https://host.test/invite/abc'), '')
  assert.equal(basePathFromHref('/llm/', 'https://host.test/llm/app/chat/x'), '/llm')
})

test('basePath: a full-URL base href keeps its path', () => {
  assert.equal(basePathFromHref('http://host.test/llm/', 'https://host.test/llm/app'), '/llm')
  assert.equal(basePathFromHref('http://host.test/', 'https://host.test/app'), '')
})

test('basePath: a missing or broken base href gives no prefix', () => {
  assert.equal(basePathFromHref('', 'https://host.test/app'), '')
  assert.equal(basePathFromHref(null, 'https://host.test/app'), '')
  assert.equal(basePathFromHref('/llm/', 'not a url'), '')
})
