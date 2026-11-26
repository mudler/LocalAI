// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import {
  RELOAD_KEY, RELOAD_WINDOW_MS, createChunkRecovery, isChunkLoadError, routeErrorKind,
} from '../../src/utils/chunkReload.js'

// A server update removes the chunks an open tab still names. The recovery
// reloads once onto the current build and then lets the error reach the error
// screen, so a chunk that is really gone never reloads the app in a loop.

function memoryStorage(throws = false) {
  const data = new Map()
  return {
    getItem: (k) => { if (throws) throw new Error('denied'); return data.has(k) ? data.get(k) : null },
    setItem: (k, v) => { if (throws) throw new Error('denied'); data.set(k, String(v)) },
    data,
  }
}

function harness({ throws = false, start = 1_000_000 } = {}) {
  const store = memoryStorage(throws)
  const clock = { now: start }
  const reloads = []
  const recovery = createChunkRecovery({ storage: () => store, reload: () => reloads.push(clock.now), now: () => clock.now })
  return { store, clock, reloads, recovery }
}

const settled = (p) => Promise.race([p.then(() => 'resolved', () => 'rejected'), new Promise(r => setTimeout(() => r('pending'), 20))])

test('chunk recovery: the first failure reloads once and stays pending', async () => {
  const { recovery, reloads, store, clock } = harness()
  const p = recovery.recover(new TypeError('Failed to fetch dynamically imported module: /assets/InboxPanel-x.js'))
  assert.equal(await settled(p), 'pending')
  assert.deepEqual(reloads, [clock.now])
  assert.equal(store.data.get(RELOAD_KEY), String(clock.now))
  assert.equal(recovery.reloading, true)
})

test('chunk recovery: a second failure in the same document waits for the reload', async () => {
  const { recovery, reloads } = harness()
  recovery.recover(new Error('a'))
  assert.equal(await settled(recovery.recover(new Error('b'))), 'pending')
  assert.equal(reloads.length, 1)
})

test('chunk recovery: after a reload inside the window the error goes on', () => {
  const first = harness()
  first.recovery.recover(new Error('gone'))
  // The reloaded document: a new recovery over the same sessionStorage.
  const reloads = []
  const again = createChunkRecovery({ storage: () => first.store, reload: () => reloads.push(1), now: () => first.clock.now + RELOAD_WINDOW_MS - 1 })
  const err = new Error('still gone')
  assert.throws(() => again.recover(err), e => e === err)
  assert.deepEqual(reloads, [])
})

test('chunk recovery: once the window has passed a later update reloads again', async () => {
  const first = harness()
  first.recovery.recover(new Error('gone'))
  const reloads = []
  const later = createChunkRecovery({ storage: () => first.store, reload: () => reloads.push(1), now: () => first.clock.now + RELOAD_WINDOW_MS })
  assert.equal(await settled(later.recover(new Error('next deploy'))), 'pending')
  assert.equal(reloads.length, 1)
})

test('chunk recovery: without sessionStorage it never reloads', () => {
  const { recovery, reloads } = harness({ throws: true })
  assert.throws(() => recovery.recover(new Error('x')))
  assert.equal(recovery.reloadOnce(), false)
  assert.deepEqual(reloads, [])
})

test('chunk recovery: vite:preloadError is cancelled only when a reload starts', () => {
  const { recovery, reloads, store, clock } = harness()
  const event = () => ({ prevented: false, preventDefault() { this.prevented = true } })
  const e1 = event()
  recovery.onPreloadError(e1)
  assert.equal(e1.prevented, true)
  assert.equal(reloads.length, 1)

  // A spent window: the event is left alone, so Vite throws to the import.
  const spent = createChunkRecovery({ storage: () => store, reload: () => reloads.push(2), now: () => clock.now + 1 })
  const e2 = event()
  spent.onPreloadError(e2)
  assert.equal(e2.prevented, false)
  assert.equal(reloads.length, 1)
})

