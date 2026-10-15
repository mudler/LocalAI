import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MAX_FAVOURITES, TYPE_ORDER, canTake, collectWork, columnCount, countByType, dealColumns, filterWork,
  groupWork, handoffPath, layoutLineage, neighbour, projectOf, pruneIds, readHandoff, sourceUrlFor,
  suggestNext, suggestType, toggleId, waveBars,
} from './studioWork.js'
import {
  FAVOURITES_KEY, MEDIA_STORAGE_KEYS, clearAllMediaHistory,
} from './mediaHistoryStore.js'

const entry = (id, createdAt, extra = {}) => ({
  id, createdAt, prompt: `prompt ${id}`, model: 'm', params: {}, results: [{ url: `/generated/${id}.png` }], ...extra,
})

// A harbour: a picture and a second take of it, a clip animated from the first,
// and an unrelated sound.
function harbour() {
  const media = {
    image: [entry('i1', 100, { params: { size: '1216x832' } }), entry('i2', 110, { parentId: 'i1', edge: 'take' })],
    video: [entry('v1', 200, { parentId: 'i1', edge: 'animate' })],
    tts: [entry('t1', 300)],
    sound: [], 'audio-transform': [], diarization: [],
  }
  return collectWork(media, [], { favourites: ['i2'] })
}

test('suggestType picks a type from the words and stays quiet otherwise', () => {
  assert.equal(suggestType('slow dolly through a misty forest, drone shot'), 'video')
  assert.equal(suggestType('a 3d mesh of a ceramic bowl'), 'threed')
  assert.equal(suggestType('read this aloud in a calm voice'), 'tts')
  assert.equal(suggestType('lo-fi piano with rain'), 'sound')
  assert.equal(suggestType('who spoke in this meeting'), 'diarization')
  assert.equal(suggestType('please clean up this recording'), 'transform')
  assert.equal(suggestType('a poster of a lighthouse'), 'images')
  assert.equal(suggestType('hello there'), null)
  assert.equal(suggestType('video'), null, 'too short to suggest from')
  assert.equal(suggestType(''), null)
})

test('narrow cases win over broad ones', () => {
  // "picture" is an image word, but "video" is the more specific ask.
  assert.equal(suggestType('a video of a picture frame falling'), 'video')
  assert.equal(suggestType('who spoke in this video clip'), 'diarization')
})

test('collectWork normalises every store, newest first, with favourites', () => {
  const items = harbour()
  assert.deepEqual(items.map(i => i.id), ['t1', 'v1', 'i2', 'i1'])
  assert.equal(items.find(i => i.id === 'i2').favourite, true)
  assert.equal(items.find(i => i.id === 'i1').favourite, false)
  assert.equal(items.find(i => i.id === 'v1').type, 'video')
  assert.equal(items.find(i => i.id === 'v1').url, '/generated/v1.png')
})

test('a 3D entry takes its title from the label, else the file name, and keeps its thumbnail', () => {
  const items = collectWork({}, [
    { id: 'm1', createdAt: 5, name: 'a.glb', label: 'Ceramic bowl', inputThumb: 'data:image/jpeg;base64,AA', model: 'trellis' },
    { id: 'm2', createdAt: 4, name: 'b.glb' },
  ])
  assert.equal(items[0].title, 'Ceramic bowl')
  assert.equal(items[0].thumb, 'data:image/jpeg;base64,AA')
  assert.equal(items[1].title, 'b.glb')
})

test('a transform entry keeps its input apart from its output', () => {
  const [item] = collectWork({
    'audio-transform': [entry('x1', 1, { results: [{ kind: 'output', url: '/o.wav' }, { kind: 'input', url: '/i.wav' }] })],
  }, [])
  assert.equal(item.url, '/o.wav')
  assert.equal(item.inputUrl, '/i.wav')
  assert.deepEqual(item.urls, ['/o.wav'])
})

test('counts are per result and filters match the counts', () => {
  const items = harbour()
  const counts = countByType(items)
  assert.equal(counts.all, 4)
  assert.equal(counts.images, 2)
  assert.equal(counts.video, 1)
  assert.equal(counts.favourites, 1)
  for (const key of TYPE_ORDER) assert.equal(filterWork(items, key).length, counts[key])
  assert.equal(filterWork(items, 'favourites').length, counts.favourites)
  assert.equal(filterWork(items, 'all').length, counts.all)
})

test('related results stack into one project and the rest stay single', () => {
  const tiles = groupWork(harbour())
  assert.equal(tiles.length, 2)
  const project = tiles.find(t => t.kind === 'project')
  assert.equal(project.items.length, 3)
  assert.equal(project.id, 'i1', 'the project is named by its root result')
  assert.equal(project.cover.id, 'i2', 'the newest picture is the cover')
  assert.equal(tiles.find(t => t.kind === 'single').item.id, 't1')
  assert.equal(tiles[0].kind, 'single', 'newest activity first')
})

