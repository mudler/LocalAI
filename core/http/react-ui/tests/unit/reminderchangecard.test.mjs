// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import React, { act } from 'react'
import { createServer } from 'vite'

// The change card of a reminder that the model proposed in a chat: every state
// it can be in, and that its buttons call what they say.
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { createRoot } = await import('react-dom/client')

const server = await createServer({
  configFile: false,
  ssr: { noExternal: ['react-i18next'] },
  server: { middlewareMode: true },
  plugins: [{
    name: 'reminder-card-stubs',
    enforce: 'pre',
    resolveId(id) {
      if (id === 'react-i18next' || id === '../Icon' || id === '../ui') return `\0reminder-card-stub:${id}`
    },
    load(id) {
      if (!id.startsWith('\0reminder-card-stub:')) return
      // The key and its values, so the test sees which text and which time.
      if (id.endsWith('react-i18next')) return "export const useTranslation = () => ({ t: (k, o) => o ? `${k} ${JSON.stringify(o)}` : k, i18n: { language: 'en' } })"
      if (id.endsWith('../Icon')) return 'export default function Icon() { return null }'
      return `import React from 'react'
export function Button({ children, onClick, disabled, to, variant }) {
  return to ? React.createElement('a', { href: to, 'data-variant': variant }, children) : React.createElement('button', { type: 'button', onClick, disabled, 'data-variant': variant }, children)
}`
    },
  }],
})
const { default: ReminderChangeCard } = await server.ssrLoadModule('/src/components/chat/ReminderChangeCard.jsx')
test.after(() => server.close())

const reminder = (extra = {}) => ({ watch_id: 'w1', watch_name: 'My calendar', watch_zone: 'UTC', title: 'Dentist',
  before: { due: '2026-11-26T08:45:00Z', offset_minutes: -15, task: 'remind me' }, after: { due: '2026-11-26T08:30:00Z', offset_minutes: -30, task: 'remind me' }, ...extra })
const view = (extra = {}) => ({ kind: 'pending', op: 'update', stale: false, canUndo: false, blocked: '', reminder: reminder(), ...extra })

async function mount(props) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const root = createRoot(el)
  await act(async () => root.render(React.createElement(ReminderChangeCard, { busy: '', apply() {}, dismiss() {}, undo() {}, alert: null, ...props })))
  return { el, async close() { await act(async () => root.unmount()); el.remove() } }
}
const buttons = (el) => [...el.querySelectorAll('button')].map((b) => b.textContent)

test('reminder card: a waiting change shows the watch, the time before and after, Approve, Edit and Not now', async () => {
  const calls = []
  const m = await mount({ view: view(), apply: () => calls.push('apply'), dismiss: () => calls.push('dismiss') })
  const card = m.el.querySelector('[data-testid="reminder-change"]')
  assert.equal(card.dataset.state, 'pending')
  assert.match(card.textContent, /watch\.change\.title\.update .*Dentist/)
  assert.match(card.querySelector('[data-row="watch"]').textContent, /My calendar/)
  assert.match(card.querySelector('[data-row="when"] [data-testid="change-before"]').textContent, /08:45/, 'in the zone of the watch')
  assert.match(card.querySelector('[data-row="when"] [data-testid="change-after"]').textContent, /08:30/)
  assert.equal(card.querySelector('[data-row="task"]'), null, 'an unchanged task has no row')
  assert.deepEqual(buttons(m.el), ['proposal.approve', 'proposal.notNow'])
  assert.equal(m.el.querySelector('a').getAttribute('href'), '/app/triggers/w1', 'Edit opens the watch')
  await act(async () => m.el.querySelectorAll('button')[0].click())
  await act(async () => m.el.querySelectorAll('button')[1].click())
  assert.deepEqual(calls, ['apply', 'dismiss'])
  await m.close()
})

test('reminder card: a stale change has no Approve, and a cancel is a danger button with no after', async () => {
  let m = await mount({ view: view({ stale: true }) })
  assert.match(m.el.textContent, /watch\.change\.stale/)
  assert.deepEqual(buttons(m.el), ['proposal.notNow'])
  await m.close()
  m = await mount({ view: view({ op: 'delete', reminder: reminder({ after: undefined }) }) })
  assert.equal(m.el.querySelector('button').dataset.variant, 'danger')
  assert.match(m.el.querySelector('[data-row="when"] [data-testid="change-after"]').textContent, /watch\.change\.none/)
  await m.close()
})

test('reminder card: an applied change has View watch and Undo, or says why it cannot be undone', async () => {
  let undone = 0
  let m = await mount({ view: view({ kind: 'applied', canUndo: true }), undo: () => { undone++ } })
  assert.equal(m.el.querySelector('[data-testid="reminder-change"]').dataset.state, 'applied')
  assert.deepEqual(buttons(m.el), ['proposal.receipt.undo'])
  assert.equal(m.el.querySelector('a').textContent, 'watch.viewWatch')
  await act(async () => m.el.querySelector('button').click())
  assert.equal(undone, 1)
  await m.close()
  m = await mount({ view: view({ kind: 'applied', canUndo: false, blocked: 'edited' }) })
  assert.deepEqual(buttons(m.el), [])
  assert.match(m.el.querySelector('[data-testid="change-blocked"]').textContent, /watch\.change\.blocked\.edited/)
  await m.close()
})

test('reminder card: a dismissed or undone change is a quiet receipt with no buttons', async () => {
  for (const kind of ['dismissed', 'undone']) {
    const m = await mount({ view: view({ kind }) })
    const card = m.el.querySelector('[data-testid="reminder-change"]')
    assert.equal(card.dataset.state, kind)
    assert.match(card.textContent, new RegExp(`watch\\.change\\.receipt\\.${kind}`))
    assert.deepEqual(buttons(m.el), [])
    await m.close()
  }
})
