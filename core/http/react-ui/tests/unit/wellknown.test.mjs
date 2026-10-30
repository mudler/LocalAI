import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createWellKnownApi, WellKnownError, fieldsFromSchema, checkField, changedValues, parseSource,
  shortcutList, cleanShortcuts, move, widgetList, cleanWidgets, widgetId, widgetData, sparkPath, formatMetric,
  createWellKnownStore, wellKnownPath,
} from '../../src/utils/wellKnown.js'

const reply = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })

function api(handler) {
  const calls = []
  const fetch = async (url, init) => { calls.push({ url, init }); return handler(url, init) }
  return { api: createWellKnownApi({ fetch }), calls }
}

const personaSchema = {
  properties: {
    name: { type: 'string', title: 'Name', description: 'What the assistant calls itself.', maxLength: 80 },
    system_prompt: { type: 'string', title: 'System prompt', maxLength: 8000, 'x-ui': { widget: 'textarea' } },
    version: { type: 'integer', 'x-ui': { hidden: true } },
  },
}

test('well-known files: a save sends the version read in If-Match and only the values', async () => {
  const { api: a, calls } = api(() => reply(200, { name: 'persona', version: 4 }))
  await a.put('persona', { name: 'Ada' }, { version: 3 })
  assert.equal(calls[0].url, '/api/chats/wellknown/persona')
  assert.equal(calls[0].init.method, 'PUT')
  assert.equal(calls[0].init.headers['If-Match'], '"3"')
  assert.deepEqual(JSON.parse(calls[0].init.body), { values: { name: 'Ada' }, replace: false })
  await a.put('persona', {}, { version: 0 })
  assert.equal(calls[1].init.headers['If-Match'], '"0"', 'version 0 says the file must not exist yet')
  await a.put('persona', {})
  assert.equal(calls[2].init.headers['If-Match'], undefined)
  await a.get('widget-data', 'data/tasks.json')
  assert.equal(calls[3].url, '/api/chats/wellknown/widget-data?path=data%2Ftasks.json')
})

test('well-known files: a conflict keeps the current file, and a refused save keeps the schema errors', async () => {
  const { api: c } = api(() => reply(409, { error: { message: 'changed', type: 'conflict' }, current: { version: 9, data: { name: 'B' } } }))
  await assert.rejects(c.put('persona', { name: 'A' }, { version: 3 }), (e) => e instanceof WellKnownError && e.conflict && e.current.version === 9)
  const { api: v } = api(() => reply(400, { error: { message: 'bad' }, errors: ['name: too long'] }))
  await assert.rejects(v.put('persona', { name: 'x' }), (e) => e.status === 400 && e.errors[0] === 'name: too long' && !e.conflict)
})

test('well-known files: the schema gives the fields in order and hides what the form does not edit', () => {
  const f = fieldsFromSchema(personaSchema)
  assert.deepEqual(f.map(x => x.key), ['name', 'system_prompt'])
  assert.equal(f[0].label, 'Name')
  assert.equal(f[0].help, 'What the assistant calls itself.')
  assert.equal(f[1].widget, 'textarea')
  const ui = fieldsFromSchema({ properties: { theme: { enum: ['dark', 'light'] }, pinnedPages: { type: 'array' }, on: { type: 'boolean' } } })
  assert.deepEqual(ui.map(x => [x.key, x.widget]), [['theme', 'select'], ['on', 'checkbox']])
  assert.deepEqual(ui[0].options, ['dark', 'light'])
})

test('well-known files: a field is checked against its length, pattern and choices', () => {
  assert.equal(checkField({ maxLength: 3 }, 'abcd'), 'tooLong')
  assert.equal(checkField({ pattern: '^[a-z]+$' }, 'A1'), 'format')
  assert.equal(checkField({ options: ['dark'] }, 'blue'), 'format')
  assert.equal(checkField({ required: true }, ''), 'required')
  assert.equal(checkField({ maxLength: 3 }, ''), '')
})

