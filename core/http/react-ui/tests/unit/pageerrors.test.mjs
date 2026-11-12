import test from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { MAX_ERROR_MESSAGE, MAX_ERROR_STACK, buildPageSrcdoc, createPageHost, isNoiseError } from '../../src/utils/pagesBridge.js'

// The bridge of a frame, run in a sandbox with a fake window. timers holds the
// callbacks of setTimeout, so a test decides when the flush of repeats runs.
function frame(html = '<p>x</p>', opts = {}) {
  const listeners = {}
  const calls = []
  const timers = []
  const sandbox = {
    console: { warn() {}, error() {}, log() {} },
    setTimeout: (fn) => { timers.push(fn); return timers.length },
    Promise, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI: 'http://localai.test/app/pages/x', readyState: 'complete',
      documentElement: { setAttribute() {}, style: {} },
      addEventListener() {}, getElementById: () => null, getElementsByName: () => [],
    },
    scrollTo() {},
    addEventListener: (t, fn) => { (listeners[t] ||= []).push(fn) },
  }
  sandbox.window = sandbox
  sandbox.parent = {}
  vm.createContext(sandbox)
  const doc = buildPageSrcdoc(html, { name: 'x', ...opts })
  vm.runInContext(/<script>([\s\S]*?)<\/script>/.exec(doc)[1], sandbox)
  listeners.message[0]({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [{ postMessage: (m) => calls.push(m) }] })
  const errors = () => calls.filter((m) => m.method === 'page.error').map((m) => m.params)
  return {
    doc, calls, timers, errors,
    throws: (message, lineno = 0, colno = 0, extra = {}) => listeners.error.forEach((fn) => fn({ message, lineno, colno, error: { stack: `Error: ${message}\n    at f (<anonymous>:${lineno}:${colno})` }, ...extra })),
    raw: (event) => listeners.error.forEach((fn) => fn(event)),
    rejects: (reason) => listeners.unhandledrejection.forEach((fn) => fn({ reason })),
    flush: () => timers.splice(0).forEach((fn) => fn()),
  }
}

test('page errors frame: an error of the script goes to the host with its message, place and stack', () => {
  const f = frame()
  const shift = Number(/"shift":(\d+)/.exec(f.doc)[1])
  assert.ok(shift > 100, 'the bridge puts many lines before the page')
  f.throws('Uncaught ReferenceError: x is not defined', shift + 12, 7)
  assert.equal(f.errors().length, 1)
  const [e] = f.errors()
  assert.equal(e.message, 'Uncaught ReferenceError: x is not defined')
  assert.equal(e.line, 12, 'the line is the page\'s own, without the lines LocalAI put before it')
  assert.equal(e.column, 7)
  assert.equal(e.count, 1)
  assert.match(e.stack, /^Error: Uncaught ReferenceError/)
})

test('page errors frame: the line of the page matches the lines of the document it was built from', () => {
  const html = '<!doctype html>\n<html>\n<body>\n<script>\nthrow new Error("x")\n</script>\n</body>\n</html>'
  const f = frame(html)
  const shift = Number(/"shift":(\d+)/.exec(f.doc)[1])
  // The line of the throw in the built document is its line in the page plus the shift.
  const built = f.doc.split('\n').findIndex((l) => l === 'throw new Error("x")') + 1
  assert.equal(built - shift, 5)
  f.throws('Uncaught Error: x', built, 1)
  assert.equal(f.errors()[0].line, 5)
})

test('page errors frame: a script file or module names itself and keeps its own lines; a place inside the bridge is unknown', () => {
  const f = frame()
  f.throws('in file', 3, 4, { filename: 'app.js' })
  f.throws('in module', 8, 2, { filename: 'blob:null/1234' })
  f.throws('in the bridge', 5, 1)
  f.throws('no place')
  const [file, mod, inside, none] = f.errors()
  assert.deepEqual([file.file, file.line, file.column], ['app.js', 3, 4])
  assert.deepEqual([mod.file, mod.line], ['a module', 8])
  assert.deepEqual([inside.line, inside.column], [0, 0])
  assert.deepEqual([none.line, none.column, none.file], [0, 0, ''])
})

test('page errors frame: an unhandled rejection is reported, for an Error and for another value', () => {
  const f = frame()
  f.rejects(Object.assign(new TypeError('x.map is not a function'), { stack: 'TypeError: x.map is not a function\n    at a' }))
  f.rejects('plain text')
  f.rejects(undefined)
  const got = f.errors().map((e) => e.message)
  assert.deepEqual(got, ['Unhandled promise rejection: TypeError: x.map is not a function', 'Unhandled promise rejection: plain text', 'Unhandled promise rejection: '])
})

test('page errors frame: a throw in a loop sends the first one at once and the repeats counted, later', () => {
  const f = frame()
  for (let i = 0; i < 600; i++) f.throws('Uncaught TypeError: loop', 400, 3)
  assert.equal(f.errors().length, 1, 'one message for 600 throws')
  assert.equal(f.timers.length, 1, 'one flush is waiting, not one per throw')
  f.flush()
  const all = f.errors()
  assert.equal(all.length, 2)
  assert.equal(all[1].count, 599)
  f.flush()
  assert.equal(f.errors().length, 2, 'nothing more to flush')
  f.throws('Uncaught TypeError: loop', 400, 3)
  f.flush()
  assert.equal(f.errors().at(-1).count, 1)
})

