import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  NAV_ALWAYS_SHOWN, NAV_BUILTIN_IDS, NAV_LIMITS, canAdd, cleanRows, defaultRows, editorRows, entryLabel, itemKey, itemKind, mergeItems, navUrlOk,
  mergeNav, readItems, rename, rowKind, sections, setHidden, withGroup, withLink,
} from '../../src/utils/nav.js'
import { move, WELL_KNOWN_FILES } from '../../src/utils/wellKnown.js'

const here = dirname(fileURLToPath(import.meta.url))
const schema = JSON.parse(readFileSync(join(here, '../../../../services/studio/builtinskills/tree/localai-workspace/schemas/nav.schema.json'), 'utf8'))
const ALL = new Set(NAV_BUILTIN_IDS)
const ids = (secs) => secs.map(s => ({ group: s.group, ids: s.entries.map(e => (e.kind === 'builtin' ? e.id : e.key)) }))
const flat = (secs) => secs.flatMap(s => s.entries.map(e => (e.kind === 'builtin' ? e.id : e.key)))

test('nav path is well known and the built-in ids are the ones the server lists', () => {
  assert.equal(WELL_KNOWN_FILES.nav, 'LocalAI/nav.json')
  assert.deepEqual([...NAV_BUILTIN_IDS], schema['x-builtin-ids'])
})

test('nav: no way to name the fixed entries', () => {
  for (const id of ['new-chat', 'chats', 'settings', 'palette', 'search', 'operate']) assert.ok(!NAV_BUILTIN_IDS.includes(id), id)
})

test('nav: the kind and the key of an item', () => {
  assert.equal(itemKind({ id: 'files' }), 'builtin')
  assert.equal(itemKind({ group: 'Tools' }), 'group')
  assert.equal(itemKind({ type: 'page', page: 'board' }), 'link')
  assert.equal(itemKind({ type: 'chat', chat: 'c_1' }), 'link')
  assert.equal(itemKind({ type: 'url', url: 'https://localai.io/docs' }), 'link')
  assert.equal(itemKey({ id: 'files' }), 'id:files')
  assert.equal(itemKey({ group: 'Tools' }), 'group:Tools')
  assert.equal(itemKey({ type: 'url', url: 'https://a.io/' }), 'link:url:https://a.io/')
  for (const bad of [null, [], {}, { id: 'files', group: 'x' }, { group: ' ' }, { type: 'page' }, { type: 'page', page: '../x' },
    { type: 'url', url: 'javascript:alert(1)' }, { type: 'url', url: 'https://a:b@c.io' }, { type: 'url', url: '/app/files' }, { type: 'file', file: 'a' }]) {
    assert.equal(itemKind(bad), '', JSON.stringify(bad))
  }
  assert.equal(navUrlOk('https://localai.io'), true)
  assert.equal(navUrlOk('data:text/html,x'), false)
})

test('nav: reading keeps valid items once, drops bad labels and keeps unknown keys', () => {
  const got = readItems({ items: [
    { id: 'files', x_k: 1, label: '  Docs  ' }, { id: 'files' }, null, { group: '' },
    { id: 'skills', label: 'x'.repeat(41), hidden: 'yes', icon: 'Bad Icon' },
  ] })
  assert.deepEqual(got, [{ id: 'files', x_k: 1, label: 'Docs' }, { id: 'skills' }])
  assert.deepEqual(readItems(undefined), [])
  assert.deepEqual(readItems({ items: 'x' }), [])
  const many = Array.from({ length: 80 }, (_, i) => ({ group: `g${i}` }))
  assert.equal(readItems({ items: many }).length, NAV_LIMITS.items)
})

test('nav: sections follow the order, renames and hidden entries', () => {
  const items = readItems({ items: [
    { id: 'files' }, { id: 'skills', label: 'Recipes' }, { group: 'Create' }, { id: 'agents', hidden: true }, { id: 'memory', label: 'Library' },
    { group: 'Tools' }, { id: 'models' },
  ] })
  const secs = sections(items, { available: ALL })
  assert.deepEqual(secs.map(s => s.group), [null, 'Create', 'Tools'])
  assert.deepEqual(secs[0].entries.map(e => e.id), ['files', 'skills'])
  assert.equal(secs[0].entries[1].label, 'Recipes')
  assert.deepEqual(secs[1].entries.map(e => e.id), ['memory'])
  // The built-in entries the list does not name follow the last item, in the default order.
  assert.deepEqual(secs[2].entries.map(e => e.id), ['models', 'home', 'pages', 'connections', 'addons'])
})

test('nav: the entries of a viewer who cannot use them stay out, and an empty group is not drawn', () => {
  const items = readItems({ items: [{ group: 'A' }, { id: 'models' }, { group: 'B' }, { id: 'files' }] })
  const secs = sections(items, { available: new Set(['files', 'home']) })
  assert.deepEqual(secs.map(s => s.group), ['B'])
})