test('well-known files: a save sends the changed fields, null for an emptied one, and never a key the form does not show', () => {
  const fields = fieldsFromSchema(personaSchema)
  const original = { name: 'Ada', system_prompt: 'be brief', x_mood: 'calm' }
  assert.deepEqual(changedValues(fields, original, { ...original, name: 'Bea' }), { name: 'Bea' })
  assert.deepEqual(changedValues(fields, original, { ...original, system_prompt: '' }), { system_prompt: null })
  assert.deepEqual(changedValues(fields, original, original), {})
  assert.deepEqual(changedValues(fields, {}, { name: 'Ada ' }), { name: 'Ada' })
})

test('well-known files: the source view accepts a JSON object only', () => {
  assert.deepEqual(parseSource('{"a":1}'), { value: { a: 1 } })
  assert.equal(parseSource('[1]').error, 'notObject')
  assert.equal(parseSource('{').error, 'notJson')
})

test('shortcuts: the starters are the defaults and shortcuts.json replaces them, even with an empty list', () => {
  const defaults = ['Explain how this works', 'Brainstorm ideas']
  const none = shortcutList({ exists: false, data: {} }, defaults)
  assert.equal(none.fromFile, false)
  assert.deepEqual(none.items.map(s => s.prompt), defaults)
  const mine = shortcutList({ exists: true, data: { shortcuts: [{ label: 'Plan', prompt: 'Plan my day', icon: 'calendar', send: true }, { label: '', prompt: 'x' }] } }, defaults)
  assert.equal(mine.fromFile, true)
  assert.deepEqual(mine.items, [{ label: 'Plan', prompt: 'Plan my day', icon: 'calendar', send: true }])
  assert.deepEqual(shortcutList({ exists: true, data: { shortcuts: [] } }, defaults).items, [])
  assert.equal(shortcutList({ exists: true, data: {} }, defaults).fromFile, false, 'a file without a list keeps the defaults')
})

test('shortcuts: the editor drops empty rows and empty keys, and moves rows', () => {
  assert.deepEqual(cleanShortcuts([{ label: ' Plan ', prompt: 'Plan my day', icon: '', send: false }, { label: '', prompt: '' }]), [{ label: 'Plan', prompt: 'Plan my day' }])
  assert.deepEqual(move(['a', 'b', 'c'], 0, 2), ['b', 'c', 'a'])
  assert.deepEqual(move(['a', 'b'], 0, -1), ['a', 'b'])
})

test('home widgets: home.json gives the widgets in order with unique ids, and the editor writes valid ones', () => {
  const list = widgetList({ data: { widgets: [{ id: 'a', type: 'list', title: 'Tasks', source: 'data/t.json' }, { id: 'a', type: 'chats', title: 'Chats' }, { type: 'clock', title: 'x' }] } })
  assert.deepEqual(list.map(w => [w.id, w.type]), [['a', 'list'], ['a-x', 'chats']])
  assert.equal(widgetId('Open tasks!', ['open-tasks']), 'open-tasks-2')
  assert.deepEqual(cleanWidgets([{ id: 'Bad Id', type: 'list', title: 'Open tasks', source: '/data/t.json' }, { type: 'chats', title: 'Last chats', source: 'x.json' }, { type: 'list', title: '' }]), [
    { id: 'open-tasks', type: 'list', title: 'Open tasks', source: 'data/t.json' },
    { id: 'last-chats', type: 'chats', title: 'Last chats' },
  ])
})

test('home widgets: a missing data file is empty, a broken one is stale or an error, never a crash', () => {
  assert.equal(widgetData(null).state, 'loading')
  assert.equal(widgetData({ exists: false, valid: true, data: {} }).state, 'empty')
  assert.equal(widgetData({ exists: true, valid: false, fallback: true, data: { items: [{ label: 'x' }] } }).state, 'stale')
  assert.equal(widgetData({ exists: true, valid: false, data: {} }).state, 'error')
  const ok = widgetData({ exists: true, valid: true, data: { items: [{ label: 'a' }, { nope: 1 }], value: 184000, series: [1, 'x', 2] } })
  assert.equal(ok.state, 'ok')
  assert.equal(ok.items.length, 1)
  assert.deepEqual(ok.series, [1, 2])
  assert.equal(formatMetric(184000), '184k')
  assert.match(sparkPath([1, 2, 3]), /^M0\.0,/)
  assert.equal(sparkPath([1]), '')
})

