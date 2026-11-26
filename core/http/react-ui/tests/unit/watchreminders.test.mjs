// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import React, { act } from 'react'
import { createServer } from 'vite'

// The reminder list of a watch: one change at a time, so a second Save while
// the first is on its way sends nothing.
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.FormData = dom.window.FormData // the form reads its fields with the DOM's own FormData
globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { createRoot } = await import('react-dom/client')

// The test's API: what each call does is set by the test.
globalThis.__watchApi = {}
const server = await createServer({
  configFile: false,
  ssr: { noExternal: ['react-i18next'] },
  server: { middlewareMode: true },
  plugins: [{
    name: 'watch-reminders-stubs',
    enforce: 'pre',
    resolveId(id) {
      if (id === 'react-i18next' || id === '../utils/api' || id === './ui') return `\0watch-reminders-stub:${id}`
    },
    load(id) {
      if (!id.startsWith('\0watch-reminders-stub:')) return
      if (id.endsWith('react-i18next')) return "export const useTranslation = () => ({ t: (k) => k, i18n: { language: 'en' } })"
      if (id.endsWith('../utils/api')) {
        return `export const chatTriggersApi = {
  reminders: (...a) => globalThis.__watchApi.reminders(...a),
  patchReminder: (...a) => globalThis.__watchApi.patchReminder(...a),
}`
      }
      return `import React from 'react'
export function Button({ children, onClick, disabled, type, busy }) {
  return React.createElement('button', { type: type || 'button', onClick, disabled, 'data-busy': busy ? '1' : '' }, children)
}
export function Callout({ children, actions }) { return React.createElement('div', null, children, actions) }
export function LoadError({ title }) { return React.createElement('p', { role: 'alert' }, title) }
export function SkeletonRows() { return null }
export function Tag({ children }) { return React.createElement('span', null, children) }`
    },
  }],
})
const { default: WatchReminders } = await server.ssrLoadModule('/src/components/WatchReminders.jsx')
test.after(() => server.close())

const reminder = { id: 'r1', title: 'Dentist', task: 'remind me', due: '2026-11-26T08:45:00Z', offset_minutes: -15 }

test('watch reminders: a second Save while the first is on its way sends nothing', async () => {
  const sent = []
  let finish
  globalThis.__watchApi = {
    reminders: async () => ({ reminders: [reminder] }),
    patchReminder: (...a) => { sent.push(a); return new Promise((resolve) => { finish = () => resolve(reminder) }) },
  }
  const el = document.createElement('div')
  document.body.appendChild(el)
  const root = createRoot(el)
  await act(async () => root.render(React.createElement(WatchReminders, { watch: { id: 'w1', time_zone: 'UTC' } })))
  const change = [...el.querySelectorAll('button')].find((b) => b.textContent === 'watch.changeTime')
  await act(async () => change.click())
  const form = el.querySelector('form')
  await act(async () => form.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => form.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true })))
  assert.equal(sent.length, 1, 'one change at a time')
  const save = [...el.querySelectorAll('button')].find((b) => b.textContent === 'watch.saveTime')
  assert.equal(save.disabled, true)
  await act(async () => finish())
  await act(async () => root.unmount())
  el.remove()
})