test('nav: Home cannot be hidden, and ids the server does not know are ignored', () => {
  const items = readItems({ items: [{ id: 'home', hidden: true }, { id: 'new-chat' }, { id: 'settings', hidden: true }, { id: 'future-thing' }] })
  const got = flat(sections(items, { available: ALL }))
  assert.ok(got.includes('home'))
  assert.ok(!got.includes('new-chat') && !got.includes('settings') && !got.includes('future-thing'))
  // Hiding every built-in entry leaves Home: the fixed entries of the sidebar are drawn outside nav.json.
  const hiddenAll = readItems({ items: NAV_BUILTIN_IDS.map(id => ({ id, hidden: true })) })
  assert.deepEqual(flat(sections(hiddenAll, { available: ALL })), ['home'])
})

test('nav: links to a gone page or chat are skipped, an address always shows', () => {
  const items = readItems({ items: [
    { id: 'home' }, { type: 'page', page: 'board' }, { type: 'page', page: 'gone' }, { type: 'chat', chat: 'c1' },
    { type: 'chat', chat: 'c2' }, { type: 'url', url: 'https://localai.io/docs', label: 'Docs' },
  ] })
  const status = (it) => ({ board: 'ok', gone: 'missing', c1: 'unknown', c2: 'ok' }[it.page || it.chat])
  const got = flat(sections(items, { available: new Set(['home']), linkStatus: status }))
  assert.deepEqual(got, ['home', 'link:page:board', 'link:chat:c2', 'link:url:https://localai.io/docs'])
})

test('nav: without a list the rows are the default sidebar, and a default edit saves as an empty list', () => {
  const { rows, extras } = editorRows([])
  assert.deepEqual(rows, defaultRows())
  assert.deepEqual(extras, [])
  assert.deepEqual(cleanRows(rows, extras), [])
  assert.deepEqual(sections([], { available: ALL }).flatMap(s => s.entries.map(e => e.id)), [...NAV_BUILTIN_IDS])
})

test('nav: the editor rows list every built-in entry and keep what this version does not know', () => {
  const { rows, extras } = editorRows(readItems({ items: [{ id: 'skills' }, { id: 'future-thing', label: 'x' }, { group: 'G' }] }))
  assert.equal(rows[0].id, 'skills')
  assert.deepEqual(rows.filter(r => r.id).map(r => r.id).sort(), [...NAV_BUILTIN_IDS].sort())
  assert.deepEqual(extras, [{ id: 'future-thing', label: 'x' }])
  const saved = cleanRows(rows, extras)
  assert.deepEqual(saved.at(-1), { id: 'future-thing', label: 'x' })
})

test('nav: reorder, rename, hide, group and link produce the list the file needs', () => {
  let rows = editorRows([]).rows
  rows = move(rows, 3, 0) // agents first
  rows = rename(rows, 0, ' Bots ')
  rows = setHidden(rows, 1, true) // home: never
  rows = setHidden(rows, 5, true)
  rows = withGroup(rows, 'Tools')
  rows = withGroup(rows, 'Tools')
  rows = withLink(rows, { type: 'url', url: 'https://localai.io/docs', label: 'Docs' })
  rows = withLink(rows, { type: 'url', url: 'https://localai.io/docs' })
  const out = cleanRows(rows)
  assert.deepEqual(out[0], { id: 'agents', label: 'Bots' })
  assert.deepEqual(out[1], { id: 'home' })
  assert.equal(out.filter(r => r.hidden).length, 1)
  assert.deepEqual(out.filter(r => r.group).map(r => r.group), ['Tools', 'Tools 2'])
  assert.equal(out.filter(r => r.type === 'url').length, 1)
  // Clearing the name restores the default one.
  assert.equal(cleanRows(rename(rows, 0, '  ')).find(r => r.id === 'agents').label, undefined)
})

test('nav: the limits of the list', () => {
  let rows = []
  for (let i = 0; i < NAV_LIMITS.groups; i++) rows = withGroup(rows, `g${i}`)
  assert.equal(canAdd(rows, 'group'), false)
  assert.equal(canAdd(rows, 'link'), true)
  for (let i = 0; i < NAV_LIMITS.links; i++) rows = withLink(rows, { type: 'chat', chat: `c${i}` })
  assert.equal(canAdd(rows, 'link'), false)
})

const L = (...a) => a.map(x => (typeof x === 'string' ? { id: x } : x))
const order = (l) => l.map(itemKey)

test('nav merge: the other writer added an item while I renamed one', () => {
  const base = L('home', 'files', 'agents')
  const mine = [{ id: 'home' }, { id: 'files', label: 'Docs' }, { id: 'agents' }]
  const theirs = [...base, { type: 'url', url: 'https://localai.io/' }]
  const out = mergeItems(base, mine, theirs)
  assert.deepEqual(out, [{ id: 'home' }, { id: 'files', label: 'Docs' }, { id: 'agents' }, { type: 'url', url: 'https://localai.io/' }])
})

test('nav merge: both change one item in different fields, mine wins a field both changed', () => {
  const base = L('home', 'files')
  const mine = [{ id: 'home' }, { id: 'files', label: 'Mine' }]
  const theirs = [{ id: 'home' }, { id: 'files', hidden: true, label: 'Theirs' }]
  assert.deepEqual(mergeItems(base, mine, theirs)[1], { id: 'files', hidden: true, label: 'Mine' })
})

