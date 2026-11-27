// SPDX-License-Identifier: MIT
// The Settings page in distributed mode: a save of a memory setting names
// the revision of the cluster memory settings it was read at, and a 409 with
// a memory revision is a conflict the page reloads on.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { FIELD_BY_KEY, setValue, normalizeLoaded } from '../../src/utils/settingsSchema.js'
import { pendingChanges, payloadFor, isMemoryConflict, withMemoryRevision } from '../../src/utils/settingsPending.js'

const base = () => ({
  watchdog_enabled: false, watchdog_idle_enabled: false, watchdog_busy_enabled: false,
  watchdog_idle_timeout: '15m0s', max_active_backends: 0, csrf: false, galleries: [{ url: 'https://x', name: 'x' }],
  api_keys: ['k1', 'k2'], memory_reclaimer_threshold: 0.95, debug: false,
})

test('a save of a cluster memory setting names the revision it was read at', () => {
  const initial = normalizeLoaded({ ...base(), memory_scope: 'cluster', memory_revision: 7 })
  const draft = setValue(FIELD_BY_KEY.max_active_backends, initial, 3)
  const body = payloadFor(pendingChanges(initial, draft), draft)
  assert.deepEqual(body, { max_active_backends: 3, memory_revision: 7 })
})

test('a save without a memory setting, or on a single server, names no revision', () => {
  const cluster = normalizeLoaded({ ...base(), memory_scope: 'cluster', memory_revision: 7 })
  const other = setValue(FIELD_BY_KEY.debug, cluster, true)
  assert.deepEqual(payloadFor(pendingChanges(cluster, other), other), { debug: true })
  const single = normalizeLoaded(base())
  const draft = setValue(FIELD_BY_KEY.max_active_backends, single, 3)
  assert.deepEqual(payloadFor(pendingChanges(single, draft), draft), { max_active_backends: 3 })
})

test('a 409 that carries a memory revision is a memory settings conflict', () => {
  const conflict = Object.assign(new Error('changed'), { status: 409, body: { success: false, memory_revision: 8 } })
  assert.equal(isMemoryConflict(conflict), true)
  const other = Object.assign(new Error('owner'), { status: 409, body: { success: false, error: 'x is set on the add-on owner' } })
  assert.equal(isMemoryConflict(other), false)
  assert.equal(isMemoryConflict(Object.assign(new Error('bad'), { status: 400, body: {} })), false)
  assert.equal(isMemoryConflict(null), false)
})

test('a save reply moves the memory revision of the page forward', () => {
  const values = { max_active_backends: 3, memory_revision: 7 }
  assert.deepEqual(withMemoryRevision(values, { success: true, memory_revision: 8 }), { max_active_backends: 3, memory_revision: 8 })
  assert.equal(withMemoryRevision(values, { success: true }), values)
})

test('the conflict message is translated in every locale', () => {
  const english = JSON.parse(readFileSync(new URL('../../public/locales/en/admin.json', import.meta.url), 'utf8'))
  for (const locale of ['bn', 'de', 'es', 'fr', 'id', 'it', 'ko', 'pt-BR', 'zh-CN']) {
    const strings = JSON.parse(readFileSync(new URL(`../../public/locales/${locale}/admin.json`, import.meta.url), 'utf8'))
    assert.ok(strings.settings.memoryConflict, locale)
    assert.notEqual(strings.settings.memoryConflict, english.settings.memoryConflict, locale)
  }
})
