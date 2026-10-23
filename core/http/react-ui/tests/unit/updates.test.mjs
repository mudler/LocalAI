import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  classifyUpdate, updateView, upgradeBody, updateErrorKey, formatWhen, UPDATE_TYPES, UPDATE_STATES,
} from '../../src/components/addons/updateState.js'
import { updateStatus, stateChip } from '../../src/components/addons/addonState.js'
import { createAddonsApi, AddonsError } from '../../src/utils/addonsApi.js'

const json = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
const here = dirname(fileURLToPath(import.meta.url))

test('updates: a failed, unknown or unreadable check is never current', () => {
  for (const update of [undefined, null, 'x', {}, { available: false }, { state: 'made-up' }, { state: 'check-failed', error: 'refresh-failed' }, { state: 'integrity-error' }, { state: 'incompatible', reasons: ['host-version'] }]) {
    const v = updateView(update)
    assert.notEqual(v.state, 'up-to-date', JSON.stringify(update))
    assert.equal(v.canUpgrade, false, JSON.stringify(update))
  }
  assert.equal(updateView({ state: 'up-to-date' }).state, 'up-to-date')
  assert.equal(updateView({ state: 'check-failed', error: 'refresh-failed' }).tone, 'error')
  // A class the page does not know is still a failure, with the generic words.
  assert.equal(updateView({ state: 'check-failed', error: 'secret text from somewhere' }).error, 'internal')
  assert.deepEqual(updateView({ state: 'incompatible', reasons: ['host-version', 'made-up'] }).reasons, ['host-version'])
  assert.equal(updateStatus({ update: { state: 'check-failed' } }).kind, 'failed')
  assert.equal(updateStatus({ update: { state: 'integrity-error' } }).kind, 'integrity')
})

test('updates: an upgrade is offered only for a candidate the owner confirmed, with a digest to pin', () => {
  assert.equal(updateView({ state: 'update-available', available: true, version: '2.0.0', digest: 'sha256:' + 'b'.repeat(64) }).canUpgrade, true)
  assert.equal(updateView({ state: 'update-available', available: true, version: '2.0.0' }).canUpgrade, false)
  assert.equal(updateView({ state: 'update-available', available: false, digest: 'sha256:' + 'b'.repeat(64) }).canUpgrade, false)
  // An owner of an earlier release sent only the flag.
  assert.equal(updateView({ available: true, version: '2.0.0' }).state, 'update-available')
  const addon = { id: 'notes', generation: 3, digest: 'sha256:' + 'a'.repeat(64), update: { state: 'update-available', available: true, version: '1.1.0', digest: 'sha256:' + 'b'.repeat(64) } }
  assert.deepEqual(upgradeBody(addon, 'op-1'), { operationId: 'op-1', expectedDigest: addon.digest, candidateDigest: addon.update.digest, generation: 3 })
  assert.equal(upgradeBody({ ...addon, update: { state: 'up-to-date' } }, 'op-1'), null)
  assert.equal(upgradeBody({ ...addon, digest: '' }, 'op-1'), null)
})

test('updates: the owner\'s typed refusals keep their type and are final answers', () => {
  for (const type of UPDATE_TYPES) {
    const c = classifyUpdate(type === 'check_failed' ? 503 : 409, { error: { type, message: 'm' } })
    assert.equal(c.kind, 'invalid')
    assert.equal(c.type, type)
  }
  assert.equal(classifyUpdate(409, { error: { type: 'made_up' } }).kind, 'unavailable')
  assert.equal(classifyUpdate(403, { error: { type: 'addon_error' } }).kind, 'denied')
  assert.equal(classifyUpdate(503, { error: { type: 'outcome_unknown', operationId: 'op' } }).kind, 'unknown_outcome')
  assert.equal(classifyUpdate(503, { error: { type: 'outcome_unknown', operationId: 'op' } }).operationId, 'op')
  assert.equal(updateErrorKey({ kind: 'invalid', type: 'stale_installed' }), 'stale_installed')
  assert.equal(updateErrorKey({ kind: 'unknown_outcome' }), 'unknownOutcome')
  assert.equal(updateErrorKey({ kind: 'denied' }), 'denied')
  assert.equal(updateErrorKey(null), 'unavailable')
})

