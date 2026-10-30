import test from 'node:test'
import assert from 'node:assert/strict'
import {
  viewOf, languageOf, isPreviewableBlock, blockTitle, presentedName, codeBlocksOf, previewableBlocks,
  autoOpenTarget, targetKey, turnFiles, isPhoneWidth, closedKey, readClosed, isClosed, setClosed, MAX_CLOSED,
  MIN_BLOCK_LINES, blockView,
} from '../../src/utils/previewPane.js'

function memoryStorage(seed = {}) {
  const m = new Map(Object.entries(seed))
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)) },
    removeItem: (k) => { m.delete(k) },
    dump: () => Object.fromEntries(m),
  }
}
const throwing = {
  getItem() { throw new Error('denied') },
  setItem() { throw new Error('denied') },
  removeItem() { throw new Error('denied') },
}
const lines = (n) => Array.from({ length: n }, (_, i) => `line ${i}`).join('\n')

test('preview pane: every file type gets its view, and an unknown type gets the card', () => {
  const cases = {
    'budget.xlsx': 'table', 'm.XLSM': 'table', 'q.csv': 'table', 'q.tsv': 'table',
    'r.docx': 'document', 'd.pptx': 'slides', 'i.pdf': 'pdf', 'p.html': 'html', 'p.htm': 'html',
    'a.png': 'image', 'logo.svg': 'image', 's.mp3': 'audio', 'v.webm': 'video',
    'n.md': 'markdown', 'c.json': 'json', 'app.py': 'code', 'main.go': 'code', 'notes.txt': 'text',
    'archive.zip': 'none', 'old.xls': 'none', 'old.doc': 'none', 'noext': 'none',
  }
  for (const [name, view] of Object.entries(cases)) assert.equal(viewOf(name), view, name)
  assert.equal(viewOf('blob', 'image/png'), 'image')
  assert.equal(viewOf('blob', 'text/plain'), 'text')
  assert.equal(languageOf('app.py'), 'python')
  assert.equal(languageOf('x.unknown'), '')
})

test('preview pane: a page, an SVG, a long block or a block presented as a file is previewable; a snippet is not', () => {
  assert.equal(isPreviewableBlock({ lang: 'html', code: '<!doctype html>\n<p>hi</p>' }), true)
  assert.equal(isPreviewableBlock({ lang: 'html', code: '<html><body>x</body></html>' }), true)
  assert.equal(isPreviewableBlock({ lang: 'html', code: '<p>a fragment</p>' }), false, 'a fragment is a snippet')
  assert.equal(isPreviewableBlock({ lang: 'svg', code: '<svg viewBox="0 0 10 10"></svg>' }), true)
  assert.equal(isPreviewableBlock({ lang: '', code: '<svg/>' }), true)
  assert.equal(isPreviewableBlock({ lang: 'python', code: lines(MIN_BLOCK_LINES) }), true)
  assert.equal(isPreviewableBlock({ lang: 'python', code: lines(MIN_BLOCK_LINES - 1) }), false)
  assert.equal(isPreviewableBlock({ lang: 'bash', code: 'ls -la' }), false)
  assert.equal(isPreviewableBlock({ lang: 'python', code: lines(4), before: 'Save this as `report.py`:\n' }), true)
  assert.equal(isPreviewableBlock({ lang: 'python', code: 'x = 1', before: 'Save this as `report.py`:\n' }), false, 'a one-liner stays a snippet')
  assert.equal(isPreviewableBlock({ lang: 'python', code: lines(4), before: 'For example, e.g. v1.2:\n' }), false)
})

test('preview pane: a block is titled by the name the text gave it, else by its language', () => {
  assert.equal(presentedName('Here it is.\n\n**index.html**:\n'), 'index.html')
  assert.equal(presentedName('Save it as `app.py`'), 'app.py')
  assert.equal(presentedName('Version 1.2 is out'), '')
  assert.equal(blockTitle('html', 0, ''), 'index.html')
  assert.equal(blockTitle('python', 1, ''), 'script-2.py')
  assert.equal(blockTitle('python', 1, 'utils.py:'), 'utils.py')
  assert.equal(blockTitle('weird', 0, ''), 'snippet.txt')
  assert.equal(blockView('svg', '<svg/>'), 'image')
  assert.equal(blockView('html', '<!doctype html><p>'), 'html')
  assert.equal(blockView('html', '<p>'), 'code')
  assert.equal(blockView('md', '# a'), 'markdown')
})

test('preview pane: code blocks are read in order with the text before each, and an unclosed block is left out', () => {
  const text = 'Intro\n\n```html\n<!doctype html>\n<p>x</p>\n```\n\nThen `inline` and:\n\n~~~py\nprint(1)\n~~~\n\n```js\nnot closed'
  const blocks = codeBlocksOf(text)
  assert.equal(blocks.length, 2)
  assert.deepEqual(blocks.map(b => [b.index, b.lang, b.code]), [[0, 'html', '<!doctype html>\n<p>x</p>'], [1, 'py', 'print(1)']])
  assert.match(blocks[1].before, /Then `inline` and:/)
})

test('preview pane: only the assistant\'s previewable blocks are offered, with stable ids', () => {
  const messages = [
    { seq: 1, role: 'user', text: '```html\n<!doctype html>\n```' },
    { seq: 2, role: 'assistant', text: 'Short:\n```sh\nls\n```\nPage:\n```html\n<!doctype html>\n<h1>Hi</h1>\n```' },
  ]
  const blocks = previewableBlocks(messages)
  assert.deepEqual(blocks.map(b => [b.id, b.name, b.view]), [['2-1', 'index-2.html', 'html']])
})

