import assert from 'node:assert/strict'
import test from 'node:test'

import {
  FIELDS, FIELD_BY_KEY, GROUPS, getValue, setValue, isChanged, hasDefault, parseGoDuration,
  compactDuration, vramBudgetError, searchFields, normalizeLoaded, valuesEqual,
} from './settingsSchema.js'
import {
  pendingChanges, visibleChanges, runChecks, hasBlockingCheck, payloadFor, displayValue,
  historyEntries, linesOf,
} from './settingsPending.js'

const base = () => ({
  watchdog_enabled: false, watchdog_idle_enabled: false, watchdog_busy_enabled: false,
  watchdog_idle_timeout: '15m0s', max_active_backends: 0, csrf: false, galleries: [{ url: 'https://x', name: 'x' }],
  api_keys: ['k1', 'k2'], memory_reclaimer_threshold: 0.95, p2p_token: '', debug: false,
})

test('every field belongs to a known group and has a unique key', () => {
  const ids = new Set(GROUPS.map(g => g.id))
  assert.equal(new Set(FIELDS.map(f => f.key)).size, FIELDS.length)
  for (const f of FIELDS) assert.ok(ids.has(f.group), f.key)
})

test('parseGoDuration reads what Go reads', () => {
  assert.equal(parseGoDuration('15m'), 900000)
  assert.equal(parseGoDuration('15m0s'), 900000)
  assert.equal(parseGoDuration('1h30m'), 5400000)
  assert.equal(parseGoDuration('500ms'), 500)
  assert.equal(parseGoDuration('1.5s'), 1500)
  assert.equal(parseGoDuration('0'), 0)
  assert.equal(parseGoDuration('15'), null)
  assert.equal(parseGoDuration('soon'), null)
  assert.equal(parseGoDuration(''), null)
})

test('compactDuration shortens the Go form', () => {
  assert.equal(compactDuration('15m0s'), '15m')
  assert.equal(compactDuration('1h0m0s'), '1h')
  assert.equal(compactDuration('1m30s'), '1m30s')
  assert.equal(compactDuration('500ms'), '500ms')
  assert.equal(compactDuration('1.5s'), '1.5s')
  assert.equal(compactDuration('0'), '0')
  assert.equal(compactDuration('nonsense'), 'nonsense')
})

test('vramBudgetError mirrors the server parser', () => {
  for (const ok of ['', '80%', '0.8', '12GB', '12GiB', '12000MB', '1073741824']) assert.equal(vramBudgetError(ok), '', ok)
  for (const bad of ['150%', 'abc', '-1GB', '%', '1.5e']) assert.notEqual(vramBudgetError(bad), '', bad)
})

test('defaults exist only where the code states them', () => {
  assert.equal(hasDefault(FIELD_BY_KEY.threads), false)
  assert.equal(hasDefault(FIELD_BY_KEY.context_size), false)
  assert.equal(hasDefault(FIELD_BY_KEY.lru_eviction_max_retries), true)
  assert.equal(FIELD_BY_KEY.watchdog_idle_timeout.default, '15m')
})

test('changed from default compares durations by value and ignores fields without a default', () => {
  const s = normalizeLoaded(base())
  assert.equal(s.watchdog_idle_timeout, '15m')
  assert.equal(isChanged(FIELD_BY_KEY.watchdog_idle_timeout, s), false)
  assert.equal(isChanged(FIELD_BY_KEY.watchdog_idle_timeout, { ...s, watchdog_idle_timeout: '1h' }), true)
  assert.equal(isChanged(FIELD_BY_KEY.threads, { ...s, threads: 99 }), false)
  assert.equal(isChanged(FIELD_BY_KEY.memory_reclaimer_threshold, { ...s, memory_reclaimer_threshold: 0.9 }), true)
})

test('the CSRF switch shows the inverse of the wire field', () => {
  const f = FIELD_BY_KEY.csrf
  assert.equal(getValue(f, { csrf: false }), true)
  assert.deepEqual(setValue(f, { csrf: false }, false), { csrf: true })
})

test('the master watchdog switch writes both checks', () => {
  const f = FIELD_BY_KEY.watchdog_enabled
  const next = setValue(f, base(), true)
  assert.equal(next.watchdog_idle_enabled, true)
  assert.equal(next.watchdog_busy_enabled, true)
  assert.equal(next.watchdog_enabled, true)
})