test('updates: the apps list marks an update beside the process state, never in place of it', () => {
  assert.equal(stateChip({ state: 'failed', update: 'update-pending' }).key, 'failed')
  assert.equal(stateChip({ state: 'running', update: 'update-pending' }).key, 'running')
})

test('updates: times are shown as the reader\'s own and a bad time as nothing', () => {
  assert.equal(formatWhen('', 'en'), '')
  assert.equal(formatWhen('not a time', 'en'), '')
  assert.match(formatWhen('2026-10-14T12:00:00Z', 'en'), /2026/)
})

test('updates: the client sends check and upgrade as the administrator\'s own action and types the refusals', async () => {
  const calls = []
  const api = createAddonsApi({
    base: p => p,
    fetch: async (url, init) => {
      calls.push({ url, init })
      if (url.endsWith('/upgrade')) {
        return init.body.includes('stale') ? json(409, { error: { type: 'stale_installed', message: 'm' } }) : json(200, { payload: { OperationID: 'op-9', Phase: 'completed', Generation: 2, AddonID: 'notes' } })
      }
      return json(200, { addonId: 'notes', state: 'up-to-date' })
    },
  })
  const status = await api.updates.status('notes')
  assert.equal(status.state, 'up-to-date')
  assert.equal(calls[0].init.method, 'GET')
  await api.updates.check('notes', { operationId: 'chk-1' })
  assert.equal(calls[1].url, '/api/addons/notes/update/check')
  assert.equal(calls[1].init.headers['X-LocalAI-Confirm'], 'chk-1')
  assert.equal(calls[1].init.credentials, 'same-origin')
  const done = await api.updates.upgrade('notes', { operationId: 'up-9', expectedDigest: 'sha256:a', candidateDigest: 'sha256:b', generation: 1 })
  assert.deepEqual(done, { operationId: 'op-9', addonId: 'notes', phase: 'completed', generation: 2, runtimeState: '' })
  assert.equal(calls[2].init.headers['X-LocalAI-Confirm'], 'up-9')
  assert.deepEqual(JSON.parse(calls[2].init.body), { operationId: 'up-9', expectedDigest: 'sha256:a', candidateDigest: 'sha256:b', generation: 1 })
  await assert.rejects(api.updates.upgrade('notes', { operationId: 'stale-1', expectedDigest: 'stale', candidateDigest: 'x' }), (e) => e instanceof AddonsError && e.kind === 'invalid' && e.type === 'stale_installed' && e.status === 409)
})

test('updates: every locale carries the same update keys as English, and words for every state, reason and refusal', () => {
  const walk = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? walk(v, `${p}${k}.`) : [`${p}${k}`]))
  const load = (lang) => JSON.parse(readFileSync(join(here, '../../public/locales', lang, 'addons.json'), 'utf8'))
  const en = walk(load('en').updates).sort()
  assert.ok(en.length > 40)
  for (const lang of readdirSync(join(here, '../../public/locales'))) {
    const d = load(lang)
    assert.deepEqual(walk(d.updates || {}).sort(), en, `${lang} update keys differ from English`)
    for (const s of UPDATE_STATES) assert.ok(d.updates.state[s], `${lang} lacks the words for state ${s}`)
    for (const r of ['platform', 'host-version', 'api-version', 'protocol-version', 'sdk-version', 'data-format', 'digest-changed']) assert.ok(d.updates.reason[r], `${lang} lacks reason ${r}`)
    for (const c of ['source-unavailable', 'refresh-failed', 'credential-unavailable', 'not-listed', 'ambiguous-release', 'canceled', 'internal']) assert.ok(d.updates.class[c], `${lang} lacks class ${c}`)
    for (const type of [...UPDATE_TYPES, 'unknownOutcome', 'auth', 'denied', 'network', 'unavailable']) assert.ok(d.updates.errors[type], `${lang} lacks the words for ${type}`)
  }
})
