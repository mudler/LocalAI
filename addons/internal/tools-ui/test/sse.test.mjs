import test from 'node:test'
import assert from 'node:assert/strict'
import { readSse } from '../src/sse.js'

function stream(chunks) {
  const enc = new TextEncoder()
  return { body: new ReadableStream({ start(c) { for (const x of chunks) c.enqueue(enc.encode(x)); c.close() } }) }
}

test('readSse yields one event per data block, across chunk boundaries', async () => {
  const got = []
  await readSse(stream(['data: {"a":1}\n\nda', 'ta: {"a":2}\n\n']), (e) => got.push(e))
  assert.deepEqual(got, [{ a: 1 }, { a: 2 }])
})

test('readSse ignores comments and events that are not JSON', async () => {
  const got = []
  await readSse(stream([': keep-alive\n\ndata: not json\n\ndata: {"ok":true}\n\n']), (e) => got.push(e))
  assert.deepEqual(got, [{ ok: true }])
})

test('readSse stops when the signal aborts', async () => {
  const ctl = new AbortController()
  const got = []
  const p = readSse(stream(['data: {"a":1}\n\n']), (e) => { got.push(e); ctl.abort() }, ctl.signal)
  await p
  assert.equal(got.length, 1)
})