// A fake api for the store: get answers from `files`, put merges like the
// server and bumps the version, and every call can be held to order races.
function fakeApi(files = {}) {
  const calls = []
  const held = []
  const api = {
    get: async (name) => {
      calls.push(['get', name])
      const hold = held.shift()
      if (hold) await hold
      const f = files[name] || { version: 0, data: {} }
      return { name, version: f.version, exists: f.version > 0, data: { ...f.data } }
    },
    put: async (name, values, { version }) => {
      calls.push(['put', name, version])
      const f = files[name] || { version: 0, data: {} }
      if (version !== f.version) {
        throw Object.assign(new Error('changed'), { status: 409, conflict: true, current: { name, version: f.version, data: { ...f.data } } })
      }
      files[name] = { version: f.version + 1, data: { ...f.data, ...values } }
      return { name, version: files[name].version, exists: true, data: { ...files[name].data } }
    },
    revert: async (name) => api.get(name),
  }
  return { api, calls, files, hold: () => { let open; held.push(new Promise(r => { open = r })); return () => open() } }
}

test('well-known store: every reader of one file shares one copy, so a save shows everywhere at once', async () => {
  const { api, calls } = fakeApi({ shortcuts: { version: 1, data: { shortcuts: [] } } })
  const own = []
  const store = createWellKnownStore({ api, onOwnWrite: (p) => own.push(p) })
  let seen = 0
  const off = store.subscribe('shortcuts', undefined, () => { seen++ })
  await Promise.all([store.load('shortcuts'), store.load('shortcuts')])
  assert.equal(calls.filter(c => c[0] === 'get').length, 1, 'two readers at once read the file once')
  assert.equal(store.snapshot('shortcuts').doc.version, 1)

  const before = seen
  await store.save('shortcuts', { shortcuts: [{ label: 'a', prompt: 'a' }] })
  assert.deepEqual(calls.at(-1), ['put', 'shortcuts', 1], 'the save starts from the version every reader shows')
  assert.equal(store.snapshot('shortcuts').doc.version, 2)
  assert.ok(seen > before, 'the other readers are told')
  assert.deepEqual(own, ['shortcuts.json'], 'the feed is told this tab wrote the file, so its echo does not read it again')
  off()
})

test('well-known store: a read that started before a save does not bring back the older file', async () => {
  const { api, hold } = fakeApi({ ui: { version: 3, data: { theme: 'dark' } } })
  const store = createWellKnownStore({ api })
  await store.load('ui')
  const open = hold()
  const stale = store.load('ui')
  await store.save('ui', { theme: 'light' })
  open()
  await stale
  assert.equal(store.snapshot('ui').doc.version, 4)
  assert.equal(store.snapshot('ui').doc.data.theme, 'light')
  // A read asked for after the save does not join the stale one.
  await store.load('ui')
  assert.equal(store.snapshot('ui').doc.version, 4)
})

test('well-known store: files of other names and paths are kept apart', async () => {
  const { api } = fakeApi({ ui: { version: 2, data: { theme: 'dark' } } })
  const store = createWellKnownStore({ api })
  await store.load('ui')
  assert.equal(store.snapshot('ui').doc.version, 2)
  assert.equal(store.snapshot('home').doc, null)
  assert.equal(store.snapshot('widget-data', '/data/a.json').doc, null)
  assert.equal(wellKnownPath('widget-data', '/data/a.json'), 'data/a.json')
  assert.equal(wellKnownPath('ui'), 'ui.json')
})
