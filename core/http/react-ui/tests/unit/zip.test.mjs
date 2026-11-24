import assert from 'node:assert/strict'
import test from 'node:test'

import { crc32, makeZip, zipNames } from '../../src/utils/zip.js'

const bytes = (s) => new TextEncoder().encode(s)

test('crc32 matches the known check value', () => {
  assert.equal(crc32(bytes('123456789')), 0xcbf43926)
  assert.equal(crc32(new Uint8Array(0)), 0)
})

test('makeZip writes a readable archive with each name and body', async () => {
  const blob = makeZip([
    { name: '/a/one.txt', data: bytes('hello') },
    { name: 'b/dos é.txt', data: bytes('') },
  ], new Date(2026, 0, 2, 3, 4, 6))
  assert.equal(blob.type, 'application/zip')
  const buf = new DataView(await blob.arrayBuffer())
  const end = buf.byteLength - 22
  assert.equal(buf.getUint32(end, true), 0x06054b50)
  assert.equal(buf.getUint16(end + 10, true), 2)
  let p = buf.getUint32(end + 16, true)
  const found = []
  for (let i = 0; i < 2; i++) {
    assert.equal(buf.getUint32(p, true), 0x02014b50)
    const size = buf.getUint32(p + 24, true)
    const nameLen = buf.getUint16(p + 28, true)
    const local = buf.getUint32(p + 42, true)
    const name = new TextDecoder().decode(new Uint8Array(buf.buffer, p + 46, nameLen))
    const start = local + 30 + nameLen
    const body = new Uint8Array(buf.buffer, start, size)
    assert.equal(buf.getUint32(p + 16, true), crc32(body))
    found.push([name, new TextDecoder().decode(body)])
    p += 46 + nameLen
  }
  assert.deepEqual(found, [['a/one.txt', 'hello'], ['b/dos é.txt', '']])
})

test('zipNames drops the folder that all files share and keeps the rest', () => {
  assert.deepEqual(zipNames(['/chats/x/work/a.txt', '/chats/x/work/sub/b.txt']), ['a.txt', 'sub/b.txt'])
  assert.deepEqual(zipNames(['/summary.docx', '/chats/x/rows.json']), ['summary.docx', 'chats/x/rows.json'])
  assert.deepEqual(zipNames(['/only/one.txt']), ['one.txt'])
  assert.deepEqual(zipNames([]), [])
})
