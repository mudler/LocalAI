import assert from 'node:assert/strict'
import test from 'node:test'

import { isArchive } from './fileBrowser.js'

test('isArchive accepts the archive names the server extracts', () => {
  for (const name of ['a.zip', 'A.ZIP', 'b.tar', 'c.tar.gz', 'c.tgz', 'd.tar.bz2', 'd.tbz2', 'e.tar.xz', 'e.txz', 'f.tar.zst', 'g.7z', 'h.rar', 'i.tar.lz4']) {
    assert.equal(isArchive({ kind: 'file', name }), true, name)
  }
})

test('isArchive refuses folders and other files', () => {
  assert.equal(isArchive({ kind: 'dir', name: 'a.zip' }), false)
  for (const name of ['a.txt', 'zip', 'a.gz', 'a.zipx', 'tar']) {
    assert.equal(isArchive({ kind: 'file', name }), false, name)
  }
  assert.equal(isArchive(null), false)
})
