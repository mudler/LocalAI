// The rules of the private file cards of the Tables CSV export: the host card
// facts, the identifiers it accepts and the arguments of the export of a view.
import test from 'node:test'
import assert from 'node:assert/strict'
import { validArtifactId, artifactFacts, artifactDownloadPath, artifactStatusPath, artifactIds, formatBytes } from '../../src/addons/artifactFacts.js'
import { artifactReferences } from '../../src/addons/proposalResult.js'
import { exportArguments, exportOutcome } from '../../../../../addons/tables/ui/src/lib/export.js'

const ID = 'a' + '0123456789abcdef'.repeat(2)
const NOW = Date.parse('2026-10-14T12:00:00Z')

test('artifacts: only a host identifier names a file, and the only link is the host route', () => {
  assert.equal(validArtifactId(ID), true)
  for (const bad of ['', 'a', ID + '0', ID.toUpperCase(), '../x', 'https://evil.example/' + ID, 'x' + ID.slice(1), null, 5, { id: ID }]) {
    assert.equal(validArtifactId(bad), false, String(bad))
    assert.equal(artifactDownloadPath(bad), '')
    assert.equal(artifactStatusPath(bad), '')
  }
  assert.equal(artifactDownloadPath(ID), `/api/addons/artifacts/${ID}/download`)
  assert.equal(artifactStatusPath(ID), `/api/addons/artifacts/${ID}`)
})

test('artifacts: the card offers a download only for a complete file that has not expired', () => {
  const rec = { id: ID, name: 'Orders.csv', size: 2048, state: 'complete', expiresAt: '2026-10-14T13:00:00Z', path: '/etc/passwd', url: 'https://evil.example/' }
  const ok = artifactFacts(rec, NOW)
  assert.deepEqual({ ...ok }, { id: ID, name: 'Orders.csv', size: 2048, state: 'complete', expiresAt: Date.parse(rec.expiresAt), expired: false, downloadable: true })
  assert.equal(artifactFacts({ ...rec, state: 'transferring' }, NOW).downloadable, false)
  assert.equal(artifactFacts({ ...rec, state: 'failed' }, NOW).downloadable, false)
  assert.equal(artifactFacts({ ...rec, state: 'cancelled' }, NOW).downloadable, false)
  const late = artifactFacts(rec, Date.parse('2026-10-14T13:00:01Z'))
  assert.equal(late.expired, true)
  assert.equal(late.downloadable, false)
  assert.equal(artifactFacts({ ...rec, id: '../x' }, NOW).downloadable, false)
  assert.equal(artifactFacts(null, NOW).downloadable, false)
  assert.equal('path' in ok, false)
  assert.equal('url' in ok, false)
})

test('artifacts: sizes are written with a binary unit', () => {
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(1023), '1023 B')
  assert.equal(formatBytes(1536), '1.5 KiB')
  assert.equal(formatBytes(64 << 20), '64 MiB')
  assert.equal(formatBytes(-1), '')
  assert.equal(formatBytes(NaN), '')
})

test('artifacts: a tool result keeps well formed identifiers of its own kind only, at most four', () => {
  const doc = (v) => JSON.stringify(v)
  assert.deepEqual(artifactIds({ kind: 'artifact', artifacts: [{ id: ID, name: 'x' }, { id: ID }, { id: '../x' }, {}] }), [ID])
  assert.deepEqual(artifactIds({ kind: 'result', artifacts: [{ id: ID }] }), [])
  assert.deepEqual(artifactIds({ kind: 'artifact', artifacts: 'x' }), [])
  const many = Array.from({ length: 9 }, (_, i) => ({ id: 'a' + String(i).repeat(32) }))
  assert.equal(artifactIds({ kind: 'artifact', artifacts: many }).length, 4)
  assert.deepEqual(artifactReferences('addon__tables__rows-export', doc({ kind: 'artifact', artifacts: [{ id: ID }] })), [ID])
  assert.deepEqual(artifactReferences('rows-export', doc({ kind: 'artifact', artifacts: [{ id: ID }] })), [])
  assert.deepEqual(artifactReferences('addon__tables__rows-export', 'not json'), [])
})

const columns = [{ name: 'Item', type: 'text' }, { name: 'Qty', type: 'integer' }]

test('export: the view becomes the arguments of the tool and nothing a page or a cursor', () => {
  const plain = exportArguments({ table: 'Orders' }, columns)
  assert.deepEqual(plain, { arguments: { tool: 'rows-export', table: 'Orders' }, errors: {} })
  const view = exportArguments({
    table: 'Orders', mode: 'and', sort: [{ column: 'Qty', desc: true }],
    conditions: [{ column: 'Qty', op: 'gte', value: '2' }],
  }, columns)
  assert.deepEqual(Object.keys(view.arguments).sort(), ['filter', 'sort', 'table', 'tool'])
  assert.deepEqual(view.arguments.sort, [{ column: 'Qty', desc: true }])
  assert.equal(view.arguments.filter.column, 'Qty')
  assert.equal('limit' in view.arguments, false)
  assert.equal('cursor' in view.arguments, false)
  const withId = exportArguments({ table: 'Orders', rowId: true }, columns)
  assert.deepEqual(withId.arguments.columns, ['_id', 'Item', 'Qty'])
})

test('export: the answer is a file, or the reason there is none', () => {
  assert.deepEqual(exportOutcome({ files: [{ id: ID }], payload: null }), { kind: 'file', id: ID })
  assert.deepEqual(exportOutcome({ files: [], payload: { exported: false, error: 'too-large' } }), { kind: 'too-large' })
  assert.deepEqual(exportOutcome({ files: [], payload: { error: 'busy' } }), { kind: 'busy' })
  assert.deepEqual(exportOutcome({ files: [], payload: {} }), { kind: 'none' })
  assert.deepEqual(exportOutcome(null), { kind: 'none' })
  assert.deepEqual(exportOutcome({ files: [{ id: ID }, { id: ID }] }), { kind: 'none' })
})
