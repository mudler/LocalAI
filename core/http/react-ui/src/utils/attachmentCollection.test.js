import assert from 'node:assert/strict'
import test from 'node:test'

import {
  ATTACHMENT_COLLECTION_KEY,
  UPLOAD_FAILED,
  UPLOAD_SAVED,
  UPLOAD_UPLOADING,
  attachmentOriginals,
  canSaveAttachments,
  collectionNames,
  effectiveCollection,
  loadAttachmentCollection,
  saveAttachmentCollection,
  saveAttachmentsToCollection,
  saveAttachmentsWhenReady,
  serializeAttachmentSources,
  restoreAttachmentSources,
} from './attachmentCollection.js'

function memoryStorage() {
  const m = new Map()
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
    map: m,
  }
}

const b64 = (s) => Buffer.from(s).toString('base64')

function recorder({ fail = new Set() } = {}) {
  const calls = []
  const upload = async (collection, form) => {
    const file = form.get('file')
    calls.push({ collection, name: file.name, text: await file.text() })
    if (fail.has(file.name)) throw new Error(`rejected ${file.name}`)
    return { status: 'ok' }
  }
  return { calls, upload }
}

test('selection is persisted per browser and cleared by "none"', () => {
  const s = memoryStorage()
  assert.equal(loadAttachmentCollection(s), '')
  saveAttachmentCollection('docs', s)
  assert.equal(s.map.get(ATTACHMENT_COLLECTION_KEY), 'docs')
  assert.equal(loadAttachmentCollection(s), 'docs')
  saveAttachmentCollection('', s)
  assert.equal(s.map.has(ATTACHMENT_COLLECTION_KEY), false)
})

test('storage that throws does not break loading or saving', () => {
  const broken = { getItem() { throw new Error('blocked') }, setItem() { throw new Error('blocked') }, removeItem() { throw new Error('blocked') } }
  assert.equal(loadAttachmentCollection(broken), '')
  assert.doesNotThrow(() => saveAttachmentCollection('docs', broken))
})

test('gating follows the agent pool, the feature flag and the permission', () => {
  const yes = () => true
  const no = () => false
  assert.equal(canSaveAttachments({ features: { agents: true }, hasFeature: yes }), true)
  // /api/features not answered yet: no request to the collections API.
  assert.equal(canSaveAttachments({ features: {}, hasFeature: yes }), false)
  assert.equal(canSaveAttachments({ features: { agents: false }, hasFeature: yes }), false)
  assert.equal(canSaveAttachments({ features: { agents: true, collections: false }, hasFeature: yes }), false)
  assert.equal(canSaveAttachments({ features: { agents: true }, hasFeature: no }), false)
  assert.equal(canSaveAttachments(), false)
})

test('a stored collection that no longer exists counts as none', () => {
  assert.equal(effectiveCollection('docs', ['docs', 'notes']), 'docs')
  assert.equal(effectiveCollection('gone', ['docs']), '')
  assert.equal(effectiveCollection('docs', null), '')
  assert.equal(effectiveCollection('', ['docs']), '')
})

test('collection names accept both string and object list entries', () => {
  assert.deepEqual(collectionNames({ collections: ['a', { name: 'b' }, null, {}] }), ['a', 'b'])
  assert.deepEqual(collectionNames({}), [])
  assert.deepEqual(collectionNames(null), [])
})

test('originals: one upload per source file, scanned PDF pages collapse to the PDF', () => {
  const pdf = new File(['%PDF-1.4 scan'], 'scan.pdf', { type: 'application/pdf' })
  const img = new File(['png'], 'photo.png', { type: 'image/png' })
  const out = attachmentOriginals([
    { name: 'scan-page-1.png', type: 'image/png', base64: b64('page1'), source: pdf },
    { name: 'scan-page-2.png', type: 'image/png', base64: b64('page2'), source: pdf },
    { name: 'photo.png', type: 'image/png', base64: b64('png'), source: img },
  ])
  assert.deepEqual(out.map(o => o.name), ['scan.pdf', 'photo.png'])
  assert.equal(out[0].file, pdf)
})

test('originals: entries without a source are rebuilt from their base64 bytes', async () => {
  const out = attachmentOriginals([{ name: 'notes.md', type: 'text/markdown', base64: b64('# hello') }])
  assert.equal(out.length, 1)
  assert.equal(out[0].file.name, 'notes.md')
  assert.equal(out[0].file.type, 'text/markdown')
  assert.equal(await out[0].file.text(), '# hello')
})