test('preview pane: the chips of a turn list its created files before its changed ones, never the user\'s', () => {
  const files = [
    { path: '/a.csv', name: 'a.csv', origin: 'user', turn: 2, size: 1 },
    { path: '/b.md', name: 'b.md', origin: 'tool', turn: 2, changed: true, size: 2 },
    { path: '/c.xlsx', name: 'c.xlsx', origin: 'model', turn: 2, size: 3 },
    { path: '/d.txt', name: 'd.txt', origin: 'model', turn: 1, size: 4 },
  ]
  assert.deepEqual(turnFiles(files, 2), [
    { path: '/c.xlsx', name: 'c.xlsx', size: 3, kind: 'created' },
    { path: '/b.md', name: 'b.md', size: 2, kind: 'changed' },
  ])
  assert.deepEqual(turnFiles(files, 0), [])
})

test('preview pane: a new file of the assistant opens the pane, the newest first, and wins over a block', () => {
  const since = { turn: 1, seq: 2 }
  const files = [
    { path: '/old.xlsx', name: 'old.xlsx', origin: 'model', turn: 1, at: '2026-10-30T09:00:00Z' },
    { path: '/in.csv', name: 'in.csv', origin: 'user', turn: 2, at: '2026-10-30T10:00:00Z' },
    { path: '/a.md', name: 'a.md', origin: 'tool', turn: 2, at: '2026-10-30T10:00:01Z' },
    { path: '/budget.xlsx', name: 'budget.xlsx', origin: 'model', turn: 2, at: '2026-10-30T10:00:02Z' },
  ]
  const messages = [{ seq: 4, role: 'assistant', text: '```html\n<!doctype html>\n```' }]
  assert.deepEqual(autoOpenTarget({ files, messages, since }), { type: 'file', path: '/budget.xlsx', name: 'budget.xlsx' })
  assert.deepEqual(autoOpenTarget({ files: files.slice(0, 2), messages, since }), { type: 'block', id: '4-0', name: 'index.html' })
})

test('preview pane: nothing opens on a reload, an old chat, the user\'s own upload or a snippet', () => {
  const old = [{ path: '/r.xlsx', name: 'r.xlsx', origin: 'model', turn: 3 }]
  assert.equal(autoOpenTarget({ files: old, messages: [], since: { turn: 3, seq: 9 } }), null)
  assert.equal(autoOpenTarget({ files: old, messages: [], since: null }), null, 'no watermark yet')
  assert.equal(autoOpenTarget({ files: [{ path: '/u.pdf', name: 'u.pdf', origin: 'user', turn: 4 }], messages: [], since: { turn: 3, seq: 9 } }), null)
  const snippet = [{ seq: 10, role: 'assistant', text: 'Run:\n```sh\nmake build\n```' }]
  assert.equal(autoOpenTarget({ files: [], messages: snippet, since: { turn: 3, seq: 9 } }), null)
  const oldBlock = [{ seq: 9, role: 'assistant', text: '```svg\n<svg/>\n```' }]
  assert.equal(autoOpenTarget({ files: [], messages: oldBlock, since: { turn: 3, seq: 9 } }), null)
  assert.equal(targetKey({ type: 'file', path: '/a' }), 'file:/a')
  assert.equal(targetKey({ type: 'block', id: '4-0' }), 'block:4-0')
  assert.equal(targetKey(null), '')
})

test('preview pane: a phone never opens the pane by itself', () => {
  assert.equal(isPhoneWidth(390), true)
  assert.equal(isPhoneWidth(639), true)
  assert.equal(isPhoneWidth(640), false)
  assert.equal(isPhoneWidth(1440), false)
  assert.equal(isPhoneWidth(undefined), false)
})

test('preview pane: a closed pane is remembered per chat and per account, and opening it again forgets it', () => {
  const st = memoryStorage()
  const key = closedKey('u1')
  assert.equal(key, 'localai_chat_preview_closed:u1')
  assert.notEqual(closedKey('u2'), key)
  assert.equal(isClosed(st, key, 'c1'), false)
  setClosed(st, key, 'c1', true, 100)
  assert.equal(isClosed(st, key, 'c1'), true)
  assert.equal(isClosed(st, key, 'c2'), false, 'another chat is not closed')
  assert.equal(isClosed(st, closedKey('u2'), 'c1'), false, 'another account is not closed')
  setClosed(st, key, 'c1', false)
  assert.equal(isClosed(st, key, 'c1'), false)
  assert.equal(st.getItem(key), null, 'an empty memory leaves nothing behind')
  setClosed(st, key, '', true)
  assert.deepEqual(st.dump(), {}, 'a new chat is never written')
})

test('preview pane: the closed memory keeps the newest chats only, and survives a broken or blocked storage', () => {
  const st = memoryStorage()
  const key = closedKey('u1')
  for (let i = 0; i < MAX_CLOSED + 5; i++) setClosed(st, key, `c${i}`, true, i)
  const kept = readClosed(st, key)
  assert.equal(Object.keys(kept).length, MAX_CLOSED)
  assert.equal('c0' in kept, false)
  assert.equal(`c${MAX_CLOSED + 4}` in kept, true)
  assert.deepEqual(readClosed(memoryStorage({ [key]: '{nope' }), key), {})
  assert.deepEqual(readClosed(memoryStorage({ [key]: '[1,2]' }), key), {})
  assert.deepEqual(readClosed(throwing, key), {})
  assert.doesNotThrow(() => setClosed(throwing, key, 'c1', true))
  assert.equal(isClosed(null, key, 'c1'), false)
})
