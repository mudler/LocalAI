import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createPageHost, isCodeFile, CODE_WRITE_MESSAGE } from '../../src/utils/pagesBridge.js'

// The same table runs in Go (pages.IsCodeFile), so the two cannot drift.
const here = dirname(fileURLToPath(import.meta.url))
const table = JSON.parse(readFileSync(join(here, '../../../../services/studio/pages/testdata/code-files.json'), 'utf8'))

test('page code files: the shared table gives the same verdicts as the server', () => {
  assert.ok(table.length > 30)
  for (const c of table) assert.equal(isCodeFile(c.path), c.code, `path ${JSON.stringify(c.path)}`)
})

function host(write) {
  const sent = []
  const h = createPageHost({ post: (m) => sent.push(m), files: { read: async () => ({}), write, list: async () => [] }, chat: {} })
  return { h, sent }
}

test('page code files: the host refuses code before any request, and passes data files', async () => {
  const calls = []
  const { h, sent } = host(async (p, c) => { calls.push(p); return { path: p, version: 1, size: c.length } })
  await h.receive({ id: 1, method: 'files.write', params: { path: 'app.js', content: 'x' } })
  await h.receive({ id: 2, method: 'files.write', params: { path: 'data.json', content: '{}' } })
  assert.deepEqual(sent[0], { id: 1, error: { code: 'forbidden', message: CODE_WRITE_MESSAGE } })
  assert.equal(sent[1].result.path, 'data.json')
  assert.deepEqual(calls, ['data.json'])
})

test('page code files: a refusal of the server reaches the page as forbidden', async () => {
  const refusal = Object.assign(new Error('pages: a page can write data files only'), { status: 403, body: { error: { type: 'authorization_error' } } })
  const { h, sent } = host(async () => { throw refusal })
  await h.receive({ id: 1, method: 'files.write', params: { path: 'data.json', content: 'x' } })
  assert.equal(sent[0].error.code, 'forbidden')
  assert.match(sent[0].error.message, /data files only/)
})