test('pending changes list only what differs and hide the master switch', () => {
  const initial = normalizeLoaded(base())
  const draft = setValue(FIELD_BY_KEY.watchdog_enabled, initial, true)
  const changes = pendingChanges(initial, draft)
  assert.deepEqual(visibleChanges(changes).map(c => c.field.key), ['watchdog_idle_enabled', 'watchdog_busy_enabled'])
  assert.equal(pendingChanges(initial, structuredClone(initial)).length, 0)
})

test('the request body carries only the changed keys, in wire form', () => {
  const initial = normalizeLoaded(base())
  let draft = setValue(FIELD_BY_KEY.csrf, initial, false)
  draft = setValue(FIELD_BY_KEY.api_keys, draft, 'k1, k3\nk4')
  draft = setValue(FIELD_BY_KEY.galleries, draft, '[{"url":"https://y","name":"y"}]')
  draft = setValue(FIELD_BY_KEY.max_active_backends, draft, 3)
  const body = payloadFor(pendingChanges(initial, draft), draft)
  assert.deepEqual(body, {
    max_active_backends: 3,
    galleries: [{ url: 'https://y', name: 'y' }],
    csrf: true,
    api_keys: ['k1', 'k3', 'k4'],
  })
})

test('reformatting JSON is not a change', () => {
  const initial = normalizeLoaded(base())
  const draft = setValue(FIELD_BY_KEY.galleries, initial, JSON.stringify(initial.galleries))
  assert.equal(pendingChanges(initial, draft).length, 0)
})

test('checks: durations, budgets and JSON can block; restart and cautions warn', () => {
  const initial = normalizeLoaded(base())
  let draft = setValue(FIELD_BY_KEY.watchdog_idle_timeout, initial, 'soon')
  draft = setValue(FIELD_BY_KEY.vram_budget, draft, '150%')
  draft = setValue(FIELD_BY_KEY.galleries, draft, '{not json')
  draft = setValue(FIELD_BY_KEY.agent_pool_enable_logs, draft, true)
  draft = setValue(FIELD_BY_KEY.force_eviction_when_busy, draft, true)
  const checks = runChecks(pendingChanges(initial, draft), initial, draft)
  assert.ok(hasBlockingCheck(checks))
  assert.equal(checks.filter(c => c.level === 'error').length, 3)
  assert.ok(checks.some(c => c.level === 'warn' && /Restart LocalAI for Agent logs/.test(c.text)))
  assert.ok(checks.some(c => c.level === 'warn' && /interrupt requests/.test(c.text)))

  const good = setValue(FIELD_BY_KEY.watchdog_idle_timeout, initial, '30m')
  const okChecks = runChecks(pendingChanges(initial, good), initial, good)
  assert.equal(hasBlockingCheck(okChecks), false)
  assert.ok(okChecks.some(c => c.level === 'ok' && /30m is a valid duration/.test(c.text)))
})

test('secrets are shown as set or empty, never as a value', () => {
  assert.equal(displayValue(FIELD_BY_KEY.p2p_token, 'abc123'), 'set')
  assert.equal(displayValue(FIELD_BY_KEY.p2p_token, '0'), 'new token')
  assert.equal(displayValue(FIELD_BY_KEY.p2p_token, ''), 'empty')
  const initial = normalizeLoaded(base())
  const draft = setValue(FIELD_BY_KEY.p2p_token, initial, 'secret-token')
  const [entry] = historyEntries(pendingChanges(initial, draft), 1)
  assert.equal(entry.to, null)
  assert.equal(entry.from, null)
})

test('search finds a setting by name, key, description and its old section', () => {
  const s = normalizeLoaded(base())
  assert.ok(searchFields('cors', s).some(f => f.key === 'cors_allow_origins'))
  assert.ok(searchFields('idle timeout', s).some(f => f.key === 'watchdog_idle_timeout'))
  assert.ok(searchFields('Watchdog', s).length >= 6, 'the old section name still finds its settings')
  assert.ok(searchFields('Memory Reclaimer', s).some(f => f.key === 'memory_reclaimer_threshold'))
  assert.ok(searchFields('watchdog_busy_timeout', s).length === 1)
  assert.equal(searchFields('zzzz-nothing', s).length, 0)
})

test('linesOf splits on newlines and commas', () => {
  assert.deepEqual(linesOf(' a,b \n\n c '), ['a', 'b', 'c'])
})

test('valuesEqual treats an empty number like zero', () => {
  assert.equal(valuesEqual(FIELD_BY_KEY.threads, '', 0), true)
})
