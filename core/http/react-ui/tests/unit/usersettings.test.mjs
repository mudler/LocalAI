import test from 'node:test'
import assert from 'node:assert/strict'
import * as React from 'react'
import { createHostSDK } from '../../src/addons/sdk.js'
import { userSettingsRequest } from '../../src/addons/userSettingsCalls.js'

test('user settings: a read is a GET of the add-on\'s own route', () => {
  assert.deepEqual(userSettingsRequest({ op: 'get', addonId: 'memory' }), {
    method: 'GET', path: '/api/addons/memory/user-settings', headers: { Accept: 'application/json' },
  })
})

test('user settings: a write is a PUT with the own-session confirmation naming the add-on', () => {
  const r = userSettingsRequest({ op: 'set', addonId: 'memory', key: 'save-mode', value: 'auto' })
  assert.equal(r.method, 'PUT')
  assert.equal(r.path, '/api/addons/memory/user-settings')
  assert.equal(r.headers['X-LocalAI-Confirm'], 'memory')
  assert.deepEqual(r.body, { key: 'save-mode', value: 'auto' })
})

test('user settings: a malformed call is refused before it is sent', () => {
  const bad = [
    { op: 'get', addonId: '../x' },
    { op: 'get', addonId: 'Memory' },
    { op: 'get', addonId: '' },
    { op: 'get' },
    { op: 'delete', addonId: 'memory' },
    { op: 'set', addonId: 'memory', key: 'Save Mode', value: 'auto' },
    { op: 'set', addonId: 'memory', key: 'save-mode', value: '' },
    { op: 'set', addonId: 'memory', key: 'save-mode', value: 'AUTO' },
    { op: 'set', addonId: 'memory', key: 'save-mode', value: 'a b' },
    { op: 'set', addonId: 'memory', key: '', value: 'auto' },
    null,
  ]
  for (const call of bad) assert.throws(() => userSettingsRequest(call), TypeError, JSON.stringify(call))
})

function make(addonId, api) {
  const controller = new AbortController()
  const sdk = createHostSDK({
    addonId, generation: 3, signal: controller.signal, pages: ['p'],
    host: { React, components: {}, Icon: () => null, hooks: {}, language: () => 'en', theme: { mode: () => 'light', subscribe: () => () => {} }, subscribeLanguage: () => () => {}, api },
  })
  return { sdk, controller }
}

test('sdk.userSettings: every call is bound to the add-on that makes it', async () => {
  const seen = []
  const { sdk } = make('memory', { userSettings: async (call) => { seen.push(call); return { settings: [] } } })
  await sdk.userSettings.get()
  await sdk.userSettings.set('save-mode', 'never')
  assert.deepEqual(seen.map((c) => [c.op, c.addonId, c.key, c.value]), [['get', 'memory', undefined, undefined], ['set', 'memory', 'save-mode', 'never']])
})

test('sdk.userSettings: a page cannot name another add-on, whatever it passes', async () => {
  const seen = []
  const { sdk } = make('notes', { userSettings: async (call) => { seen.push(call); return {} } })
  await sdk.userSettings.get({ addonId: 'memory' })
  assert.equal(seen[0].addonId, 'notes')
})

test('sdk.userSettings: a bad key or value never reaches the host', async () => {
  let called = 0
  const { sdk } = make('memory', { userSettings: async () => { called++; return {} } })
  await assert.rejects(sdk.userSettings.set('../x', 'auto'), TypeError)
  await assert.rejects(sdk.userSettings.set('save-mode', 'a/b'), TypeError)
  await assert.rejects(sdk.userSettings.set('save-mode', 42), TypeError)
  assert.equal(called, 0)
})

test('sdk.userSettings: it stops when the add-on is withdrawn', async () => {
  const { sdk, controller } = make('memory', { userSettings: async () => ({}) })
  controller.abort()
  await assert.rejects(sdk.userSettings.get(), (e) => e.code === 'withdrawn')
})

test('sdk.core.fetch still refuses the add-on API, so the binding is the only way in', async () => {
  const { sdk } = make('memory', {})
  await assert.rejects(sdk.core.fetch('/api/addons/memory/user-settings'), (e) => e.code === 'invalid_path')
})