test('a link to a result that was deleted leaves the child standing alone', () => {
  const items = collectWork({ image: [entry('a', 1, { parentId: 'gone', edge: 'take' })], video: [], tts: [], sound: [], 'audio-transform': [], diarization: [] }, [])
  assert.equal(groupWork(items).length, 1)
  assert.equal(groupWork(items)[0].kind, 'single')
})

test('a loop in stored links does not hang or stack', () => {
  const media = { image: [entry('a', 1, { parentId: 'b' }), entry('b', 2, { parentId: 'a' })], video: [], tts: [], sound: [], 'audio-transform': [], diarization: [] }
  const items = collectWork(media, [])
  const tiles = groupWork(items)
  assert.ok(tiles.length >= 1)
  const layout = layoutLineage(projectOf(items, 'a'))
  assert.ok(layout.nodes.every(n => Number.isFinite(n.x) && Number.isFinite(n.y)))
})

test('projectOf returns the whole project from any member, oldest first', () => {
  const items = harbour()
  assert.deepEqual(projectOf(items, 'v1').map(i => i.id), ['i1', 'i2', 'v1'])
  assert.deepEqual(projectOf(items, 't1').map(i => i.id), ['t1'])
  assert.equal(projectOf(items, 'nope'), null)
})

test('the lineage lays out a prompt, two takes sharing it, and a clip from the first', () => {
  const members = projectOf(harbour(), 'v1')
  const layout = layoutLineage(members, { selectedId: 'v1' })
  const byId = Object.fromEntries(layout.nodes.map(n => [n.id, n]))
  assert.ok(byId['src:i1'], 'one prompt node')
  assert.equal(layout.nodes.filter(n => n.kind === 'source').length, 1)
  const edge = (from, to) => layout.edges.find(e => e.from === from && e.to === to)
  assert.equal(edge('src:i1', 'res:i1').label, null)
  assert.equal(edge('src:i1', 'res:i2').label, 'take', 'a take hangs off the shared prompt')
  assert.equal(edge('res:i1', 'res:v1').label, 'animate')
  assert.ok(byId['res:v1'].x > byId['res:i1'].x && byId['res:i1'].x > byId['src:i1'].x, 'columns follow depth')
  // The selected path, root to selection, is the heavy one.
  assert.ok(edge('src:i1', 'res:i1').chain && edge('res:i1', 'res:v1').chain)
  assert.ok(!edge('src:i1', 'res:i2').chain)
  assert.deepEqual([...layout.chain].sort(), ['res:i1', 'res:v1', 'src:i1'])
})

test('a file start gets a file node, a prompt start gets the prompt text', () => {
  const media = {
    image: [entry('i1', 1)], video: [], tts: [], sound: [], diarization: [],
    'audio-transform': [entry('x1', 2, { prompt: 'audio: a.wav' })],
  }
  const layout = layoutLineage(collectWork(media, []).filter(i => i.id === 'x1'))
  const source = layout.nodes.find(n => n.kind === 'source')
  assert.equal(source.input, 'file')
  assert.equal(source.title, '')
})

test('a ghost node hangs off the selection and the board grows to hold it', () => {
  const members = projectOf(harbour(), 'v1')
  const plain = layoutLineage(members, { selectedId: 'v1' })
  const ghosted = layoutLineage(members, { selectedId: 'v1', ghost: { id: 'ghost:v1', from: 'res:v1', to: 'sound', edge: 'soundtrack' } })
  const ghost = ghosted.nodes.find(n => n.kind === 'ghost')
  assert.ok(ghost)
  assert.ok(ghost.x > ghosted.nodes.find(n => n.id === 'res:v1').x)
  assert.ok(ghosted.width > plain.width)
  assert.ok(ghosted.edges.find(e => e.ghost).chain)
})

test('arrow keys walk left to the source result, right to a child, and down a column', () => {
  const layout = layoutLineage(projectOf(harbour(), 'v1'), { selectedId: 'v1' })
  assert.equal(neighbour(layout, 'v1', 'left'), 'i1')
  assert.equal(neighbour(layout, 'i1', 'right'), 'v1')
  assert.equal(neighbour(layout, 'i1', 'down'), 'i2')
  assert.equal(neighbour(layout, 'i2', 'up'), 'i1')
  assert.equal(neighbour(layout, 'i1', 'left'), 'i1', 'the first result has nothing to its left but a prompt')
})

test('the suggested next step is the first supported one with a model, else the first supported', () => {
  assert.equal(suggestNext('images', new Set(['images', 'threed'])).to, 'threed')
  assert.equal(suggestNext('images', new Set(['images', 'video'])).to, 'video')
  assert.equal(suggestNext('images', new Set(['images'])).to, 'images', 'a variation needs only the model already installed')
  assert.equal(suggestNext('images', new Set()).to, 'video', 'none installed: the first, marked as missing by the caller')
  assert.equal(suggestNext('video', new Set(['sound'])), null, 'the Sound page cannot start from a clip')
  assert.equal(suggestNext('diarization', new Set()), null)
})