test('page errors frame: at most 20 different errors leave the frame, and noise never does', () => {
  const f = frame()
  for (const noise of ['ResizeObserver loop limit exceeded', 'ResizeObserver loop completed with undelivered notifications.', 'Script error.', '']) f.throws(noise, 1, 1)
  assert.equal(f.errors().length, 0)
  for (let i = 0; i < 50; i++) f.throws(`different ${i}`, 400, 1)
  assert.equal(f.errors().length, 20)
})

test('page errors frame: the capture never breaks the page and the page cannot remove it', () => {
  const f = frame()
  assert.doesNotThrow(() => f.raw(undefined))
  assert.doesNotThrow(() => f.raw({ message: { toString() { throw new Error('hostile') } } }))
  assert.doesNotThrow(() => f.raw({ get message() { throw new Error('hostile') } }))
  assert.doesNotThrow(() => f.rejects({ get message() { throw new Error('hostile') } }))
  assert.equal(f.errors().length, 0)
})

test('page errors frame: a long message is cut', () => {
  const f = frame()
  f.throws('x'.repeat(5000), 400, 1)
  assert.equal(f.errors()[0].message.length, 500)
  assert.equal(MAX_ERROR_MESSAGE, 500)
})

function host(over = {}) {
  const sent = []
  const reports = []
  let t = 0
  const h = createPageHost({
    post: (m) => sent.push(m),
    files: { read: async () => ({}), write: async () => ({}), list: async () => [] },
    chat: { send: async () => ({}) },
    errors: { report: async (e) => { reports.push(e) } },
    now: () => t,
    ...over,
  })
  return { h, sent, reports, at: (ms) => { t = ms } }
}
const report = (h, id, params) => h.receive({ id, method: 'page.error', params })

test('page errors host: records an error for the owner and answers recorded', async () => {
  const { h, sent, reports } = host()
  await report(h, 1, { message: ' TypeError: boom ', stack: 'at a', file: 'app.js', line: 3, column: 9, count: 4 })
  assert.deepEqual(reports, [{ message: 'TypeError: boom', stack: 'at a', file: 'app.js', line: 3, column: 9, count: 4 }])
  assert.deepEqual(sent[0], { id: 1, result: { recorded: true } })
})

test('page errors host: cuts the texts and fixes odd numbers', async () => {
  const { h, reports } = host()
  await report(h, 1, { message: 'm'.repeat(9000), stack: 's'.repeat(9000), file: 'f'.repeat(900), line: -4, column: 1.5, count: 1e9 })
  assert.equal(reports[0].message.length, MAX_ERROR_MESSAGE)
  assert.equal(reports[0].stack.length, MAX_ERROR_STACK)
  assert.equal(reports[0].file.length, 100)
  assert.deepEqual([reports[0].line, reports[0].column, reports[0].count], [0, 0, 1])
})

test('page errors host: browser noise is dropped, an empty message is refused', async () => {
  const { h, sent, reports } = host()
  await report(h, 1, { message: 'ResizeObserver loop limit exceeded' })
  await report(h, 2, { message: 'Script error.' })
  assert.deepEqual(sent.map((m) => m.result), [{ recorded: false }, { recorded: false }])
  assert.equal(reports.length, 0)
  await report(h, 3, { message: '   ' })
  await report(h, 4, {})
  assert.equal(sent[2].error.code, 'invalid')
  assert.equal(sent[3].error.code, 'invalid')
  assert.ok(isNoiseError('ResizeObserver loop completed with undelivered notifications.'))
  assert.ok(!isNoiseError('TypeError: ResizeObserver loop'))
})

test('page errors host: a burst is bounded, then one a second', async () => {
  const { h, sent, reports, at } = host()
  for (let i = 1; i <= 50; i++) await report(h, i, { message: `e${i}` })
  assert.equal(reports.length, 10, 'the burst')
  assert.ok(sent.slice(10).every((m) => m.result.recorded === false && !m.error), 'the rest is dropped without an error')
  at(3000)
  await report(h, 100, { message: 'later' })
  await report(h, 101, { message: 'later 2' })
  assert.equal(reports.length, 12, 'the budget comes back with time')
})

test('page errors host: without a recorder (a shared page) nothing is kept', async () => {
  const { h, sent, reports } = host({ errors: undefined })
  await report(h, 1, { message: 'TypeError: boom' })
  assert.deepEqual(sent[0].result, { recorded: false })
  assert.equal(reports.length, 0)
})

test('page errors host: a recorder that fails is never shown to the page', async () => {
  const { h, sent } = host({ errors: { report: async () => { throw Object.assign(new Error('server down'), { status: 500 }) } } })
  await report(h, 1, { message: 'TypeError: boom' })
  assert.deepEqual(sent[0], { id: 1, result: { recorded: false } })
})
