import test from 'node:test'
import assert from 'node:assert/strict'
import { compactCount } from '../../src/utils/modelSearch.js'

test('model search: counts are compact', () => {
  assert.equal(compactCount(0), '')
  assert.equal(compactCount(undefined), '')
  assert.equal(compactCount(950), '950')
  assert.equal(compactCount(1234), '1.2k')
  assert.equal(compactCount(3400000), '3.4M')
  assert.equal(compactCount(2e9), '2B')
})

import {
  filterControl, filterParams, filterText, filterValue, sliderRange, sourceText,
} from '../../src/utils/modelSearch.js'
import {
  BUILTIN_SOURCES, allSourcesFrom, nextEnabled, remoteAsks, remoteRow, remoteSearchParams, remoteSourcesFrom,
} from '../../src/utils/remoteModels.js'

const peer = {
  id: 'peer', title: 'Peer', kind: 'addon', lists_latest: false, running: true,
  filters: [
    { id: 'kind', type: 'enum', label: 'Kind', values: ['a', 'b'], default: 'a' },
    { id: 'many', type: 'enum', label: 'Many', values: ['1', '2', '3', '4', '5'], default: '1' },
    { id: 'fresh', type: 'bool', label: 'Fresh' },
    { id: 'seeds', type: 'range', label: 'Seeds', min: 0, max: 10, step: 1, unit: 'seeds' },
    { id: 'owner', type: 'text', label: 'Owner', maxLength: 20 },
    { id: 'odd', type: 'color', label: 'Odd' },
  ],
  i18n: { it: { title: 'Pari', filters: { kind: { label: 'Tipo', values: { a: 'Uno' } } } } },
}
const hf = { id: 'huggingface', title: 'Hugging Face', kind: 'builtin', lists_latest: true, running: true }

test('model search: each filter type has a control, and an unknown type has none', () => {
  assert.deepEqual(peer.filters.map(filterControl), ['segmented', 'select', 'switch', 'range', 'text', ''])
})

test('model search: a filter shows what the person set, else the default, else an empty value', () => {
  const [kind, , fresh, seeds, owner] = peer.filters
  assert.equal(filterValue(kind, undefined), 'a')
  assert.equal(filterValue(kind, { kind: 'b' }), 'b')
  assert.equal(filterValue(fresh, undefined), false)
  assert.equal(filterValue(fresh, { fresh: true }), true)
  assert.equal(filterValue(seeds, undefined), 0)
  assert.equal(filterValue(owner, undefined), '')
})

test('model search: only a filter the person changed is sent, under its source', () => {
  assert.deepEqual(filterParams('peer', peer.filters, {}), {})
  assert.deepEqual(filterParams('peer', peer.filters, { kind: 'b', fresh: false, seeds: 3, owner: '' }), {
    'filters.peer.kind': 'b', 'filters.peer.fresh': 'false', 'filters.peer.seeds': '3',
  })
  assert.deepEqual(
    remoteSearchParams({ provider: 'peer', term: 'x', ggufOnly: true, filters: peer.filters, filterValues: { kind: 'b' } }),
    { q: 'x', providers: 'peer', limit: 20, 'filters.peer.kind': 'b', format: 'gguf' },
  )
})

test('model search: the label of a source and of a filter follows the language, English is the fallback', () => {
  assert.equal(sourceText(peer, 'it'), 'Pari')
  assert.equal(sourceText(peer, 'it-IT'), 'Pari')
  assert.equal(sourceText(peer, 'de'), 'Peer')
  const text = filterText(peer, peer.filters[0], 'it')
  assert.equal(text.label, 'Tipo')
  assert.equal(text.value('a'), 'Uno')
  assert.equal(text.value('b'), 'b')
  assert.equal(filterText(peer, peer.filters[0], 'en').label, 'Kind')
})

test('model search: a range reaches the slider with its step', () => {
  assert.deepEqual(sliderRange(peer.filters[3]), { min: 0, max: 10, step: 1, integer: true })
  assert.equal(sliderRange({ id: 'x', type: 'range', min: 0, max: 1, step: 0.1 }).integer, false)
})

test('remote sources: only an installed source that is on is a chip', () => {
  const overview = { sources: [hf, peer], enabled: ['huggingface'], missing: [] }
  assert.deepEqual(remoteSourcesFrom(overview).map(s => s.id), ['huggingface'])
  assert.deepEqual(remoteSourcesFrom({ ...overview, enabled: ['huggingface', 'peer', 'ghost'] }).map(s => s.id), ['huggingface', 'peer'])
  assert.deepEqual(remoteSourcesFrom({ ...overview, enabled: [] }), [])
  assert.deepEqual(allSourcesFrom(overview).map(s => s.id), ['huggingface', 'peer'])
})

test('remote sources: a source that is on by default is a chip, and one switch off sends the rest', () => {
  const overview = { sources: [hf, peer], enabled: ['huggingface', 'peer'], missing: [] }
  assert.deepEqual(remoteSourcesFrom(overview).map(s => s.id), ['huggingface', 'peer'])
  assert.deepEqual(nextEnabled(overview.enabled, 'peer', false), ['huggingface'])
  assert.deepEqual(nextEnabled(['huggingface'], 'peer', true), ['huggingface', 'peer'])
})

test('remote sources: with no list from the server the panel is Hugging Face alone, as before', () => {
  assert.equal(remoteSourcesFrom(null), BUILTIN_SOURCES)
  assert.equal(remoteSourcesFrom({}), BUILTIN_SOURCES)
  assert.deepEqual(BUILTIN_SOURCES.map(s => s.id), ['huggingface'])
})

test('remote sources: a source that cannot list its newest models is not asked with no term', () => {
  const sources = remoteSourcesFrom({ sources: [hf, peer], enabled: ['huggingface', 'peer'] })
  assert.equal(remoteAsks('huggingface', '', sources), true)
  assert.equal(remoteAsks('peer', '', sources), false)
  assert.equal(remoteAsks('peer', 'qwen', sources), true)
  assert.equal(remoteAsks('ghost', 'qwen', sources), false)
})

test('remote sources: the p2p tag comes from a property of the source, not from its ID', () => {
  const sources = remoteSourcesFrom({
    sources: [hf, { ...peer, id: 'other', p2p: true }, { id: 'pirateface', title: 'Pirate Face', kind: 'addon' }],
    enabled: ['huggingface', 'other', 'pirateface'],
  })
  assert.deepEqual(sources.map(s => s.p2p), [false, true, false])
  const labels = { downloads: 'd', seeds: n => `${n}` }
  assert.equal(remoteRow({ provider: 'other', id: 'a/b' }, { sourceLabel: 'x', labels, p2p: true }).p2p, true)
  assert.equal(remoteRow({ provider: 'pirateface', id: 'a/b' }, { sourceLabel: 'x', labels }).p2p, false)
})

test('remote sources: a stored ID whose add-on is not installed survives a switch of another source', () => {
  const overview = { sources: [hf, peer], enabled: ['huggingface', 'ghost'], missing: ['ghost'] }
  assert.deepEqual(remoteSourcesFrom(overview).map(s => s.id), ['huggingface'], 'it is not a chip')
  assert.deepEqual(nextEnabled(overview.enabled, 'peer', true), ['huggingface', 'ghost', 'peer'])
  assert.deepEqual(nextEnabled(overview.enabled, 'huggingface', false), ['ghost'])
  assert.deepEqual(nextEnabled(['huggingface', 'peer'], 'peer', true), ['huggingface', 'peer'], 'no duplicate')
})