test('run as a new take needs the original input', () => {
  const items = Object.fromEntries(harbour().map(i => [i.id, i]))
  assert.equal(canTake(items.i1).ok, true)
  assert.equal(canTake({ type: 'threed' }).ok, false)
  assert.equal(canTake({ type: 'diarization' }).ok, false)
  assert.equal(canTake({ type: 'transform', inputUrl: '' }).ok, false)
  assert.equal(canTake({ type: 'transform', inputUrl: '/i.wav' }).ok, true)
})

test('hand-off paths carry what a workspace accepts and read back the same', () => {
  const path = handoffPath('images', { prompt: ' a brass orrery ', model: 'flux', size: '768x768', count: 3, from: 'i1', edge: 'variation' })
  assert.match(path, /^\/app\/studio\/images\?/)
  const got = readHandoff(new URL(path, 'http://x').searchParams)
  assert.deepEqual(got, { prompt: 'a brass orrery', model: 'flux', size: '768x768', count: 3, from: 'i1', edge: 'variation' })
  assert.equal(handoffPath('tts', {}), '/app/studio/tts')
  assert.equal(handoffPath('images', { count: 1 }), '/app/studio/images', 'a count of one is the default')
  assert.deepEqual(readHandoff(new URLSearchParams('n=abc')).count, 0)
})

test('a take of a transform starts from its input, anything else from its output', () => {
  const item = { url: '/o.wav', inputUrl: '/i.wav' }
  assert.equal(sourceUrlFor(item, 'take'), '/i.wav')
  assert.equal(sourceUrlFor(item, 'diarize'), '/o.wav')
  assert.equal(sourceUrlFor(null, 'take'), '')
})

test('favourites toggle, stay bounded, and drop ids that no longer exist', () => {
  assert.deepEqual(toggleId([], 'a'), ['a'])
  assert.deepEqual(toggleId(['a', 'b'], 'a'), ['b'])
  let ids = []
  for (let i = 0; i < MAX_FAVOURITES + 25; i++) ids = toggleId(ids, `id${i}`)
  assert.equal(ids.length, MAX_FAVOURITES)
  assert.equal(ids[ids.length - 1], `id${MAX_FAVOURITES + 24}`, 'the newest is kept')
  assert.deepEqual(pruneIds(['i1', 'zzz'], harbour()), ['i1'])
})

test('clearing history removes every list and the favourites, and nothing else', () => {
  const store = new Map([['unrelated', 'keep'], [FAVOURITES_KEY, '["a"]']])
  for (const key of Object.values(MEDIA_STORAGE_KEYS)) store.set(key, '[{"id":"a"}]')
  globalThis.localStorage = {
    getItem: k => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
    removeItem: k => store.delete(k),
  }
  const removed = clearAllMediaHistory()
  assert.equal(removed, Object.keys(MEDIA_STORAGE_KEYS).length + 1)
  assert.deepEqual([...store.keys()], ['unrelated'])
  assert.equal(clearAllMediaHistory(), 0, 'clearing again removes nothing')
  delete globalThis.localStorage
})

test('every workspace has a history list, including diarization', () => {
  assert.ok(MEDIA_STORAGE_KEYS.diarization)
  for (const key of ['image', 'video', 'tts', 'sound', 'audio-transform']) assert.ok(MEDIA_STORAGE_KEYS[key])
})

test('the masonry balances columns and follows the width', () => {
  assert.equal(columnCount(0), 1)
  assert.equal(columnCount(320), 1)
  assert.equal(columnCount(700), 2)
  assert.equal(columnCount(1000), 3)
  assert.equal(columnCount(5000), 4)
  const tiles = groupWork(harbour()).concat(
    ...[1, 2, 3, 4, 5].map(n => groupWork(collectWork({ image: [entry(`z${n}`, n)], video: [], tts: [], sound: [], 'audio-transform': [], diarization: [] }, []))),
  )
  const cols = dealColumns(tiles, 3)
  assert.equal(cols.length, 3)
  assert.equal(cols.flat().length, tiles.length, 'every tile is dealt once')
  assert.ok(cols.every(c => c.length > 0))
})

test('a waveform placeholder is repeatable and bounded', () => {
  const a = waveBars('t1')
  assert.deepEqual(a, waveBars('t1'))
  assert.notDeepEqual(a, waveBars('t2'))
  assert.ok(a.every(h => h > 0 && h <= 1))
  assert.equal(waveBars('x', 10).length, 10)
})

test('a 3D result is titled by the label, else the motion prompt, else the file name', () => {
  const base = { id: 'm1', createdAt: 1, model: 'trellis', params: {}, name: 'out.glb' }
  const title = (extra) => collectWork({}, [{ ...base, ...extra }])[0].title
  assert.equal(title({ label: 'A vase' }), 'A vase')
  assert.equal(title({ inputs: { prompt: { type: 'text', data: 'Walk forward and wave' } } }), 'Walk forward and wave')
  assert.equal(title({}), 'out.glb')
})