test('no collection selected: no upload call at all', async () => {
  const { calls, upload } = recorder()
  const seen = []
  const res = await saveAttachmentsToCollection({
    collection: '',
    entries: [{ name: 'a.txt', type: 'text/plain', base64: b64('x'), source: new File(['x'], 'a.txt') }],
    upload,
    onStatus: (s) => seen.push(s),
  })
  assert.deepEqual(res, [])
  assert.equal(calls.length, 0)
  assert.equal(seen.length, 0)
})

test('no attachments: no upload call', async () => {
  const { calls, upload } = recorder()
  const res = await saveAttachmentsToCollection({ collection: 'docs', entries: [], upload })
  assert.deepEqual(res, [])
  assert.equal(calls.length, 0)
})

test('uploads every original once, with its own name and bytes, and reports saved', async () => {
  const { calls, upload } = recorder()
  const seen = []
  const audio = new File(['RIFF'], 'memo.wav', { type: 'audio/wav' })
  const res = await saveAttachmentsToCollection({
    collection: 'docs',
    entries: [
      { name: 'a.txt', type: 'text/plain', base64: b64('alpha'), textContent: 'alpha', source: new File(['alpha'], 'a.txt', { type: 'text/plain' }) },
      { name: 'memo.wav', type: 'audio/wav', base64: b64('RIFF'), source: audio },
    ],
    upload,
    onStatus: (s) => seen.push(s),
  })
  assert.deepEqual(calls.map(c => [c.collection, c.name, c.text]), [['docs', 'a.txt', 'alpha'], ['docs', 'memo.wav', 'RIFF']])
  assert.deepEqual(seen[0].map(s => s.state), [UPLOAD_UPLOADING, UPLOAD_UPLOADING])
  assert.deepEqual(res.map(s => [s.name, s.state, s.collection]), [['a.txt', UPLOAD_SAVED, 'docs'], ['memo.wav', UPLOAD_SAVED, 'docs']])
})

test('a failed upload becomes a failed status with the reason and does not reject', async () => {
  const { calls, upload } = recorder({ fail: new Set(['bad.csv']) })
  const res = await saveAttachmentsToCollection({
    collection: 'docs',
    entries: [
      { name: 'bad.csv', type: 'text/csv', base64: b64('a,b'), source: new File(['a,b'], 'bad.csv') },
      { name: 'ok.json', type: 'application/json', base64: b64('{}'), source: new File(['{}'], 'ok.json') },
    ],
    upload,
  })
  assert.equal(calls.length, 2)
  assert.deepEqual(res.map(s => [s.name, s.state, s.error]), [
    ['bad.csv', UPLOAD_FAILED, 'rejected bad.csv'],
    ['ok.json', UPLOAD_SAVED, ''],
  ])
})


test('Home handoff preserves one original PDF across rendered pages', async () => {
  const source = { name: 'scan.pdf', type: 'application/pdf', base64: b64('%PDF-original') }
  const handoff = JSON.parse(JSON.stringify(serializeAttachmentSources([
    { name: 'scan-page-1.png', type: 'image/png', base64: b64('page1'), source },
    { name: 'scan-page-2.png', type: 'image/png', base64: b64('page2'), source },
  ])))
  assert.equal(handoff.attachmentSources.length, 1)
  const entries = restoreAttachmentSources(handoff.files, handoff.attachmentSources)
  const originals = attachmentOriginals(entries)
  assert.equal(originals.length, 1)
  assert.equal(originals[0].name, 'scan.pdf')
  assert.equal(await originals[0].file.text(), '%PDF-original')
  assert.equal(entries[1].base64, b64('page2'))
})

test('send waits for collection discovery without dropping its attachments', async () => {
  let resolve
  const available = new Promise(r => { resolve = r })
  const { calls, upload } = recorder()
  const pending = saveAttachmentsWhenReady({ selected: 'docs', loadAvailable: () => available,
    entries: [{ name: 'a.txt', base64: b64('original') }], upload })
  assert.equal(calls.length, 0)
  resolve(['docs'])
  const statuses = await pending
  assert.equal(calls.length, 1)
  assert.equal(statuses[0].state, UPLOAD_SAVED)
})

test('failed discovery or revoked access never uploads', async () => {
  const { calls, upload } = recorder()
  for (const loadAvailable of [async () => [], async () => { throw new Error('denied') }]) {
    assert.deepEqual(await saveAttachmentsWhenReady({ selected: 'docs', loadAvailable,
      entries: [{ name: 'a.txt', base64: b64('original') }], upload }), [])
  }
  assert.equal(calls.length, 0)
})
