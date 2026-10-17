import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  classifyRecovery, bannerKind, phaseProgress, verificationOf, recoveryErrorKey, restoreBlocked,
  restoreBody, needsMapping, purgeBody, purgeBlocked, confirmMatches, RECOVERY_TYPES,
} from '../../src/components/addons/recoveryState.js'
import { createAddonsApi, AddonsError } from '../../src/utils/addonsApi.js'

const json = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
const here = dirname(fileURLToPath(import.meta.url))

test('recovery: the owner\'s typed refusals keep their type and nothing else is invented', () => {
  assert.deepEqual(classifyRecovery(409, { error: { type: 'needs_recovery', message: 'm' } }), { kind: 'recovery', type: 'needs_recovery', status: 409, message: 'm' })
  assert.equal(classifyRecovery(404, { error: { type: 'backup_not_found' } }).type, 'backup_not_found')
  // A type the page does not know is classified like any other answer.
  assert.equal(classifyRecovery(409, { error: { type: 'made_up' } }).kind, 'unavailable')
  assert.equal(classifyRecovery(403, { error: { type: 'addon_error' } }).kind, 'denied')
  assert.equal(classifyRecovery(503, { error: { type: 'outcome_unknown', operationId: 'op' } }).kind, 'unknown_outcome')
  assert.equal(classifyRecovery(401, {}).kind, 'auth')
})

test('recovery: the banner never reads an unknown state as ready', () => {
  assert.equal(bannerKind({ state: 'ready' }), '')
  assert.equal(bannerKind(null), '')
  assert.equal(bannerKind({ state: 'needs-recovery' }), 'needs-recovery')
  assert.equal(bannerKind({ state: 'restoring' }), 'restoring')
  assert.equal(bannerKind({ state: 'something-new' }), 'needs-recovery')
})

test('recovery: upgrade progress follows the journaled phases', () => {
  assert.deepEqual(phaseProgress('upgrade:migrating'), { index: 4, total: 6, step: 'migrating' })
  assert.equal(phaseProgress('restore:swap'), null)
  assert.equal(phaseProgress(''), null)
})

test('recovery: the verification state is a word before it is a colour', () => {
  assert.deepEqual(verificationOf({ verification: 'verified' }), { key: 'verified', tone: 'ok' })
  assert.deepEqual(verificationOf({ verification: 'failed' }), { key: 'failed', tone: 'error' })
  assert.deepEqual(verificationOf({}), { key: 'unverified', tone: 'warn' })
})

test('recovery: every refusal has words, and an unknown outcome keeps its own', () => {
  assert.equal(recoveryErrorKey({ kind: 'recovery', type: 'addon_running' }), 'addon_running')
  assert.equal(recoveryErrorKey({ kind: 'unknown_outcome' }), 'unknownOutcome')
  assert.equal(recoveryErrorKey({ kind: 'denied' }), 'denied')
  assert.equal(recoveryErrorKey(null), 'unavailable')
  for (const lang of readdirSync(join(here, '../../public/locales'))) {
    const d = JSON.parse(readFileSync(join(here, '../../public/locales', lang, 'addons.json'), 'utf8'))
    for (const type of RECOVERY_TYPES) assert.ok(d.recovery.errors[type], `${lang} lacks the words for ${type}`)
    for (const key of ['unknownOutcome', 'auth', 'denied', 'network', 'unavailable']) assert.ok(d.recovery.errors[key], `${lang} lacks ${key}`)
  }
})

test('recovery: every locale carries the same recovery keys as English', () => {
  const walk = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? walk(v, `${p}${k}.`) : [`${p}${k}`]))
  const en = walk(JSON.parse(readFileSync(join(here, '../../public/locales/en/addons.json'), 'utf8')).recovery).sort()
  assert.ok(en.length > 60)
  for (const lang of readdirSync(join(here, '../../public/locales'))) {
    const got = walk(JSON.parse(readFileSync(join(here, '../../public/locales', lang, 'addons.json'), 'utf8')).recovery).sort()
    assert.deepEqual(got, en, lang)
  }
})

test('recovery: a restore needs a stopped add-on, the typed ID and, from another mode, an account', () => {
  assert.equal(restoreBlocked({ state: 'running' }, { }), 'running')
  assert.equal(restoreBlocked({ state: 'stopped' }, {}), '')
  assert.equal(restoreBlocked(null, null), 'loading')
  assert.equal(confirmMatches('tables', 'tables'), true)
  assert.equal(confirmMatches('Tables', 'tables'), false)
  assert.equal(confirmMatches('', 'tables'), false)
  const backup = { id: 'b1', mode: 'local-owner' }
  assert.equal(needsMapping(backup, { mode: 'named-user' }), true)
  assert.equal(needsMapping(backup, { mode: 'local-owner' }), false)
  assert.deepEqual(restoreBody({ operationId: 'op', backup, addonId: 'tables', mapping: ' alice ' }), { operationId: 'op', backupId: 'b1', confirm: 'tables', mapping: { 'local-owner': 'alice' } })
  assert.deepEqual(restoreBody({ operationId: 'op', backup: { id: 'b2', mode: 'named-user' }, addonId: 'tables', mapping: 'x' }), { operationId: 'op', backupId: 'b2', confirm: 'tables' })
})

test('recovery: a purge needs a scope and an add-on that is not running', () => {
  assert.equal(purgeBlocked({ enabled: false, state: 'stopped' }, { data: false, backups: false, recoveryCopies: false }), 'scope')
  assert.equal(purgeBlocked({ enabled: true, state: 'stopped' }, { data: true }), 'running')
  assert.equal(purgeBlocked({ enabled: false, state: 'stopped' }, { data: true }), '')
  assert.deepEqual(purgeBody({ operationId: 'op', addonId: 'tables', scope: { data: true } }), { operationId: 'op', confirm: 'tables', scope: { data: true, backups: false, recoveryCopies: false } })
})

test('recovery: the client sends the operation as the confirmation header and types the refusals', async () => {
  const calls = []
  const api = createAddonsApi({
    fetch: async (url, init) => {
      calls.push({ url, init })
      if (url.endsWith('/purge')) return json(409, { error: { type: 'addon_running', message: 'stop it' } })
      return json(200, { id: 'b1' })
    },
  })
  const out = await api.recovery.backup('tables', { operationId: 'op-1' })
  assert.equal(out.id, 'b1')
  assert.equal(calls[0].url, '/api/addons/tables/recovery/backup')
  assert.equal(calls[0].init.method, 'POST')
  assert.equal(calls[0].init.headers['X-LocalAI-Confirm'], 'op-1')
  assert.deepEqual(JSON.parse(calls[0].init.body), { operationId: 'op-1' })
  await assert.rejects(api.recovery.purge('tables', { operationId: 'op-2', confirm: 'tables', scope: { data: true } }), (e) => {
    assert.ok(e instanceof AddonsError)
    assert.equal(e.kind, 'recovery')
    assert.equal(e.type, 'addon_running')
    return true
  })
  assert.equal(calls[1].init.headers['X-LocalAI-Confirm'], 'op-2')
  // The status read is a plain GET with no confirmation.
  await api.recovery.status('tables')
  assert.equal(calls[2].init.method, 'GET')
  assert.equal(calls[2].init.headers['X-LocalAI-Confirm'], undefined)
})