test('nav merge: my move keeps its order and still receives their addition after its neighbour', () => {
  const base = L('home', 'files', 'agents')
  const mine = L('agents', 'home', 'files')
  const theirs = [...base.slice(0, 2), { group: 'New' }, base[2]]
  const out = mergeItems(base, mine, theirs)
  assert.deepEqual(order(out).filter(k => k !== 'group:New'), ['id:agents', 'id:home', 'id:files'])
  assert.ok(order(out).includes('group:New'))
})

test('nav merge: their reorder wins when I did not move, my addition is kept after its neighbour', () => {
  const base = L('home', 'files', 'agents')
  const mine = [...base, { type: 'chat', chat: 'c1' }]
  const theirs = L('agents', 'home', 'files')
  assert.deepEqual(order(mergeItems(base, mine, theirs)), ['id:agents', 'link:chat:c1', 'id:home', 'id:files'])
})

test('nav merge: my removal stays removed, their removal of an item I did not touch stays removed', () => {
  const base = [{ id: 'home' }, { group: 'A' }, { type: 'chat', chat: 'c1' }, { type: 'chat', chat: 'c2' }]
  const mine = [base[0], base[1], base[3]]
  const theirs = [base[0], base[1], base[2]]
  // I removed c1; they removed c2 (not in theirs) and I left it alone.
  const t2 = [base[0], base[1], base[2]]
  assert.deepEqual(order(mergeItems(base, mine, t2)), ['id:home', 'group:A'])
  // I changed c2 while they removed it: my edit keeps it.
  const mine2 = [base[0], base[1], base[2], { ...base[3], label: 'Keep' }]
  assert.deepEqual(order(mergeItems(base, mine2, [base[0], base[1], base[2]])).slice(-1), ['link:chat:c2'])
  assert.equal(theirs.length, 3)
})

test('nav merge: an unchanged edit gives their file', () => {
  const base = L('home', 'files')
  const theirs = [{ id: 'files', label: 'X' }, { id: 'home' }, { group: 'G' }]
  assert.deepEqual(mergeItems(base, base, theirs), theirs)
})

test('nav merge: a save from the editor keeps the assistant addition and all built-in entries', () => {
  const base = { items: [{ id: 'files' }] }
  const { rows, extras } = editorRows(readItems(base))
  const mine = rename(rows, 0, 'Docs')
  const theirs = { items: [{ id: 'files' }, { type: 'url', url: 'https://localai.io/' }] }
  const out = mergeNav(base, mine, extras, theirs)
  assert.deepEqual(out[0], { id: 'files', label: 'Docs' })
  assert.ok(out.some(i => i.type === 'url'))
  for (const id of NAV_BUILTIN_IDS) assert.ok(out.some(i => i.id === id), id)
})

test('nav merge: no edit and no other change is the default list', () => {
  const { rows, extras } = editorRows([])
  assert.deepEqual(mergeNav({}, rows, extras, {}), [])
})

test('nav: a group whose title was cleared in the editor is dropped on save, and the editor row id never reaches the file', () => {
  let rows = withGroup(editorRows([]).rows, 'Tools').map((r, i) => ({ ...r, _uid: i + 1 }))
  rows = rename(rows, rows.length - 1, '')
  assert.equal(rowKind(rows.at(-1)), 'group')
  assert.equal(itemKind(rows.at(-1)), '')
  assert.deepEqual(cleanRows(rows), [])
  rows = rename(rows, rows.length - 1, '  Mine ')
  const out = cleanRows(rows)
  assert.deepEqual(out.at(-1), { group: 'Mine' })
  assert.ok(out.every(r => r._uid === undefined))
})

test('nav: a nav.json label beats the ui.json strings text, which beats the built-in label', () => {
  const strings = { 'nav.home': 'Start' }
  const text = (key) => strings[key] || ''
  assert.equal(entryLabel('home', 'Dashboard', text), 'Dashboard')
  assert.equal(entryLabel('home', undefined, text), 'Start')
  assert.equal(entryLabel('home', '  ', text), 'Start')
  assert.equal(entryLabel('home', undefined, () => ''), '')
  assert.equal(entryLabel('home', undefined), '')
  // Only Home has a strings key among the entries nav.json lists.
  assert.equal(entryLabel('files', undefined, () => 'Never'), '')
  assert.equal(entryLabel('files', 'Docs', text), 'Docs')
})

test('nav: the editor never writes Home hidden, which the server refuses', () => {
  assert.deepEqual([...NAV_ALWAYS_SHOWN], ['home'])
  const rows = [{ id: 'files' }, { id: 'home' }]
  const tried = setHidden(rows, 1, true)
  assert.equal(tried[1].hidden, undefined, 'the hide of Home is a no-op')
  // A row that came in hidden (an older file) is saved without it.
  const saved = cleanRows([{ id: 'files', hidden: true }, { id: 'home', hidden: true }], [], false)
  assert.deepEqual(saved, [{ id: 'files', hidden: true }, { id: 'home' }])
})
