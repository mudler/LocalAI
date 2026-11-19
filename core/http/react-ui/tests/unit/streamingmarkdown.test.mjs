// The streaming answer is drawn in segments so that a flush touches only the
// trailing block. These cases pin that on a real DOM: a growing code block is
// one <code> with one Text node, and a finished paragraph is not rebuilt when
// later text arrives. The old single-dangerouslySetInnerHTML renderer replaced
// every node of the answer on each flush, which is the churn this must catch.
import test, { after, before } from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import React, { act } from 'react'
import { createServer } from 'vite'
import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.IS_REACT_ACT_ENVIRONMENT = true

// Prose asks for the chat namespace (the image fallback label). An empty bundle
// is enough; the real react-i18next is used, not a stub.
const i18n = i18next.createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: { en: { chat: {} } }, ns: ['chat'], defaultNS: 'chat' })

// React DOM detects input event support at import time, after JSDOM exists.
const { createRoot } = await import('react-dom/client')

// The unit runner is plain `node`, so a JSX module needs Vite's SSR loader.
let server
let StreamingMarkdown
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true } })
  ;({ default: StreamingMarkdown } = await server.ssrLoadModule('/src/components/chat/StreamingMarkdown.jsx'))
})
after(async () => { await server?.close() })

async function mount(text) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => root.render(React.createElement(StreamingMarkdown, { text })))
  return {
    container,
    async update(next) { await act(async () => root.render(React.createElement(StreamingMarkdown, { text: next }))) },
    async close() { await act(async () => root.unmount()); container.remove() },
  }
}

test('stream render: a growing code block keeps one element and one text node', async () => {
  const h = await mount('```js\nlet a = 1\n')
  try {
    const code = h.container.querySelector('code')
    assert.ok(code, 'an open fence renders a <code>')
    assert.equal(code.childNodes.length, 1)
    assert.equal(code.childNodes[0].nodeType, 3)
    const text = code.childNodes[0]
    await h.update('```js\nlet a = 1\nlet b = 2\n')
    const next = h.container.querySelector('code')
    assert.equal(next, code, 'the same <code> element must survive the flush')
    assert.equal(next.childNodes.length, 1)
    assert.equal(next.childNodes[0], text, 'the same Text node must survive the flush')
    assert.equal(next.childNodes[0].nodeValue, 'let a = 1\nlet b = 2\n')
  } finally { await h.close() }
})

test('stream render: no highlighter span and no code-block chrome while streaming', async () => {
  const h = await mount('```js\nlet a = 1\n')
  try {
    const out = h.container.firstElementChild
    assert.ok(out.querySelector('code'), 'the code block is rendered')
    assert.equal(out.querySelector('span[class*="hljs"]'), null)
    assert.equal(out.querySelector('.code-block'), null)
    assert.equal(out.querySelector('button'), null)
  } finally { await h.close() }
})

test('stream render: a finished paragraph is not rebuilt when later text arrives', async () => {
  const h = await mount('First paragraph.\n\nSecond')
  try {
    const first = h.container.querySelector('p')
    assert.equal(first.textContent, 'First paragraph.')
    await h.update('First paragraph.\n\nSecond one grows.')
    const paras = h.container.querySelectorAll('p')
    assert.equal(paras[0], first, 'the finished paragraph must be the same element')
    assert.equal(paras[0].textContent, 'First paragraph.')
    assert.equal(paras[paras.length - 1].textContent, 'Second one grows.')
  } finally { await h.close() }
})

test('stream render: the live container carries the caret hooks', async () => {
  const h = await mount('One.\n\nTwo.')
  try {
    const live = h.container.firstElementChild
    assert.ok(live.classList.contains('cx-prose--live'), live.className)
    const segs = [...live.children]
    assert.equal(segs.length, 2)
    assert.ok(segs.every(seg => seg.classList.contains('cx-seg')), 'each segment is a direct child carrying cx-seg')
    assert.ok(live.lastElementChild.classList.contains('cx-seg'))
  } finally { await h.close() }
})

test('stream render: an unterminated fence renders as code, not prose', async () => {
  const h = await mount('Here:\n\n```py\nprint(1)')
  try {
    const code = h.container.querySelector('code')
    assert.ok(code, 'the open fence is a <code>')
    assert.ok(code.classList.contains('language-py'), code.className)
    assert.equal(code.textContent, 'print(1)')
    const prose = code.closest('.cx-seg').previousElementSibling
    assert.ok(prose.classList.contains('cx-seg'))
    assert.equal(prose.querySelector('p').textContent, 'Here:')
  } finally { await h.close() }
})

test('stream render: settled text after a fence is its own prose block', async () => {
  const h = await mount('Before.\n\n```js\nx\n```\n\nAfter.')
  try {
    const segs = [...h.container.firstElementChild.children]
    assert.ok(segs.every(seg => seg.classList.contains('cx-seg')))
    assert.deepEqual(segs.map(seg => (seg.querySelector('pre > code') ? 'code' : 'prose')), ['prose', 'code', 'prose'])
    assert.equal(segs[0].querySelector('p').textContent, 'Before.')
    assert.equal(segs[1].querySelector('code').textContent, 'x')
    assert.equal(segs[2].querySelector('p').textContent, 'After.')
  } finally { await h.close() }
})