test('chunk recovery: the failed-import messages of each browser are chunk errors', () => {
  for (const msg of [
    'Failed to fetch dynamically imported module: https://example.test/assets/InboxPanel-BO4HGp7e.js',
    'error loading dynamically imported module: https://example.test/assets/Home-1.js',
    'Importing a module script failed.',
    'Unable to preload CSS for /assets/Home-1.css',
  ]) assert.equal(isChunkLoadError(new TypeError(msg)), true, msg)
  assert.equal(isChunkLoadError({ name: 'ChunkLoadError', message: '' }), true)
  assert.equal(isChunkLoadError(new TypeError("Cannot read properties of undefined (reading 'map')")), false)
  assert.equal(isChunkLoadError(null), false)
})

test('chunk recovery: routeErrorKind tells an update from offline and from a bug', () => {
  const chunk = new TypeError('Failed to fetch dynamically imported module: /assets/x.js')
  assert.equal(routeErrorKind(chunk, true), 'updated')
  assert.equal(routeErrorKind(chunk, false), 'offline')
  assert.equal(routeErrorKind(new Error('boom'), true), 'generic')
  assert.equal(routeErrorKind(new Error('boom'), false), 'generic')
})

// The production RouteError, compiled with its imports as boundaries.
const source = readFileSync(new URL('../../src/components/RouteError.jsx', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, '').replace('export default function', 'function'), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'RouteError.jsx',
}).outputText
const h = React.createElement

function renderRouteError({ error, online = true, inline = false, reloadOnce = () => false }) {
  Object.defineProperty(globalThis, 'navigator', { value: { onLine: online }, configurable: true })
  const scope = {
    React, useState: React.useState, useEffect: (fn) => fn(),
    useRouteError: () => error,
    useTranslation: () => ({ t: k => `[${k}]` }),
    Button: ({ to, onClick, children, variant, icon, busy }) => (to !== undefined
      ? h('a', { href: to, 'data-variant': variant, 'data-icon': icon }, children)
      : h('button', { type: 'button', 'data-variant': variant, 'data-icon': icon, 'data-busy': busy ? 'true' : undefined }, children)),
    Mascot: () => h('img', { alt: '' }),
    reloadOnce, routeErrorKind,
  }
  const RouteError = new Function(...Object.keys(scope), `${compiled}; return RouteError`)(...Object.values(scope))
  return renderToStaticMarkup(h(RouteError, { inline }))
}

test('route error: a failed chunk says the app was updated and offers Reload', () => {
  const calls = []
  const html = renderRouteError({ error: new TypeError('Failed to fetch dynamically imported module: /assets/InboxPanel-x.js'), reloadOnce: () => { calls.push(1); return false } })
  assert.match(html, /data-kind="updated"/)
  assert.match(html, /\[route\.updated\.title\]/)
  assert.match(html, /\[route\.updated\.text\]/)
  assert.match(html, /<button[^>]*data-icon="refresh"[^>]*>\[route\.reload\]<\/button>/)
  assert.match(html, /role="alert"/)
  // It tried its own one reload; the window was spent, so it shows the screen.
  assert.equal(calls.length, 1)
  // An update is not a bug: no technical details.
  assert.doesNotMatch(html, /route\.details/)
  assert.doesNotMatch(html, /Unexpected Application Error/)
})

test('route error: offline says so and does not reload on its own', () => {
  const calls = []
  const html = renderRouteError({ error: new TypeError('Failed to fetch dynamically imported module: /assets/x.js'), online: false, reloadOnce: () => { calls.push(1); return true } })
  assert.match(html, /data-kind="offline"/)
  assert.match(html, /\[route\.offline\.title\]/)
  assert.equal(calls.length, 0)
})

test('route error: any other error is generic, with its message under Details', () => {
  const calls = []
  const html = renderRouteError({ error: new Error('x.map is not a function'), reloadOnce: () => { calls.push(1); return true } })
  assert.match(html, /data-kind="generic"/)
  assert.match(html, /\[route\.generic\.title\]/)
  assert.match(html, /\[route\.details\]/)
  assert.match(html, /x\.map is not a function/)
  assert.equal(calls.length, 0)
})

test('route error: inline adds a way Home; the full screen does not', () => {
  const err = new Error('boom')
  assert.match(renderRouteError({ error: err, inline: true }), /<a href="\/app"[^>]*>\[route\.home\]<\/a>/)
  assert.match(renderRouteError({ error: err, inline: true }), /route-error--inline/)
  assert.doesNotMatch(renderRouteError({ error: err }), /route\.home/)
})
