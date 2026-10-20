import test from 'node:test'
import assert from 'node:assert/strict'
import * as React from 'react'
import { renderToString } from 'react-dom/server'
import { createHostSDK, coreRequestUrl, ICON_NAMES, SDK_VERSION } from '../../src/addons/sdk.js'

const C = (tag) => (p) => React.createElement(tag, { 'data-props': JSON.stringify(Object.keys(p).sort()) }, p.children)
const NAMES = ['PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'ModelSelector', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch']

function make(host = {}) {
  const controller = new AbortController()
  const components = Object.fromEntries(NAMES.map(n => [n, C('div')]))
  const sdk = createHostSDK({
    addonId: 'voice', generation: 1, signal: controller.signal, pages: ['recognition'],
    host: {
      React, components, Icon: () => null, hooks: { useMediaCapture: () => 'cap', useObjectUrl: () => 'url', useDelayedAction: () => 'act', audioBufferToWavBlob: () => 'wav' },
      viewer: { isAdmin: () => true, hasFeature: (f) => f === 'face_recognition' },
      language: () => 'en', theme: { mode: () => 'light', subscribe: () => () => {} }, subscribeLanguage: () => () => {}, api: {},
      ...host,
    },
  })
  return { sdk, controller }
}

test('SDK version is 2', () => assert.equal(SDK_VERSION, 2))

test('sdk.ui exposes every shared primitive and widget, and the wrapper passes props through unchanged', () => {
  const { sdk } = make()
  for (const n of NAMES) assert.equal(typeof sdk.ui[n], 'function', n)
  const html = renderToString(React.createElement(sdk.ui.PageHeader, { title: 'T', supporting: 'S', actions: null }))
  assert.match(html, /\[&quot;actions&quot;,&quot;supporting&quot;,&quot;title&quot;\]/)
})

test('sdk.hooks exposes the shared hooks as the host functions', () => {
  const { sdk } = make()
  assert.equal(sdk.hooks.useMediaCapture('audio'), 'cap')
  assert.equal(sdk.hooks.useObjectUrl(null), 'url')
  assert.equal(sdk.hooks.useDelayedAction(() => {}), 'act')
  assert.equal(sdk.hooks.audioBufferToWavBlob({}), 'wav')
})

test('sdk.viewer answers for the signed-in user and nothing else', () => {
  const { sdk } = make()
  assert.equal(sdk.viewer.isAdmin(), true)
  assert.equal(sdk.viewer.hasFeature('face_recognition'), true)
  assert.equal(sdk.viewer.hasFeature('addons'), false)
  assert.deepEqual(Object.keys(sdk.viewer).sort(), ['hasFeature', 'isAdmin'])
})

test('a host without viewer or hooks gives a closed viewer and no hooks, never a throw', () => {
  const { sdk } = make({ viewer: undefined, hooks: undefined })
  assert.equal(sdk.viewer.isAdmin(), false)
  assert.equal(sdk.viewer.hasFeature('x'), false)
  assert.deepEqual(Object.keys(sdk.hooks), [])
})

test('the icon allowlist holds the names the voice and face pages draw', () => {
  for (const n of ['arrow-left', 'arrow-right', 'chart-bar', 'circle-dot', 'clipboard', 'code', 'cube', 'filter-off', 'gender', 'globe', 'headphones', 'help-circle', 'mic', 'play', 'plug-off', 'save', 'shield', 'smile', 'waveform', 'camera', 'stop', 'circle', 'image', 'spinner', 'user-shield', 'mask']) {
    assert.ok(ICON_NAMES.includes(n), n)
  }
})

test('the hooks and the viewer stay frozen', () => {
  const { sdk } = make()
  assert.throws(() => { 'use strict'; sdk.viewer.isAdmin = () => false }, TypeError)
  assert.throws(() => { 'use strict'; sdk.hooks.extra = 1 }, TypeError)
})

test('coreRequestUrl accepts same-origin core paths and keeps the base path and query', () => {
  assert.equal(coreRequestUrl('/v1/face/verify'), '/v1/face/verify')
  assert.equal(coreRequestUrl('/api/voice-profiles?x=1', '/localai'), '/localai/api/voice-profiles?x=1')
  assert.equal(coreRequestUrl('/v1/audio/speech#frag'), '/v1/audio/speech')
  assert.equal(coreRequestUrl('/api/voice-profiles/a%2Fb'), '/api/voice-profiles/a%2Fb')
})

test('coreRequestUrl refuses what is not a plain core API path', () => {
  for (const bad of [
    'https://evil.example/v1/x', '//evil.example/x', 'v1/x', '', '/v1/../api/addons/apps', '/v1/%2e%2e/x',
    '/api/addons/apps', '/api/addons', '/API/ADDONS/apps', '/api/auth/status', '/api/auth', '/v1\\x', 7, null, undefined,
    // The denied prefixes written with percent-encoding.
    '/api/%61ddons/apps', '/api/addons%2Fapps', '/api/auth%2Fstatus', '/api%2Faddons/apps', '/%61pi/auth/status', '/api/addons%5Capps',
  ]) {
    assert.equal(coreRequestUrl(bad), null, String(bad))
  }
})

function withFetch() {
  const calls = []
  const { sdk, controller } = make({ basePath: '', fetch: async (url, init) => { calls.push([url, init]); return { ok: true } } })
  return { sdk, controller, calls }
}

test('sdk.core.fetch calls with the browser session and no credential of its own', async () => {
  const { sdk, calls } = withFetch()
  await sdk.core.fetch('/v1/face/verify', { method: 'POST', headers: { Authorization: 'Bearer x', Cookie: 'a=b', 'Content-Type': 'application/json' }, body: '{}' })
  const [url, init] = calls[0]
  assert.equal(url, '/v1/face/verify')
  assert.equal(init.credentials, 'same-origin')
  assert.equal(init.mode, 'same-origin')
  assert.equal(init.redirect, 'error')
  assert.equal(init.headers.has('authorization'), false)
  assert.equal(init.headers.has('cookie'), false)
  assert.equal(init.headers.get('content-type'), 'application/json')
})

test('sdk.core.fetch refuses a bad path without calling the network', async () => {
  const { sdk, calls } = withFetch()
  await assert.rejects(() => sdk.core.fetch('/api/addons/apps'), (e) => e.code === 'invalid_path')
  await assert.rejects(() => sdk.core.fetch('https://evil.example/'), (e) => e.code === 'invalid_path')
  assert.equal(calls.length, 0)
})

test('sdk.core.fetch refuses after the add-on is withdrawn', async () => {
  const { sdk, controller, calls } = withFetch()
  controller.abort('disabled')
  await assert.rejects(() => sdk.core.fetch('/v1/voice/verify'), (e) => e.code === 'withdrawn')
  assert.equal(calls.length, 0)
})

test('sdk.core.fetch aborts an in-flight call when the add-on is withdrawn', async () => {
  let seen
  const { sdk, controller } = make({ basePath: '', fetch: (url, init) => new Promise((_, reject) => { seen = init.signal; init.signal.addEventListener('abort', () => reject(new Error('aborted'))) }) })
  const p = sdk.core.fetch('/v1/voice/verify')
  controller.abort('disabled')
  await assert.rejects(p, /aborted/)
  assert.equal(seen.aborted, true)
})
