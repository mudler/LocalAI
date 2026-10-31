import test from 'node:test'
import assert from 'node:assert/strict'
import { PAGE_CSP, buildPageSrcdoc, createPageHost, MAX_CONTENT } from '../../src/utils/pagesBridge.js'
import { ageParts, feedPaths, isPageName, pageChanges, pinsOf, pinnedPages, touchesPages, trusted, withPin } from '../../src/utils/pages.js'

function setup(over = {}) {
  const sent = []
  const calls = []
  const host = createPageHost({
    post: (m) => sent.push(m),
    files: {
      read: async (p) => { calls.push(['read', p]); return { path: p, content: '[]', version: 1, size: 2 } },
      write: async (p, c, b) => { calls.push(['write', p, c, b]); return { path: p, version: 2, size: c.length } },
      list: async (d) => { calls.push(['list', d]); return [] },
    },
    chat: { send: async (t) => { calls.push(['send', t]); return { sent: true, chat_id: 'c1' } } },
    ...over,
  })
  return { host, sent, calls }
}

test('pages csp: no network, inline code only, images as data urls', () => {
  assert.match(PAGE_CSP, /default-src 'none'/)
  assert.match(PAGE_CSP, /script-src 'unsafe-inline'(;|$)/)
  assert.match(PAGE_CSP, /style-src 'unsafe-inline'(;|$)/)
  assert.match(PAGE_CSP, /img-src data:(;|$)/)
  assert.match(PAGE_CSP, /connect-src 'none'/)
  assert.match(PAGE_CSP, /frame-src 'none'/)
  assert.match(PAGE_CSP, /form-action 'none'/)
  assert.doesNotMatch(PAGE_CSP, /https?:|\*|blob:/)
})

test('pages srcdoc: the policy and the bridge come before the page, the doctype stays first', () => {
  const doc = buildPageSrcdoc('<!DOCTYPE html><html><body><p>hi</p></body></html>', { name: 'tasks', title: 'Tasks', theme: 'dark' })
  assert.ok(doc.startsWith('<!DOCTYPE html><meta http-equiv="Content-Security-Policy"'))
  assert.ok(doc.indexOf('window.localai') < doc.indexOf('<p>hi</p>') || doc.indexOf("'localai'") < doc.indexOf('<p>hi</p>'))
  assert.match(doc, /"theme":"dark"/)
  const plain = buildPageSrcdoc('<p>x</p>', { name: 'n' })
  assert.ok(plain.startsWith('<!doctype html><meta'))
  assert.match(plain, /"theme":"light"/)
})

test('pages srcdoc: a title cannot close the bridge script or inject a replacement pattern', () => {
  const doc = buildPageSrcdoc('', { name: 'x', title: '</script><script>alert(1)</script>$&$`', theme: 'light' })
  const scripts = doc.match(/<\/script>/g) || []
  assert.equal(scripts.length, 1, 'only the bridge script ends')
  assert.match(doc, /\\u003c\/script>/)
  assert.match(doc, /\$&\$`/)
})

test('pages host: routes the four calls and answers with their result', async () => {
  const { host, sent, calls } = setup()
  await host.receive({ id: 1, method: 'files.read', params: { path: 'tasks.json' } })
  await host.receive({ id: 2, method: 'files.write', params: { path: 'tasks.json', content: '[1]', base: 3 } })
  await host.receive({ id: 3, method: 'files.list', params: {} })
  await host.receive({ id: 4, method: 'chat.send', params: { text: '  hello  ' } })
  assert.deepEqual(calls, [['read', 'tasks.json'], ['write', 'tasks.json', '[1]', 3], ['list', ''], ['send', 'hello']])
  assert.deepEqual(sent.map(m => m.id), [1, 2, 3, 4])
  assert.ok(sent.every(m => m.result && !m.error))
  assert.deepEqual(sent[3].result, { sent: true, chat_id: 'c1' })
})

test('pages host: refuses malformed calls and unknown methods, drops what is not a call', async () => {
  const { host, sent, calls } = setup()
  await host.receive({ id: 1, method: 'files.read', params: { path: '' } })
  await host.receive({ id: 2, method: 'files.write', params: { path: 'a', content: 12 } })
  await host.receive({ id: 3, method: 'files.write', params: { path: 'a', content: 'x'.repeat(MAX_CONTENT + 1) } })
  await host.receive({ id: 4, method: 'files.write', params: { path: 'a', content: 'x', base: -1 } })
  await host.receive({ id: 5, method: 'chat.send', params: { text: '' } })
  await host.receive({ id: 6, method: 'chat.send', params: { text: 'x'.repeat(4001) } })
  await host.receive({ id: 7, method: 'tools.call', params: {} })
  await host.receive({ id: 8, method: 'files.read', params: { path: 'x'.repeat(300) } })
  await host.receive({ method: 'files.read', params: { path: 'a' } })
  await host.receive({ id: 'x', method: 'files.read' })
  await host.receive('files.read')
  await host.receive(null)
  assert.deepEqual(calls, [])
  assert.deepEqual(sent.map(m => m.id), [1, 2, 3, 4, 5, 6, 7, 8])
  assert.ok(sent.every(m => m.error && typeof m.error.message === 'string'))
  assert.match(sent[6].error.message, /unknown method/)
})

test('pages host: a failed call answers with its message; a closed host answers nothing', async () => {
  const { host, sent } = setup({ files: { read: async () => { throw new Error('page or file not found') } } })
  await host.receive({ id: 1, method: 'files.read', params: { path: 'x' } })
  assert.equal(sent[0].error.message, 'page or file not found')
  host.emit('files.changed', { paths: ['a.json'] })
  host.emit('other', {})
  assert.deepEqual(sent[1], { event: 'files.changed', data: { paths: ['a.json'] } })
  host.close()
  host.emit('files.changed', { paths: [] })
  await host.receive({ id: 2, method: 'files.read', params: { path: 'x' } })
  assert.equal(sent.length, 2)
})

test('pages host: bounds the calls in flight', async () => {
  let release
  const gate = new Promise(r => { release = r })
  const { host, sent } = setup({ files: { read: () => gate } })
  const runs = []
  for (let i = 1; i <= 9; i++) runs.push(host.receive({ id: i, method: 'files.read', params: { path: 'x' } }))
  await new Promise(r => setTimeout(r, 0))
  assert.equal(sent.length, 1)
  assert.equal(sent[0].id, 9)
  assert.match(sent[0].error.message, /too many/)
  release({ content: '' })
  await Promise.all(runs)
  assert.equal(sent.length, 9)
})

test('pages names: lowercase letters, digits and dashes', () => {
  for (const ok of ['tasks', 'task-board', '0']) assert.ok(isPageName(ok), ok)
  for (const bad of ['', '-x', 'Tasks', 'a/b', '..', 'a_b', 'x'.repeat(65), 12]) assert.ok(!isPageName(bad), String(bad))
})

test('pages feed: splits a change into the page itself and its data files', () => {
  assert.deepEqual(pageChanges(['/pages/tasks/tasks.json', '/pages/tasks/data/a.json', '/pages/other/x', '/notes.txt'], 'tasks'),
    { index: false, meta: false, rel: ['tasks.json', 'data/a.json'], all: false })
  assert.deepEqual(pageChanges(['pages/tasks/index.html', 'pages/tasks/page.json'], 'tasks'), { index: true, meta: true, rel: [], all: false })
  assert.equal(pageChanges(['/pages/tasks-2/index.html'], 'tasks').index, false, 'another page with a longer name')
  assert.equal(pageChanges(['/pages/tasks'], 'tasks').index, true, 'the folder itself moved or was deleted')
  assert.equal(pageChanges(undefined, 'tasks').all, true, 'a resync')
  assert.ok(touchesPages(['/pages/x/index.html']))
  assert.ok(touchesPages(['/pages']))
  assert.ok(!touchesPages(['/pagesx/a', '/notes/pages/a']))
  assert.ok(touchesPages(undefined))
})

test('pages feed: a resync of the change feed has no paths and means everything may have changed', () => {
  // The feed calls back with {paths: [], resync: true} after a gap (focus,
  // reconnect, end of a chat turn), and with the changed paths otherwise.
  assert.equal(feedPaths({ paths: [], revision: 4, resync: true }), null)
  assert.equal(feedPaths(undefined), null)
  assert.deepEqual(feedPaths({ paths: ['pages/tasks/a.json'], revision: 5 }), ['pages/tasks/a.json'])
  assert.equal(pageChanges(feedPaths({ paths: [], resync: true }), 'tasks').all, true)
  assert.ok(touchesPages(feedPaths({ paths: [], resync: true })))
  assert.ok(!touchesPages(feedPaths({ paths: ['notes.txt'], revision: 6 })))
})

test('pages trust: own pages run, anything else waits for the trust sheet', () => {
  assert.equal(trusted({ source: 'own' }), true)
  assert.equal(trusted({ source: 'shared' }), false)
  assert.equal(trusted(null), false)
})

test('pages pins: kept in ui.json without losing the keys of other editors', () => {
  const ui = { theme: 'dark', accent: '#112233', future: { x: 1 }, pinnedPages: ['tasks', 'BAD', 'tasks', 'notes'] }
  assert.deepEqual(pinsOf(ui), ['tasks', 'notes'])
  const pinned = withPin(ui, 'reading', true)
  assert.deepEqual(pinned.pinnedPages, ['tasks', 'notes', 'reading'])
  assert.equal(pinned.theme, 'dark')
  assert.deepEqual(pinned.future, { x: 1 })
  assert.deepEqual(withPin(pinned, 'tasks', false).pinnedPages, ['notes', 'reading'])
  assert.deepEqual(withPin(null, 'a', true), { pinnedPages: ['a'] })
  assert.deepEqual(pinsOf({ pinnedPages: 'tasks' }), [])
  assert.deepEqual(pinnedPages([{ name: 'a' }, { name: 'b' }], ['b', 'gone', 'a']).map(p => p.name), ['b', 'a'])
})

test('pages age: the largest unit that fits, nothing under a minute', () => {
  const now = Date.parse('2026-10-29T12:00:00Z')
  assert.equal(ageParts('2026-10-29T11:59:30Z', now), null)
  assert.deepEqual(ageParts('2026-10-29T11:55:00Z', now), { value: -5, unit: 'minute' })
  assert.deepEqual(ageParts('2026-10-29T09:00:00Z', now), { value: -3, unit: 'hour' })
  assert.deepEqual(ageParts('2026-10-27T12:00:00Z', now), { value: -2, unit: 'day' })
  assert.equal(ageParts('not a date', now), null)
  assert.equal(ageParts('2026-10-29T13:00:00Z', now), null, 'a clock ahead of ours')
})
