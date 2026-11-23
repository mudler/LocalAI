import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import i18next from 'i18next'
import {
  attentionCount, countsOf, defaultSelection, firstAttention, initialRunState, isRunning, requestItemOf, retryableKeys,
  reviewGroups, runKeys, runProgress, startItems, upgradeRunReducer,
} from '../../src/utils/upgradeRun.js'
import { upgradeFailureWords } from '../../src/components/addons/updateReview.js'

const item = (key, phase, extra = {}) => {
  const [kind, name] = key.split(':')
  return { key, kind, name, from: 'v1', to: 'v2', phase, progress: 0, message: '', queued_reason: '', error_key: '', error: '', retryable: false, cancellable: false, nodes: [], ...extra }
}
const runOf = (items, extra = {}) => ({ id: 'r1', source: 'manual', state: 'running', created_at: '2026-11-23T10:00:00Z', items, ...extra })

const preview = {
  items: [
    { key: 'gallery:kokoro', kind: 'gallery', name: 'kokoro', from: 'v0.3.1', to: 'v0.4.0', startable: true, default_selected: true, risk: [] },
    { key: 'addon:notes', kind: 'addon', name: 'notes', from: 'v0.8.1', to: 'v0.8.2', startable: true, default_selected: true, risk: [{ code: 'migrates_data' }] },
    { key: 'model:qwen3-8b', kind: 'model', name: 'qwen3-8b', from: 'sha256:1a', to: 'sha256:9f', startable: true, default_selected: false, risk: [] },
    { key: 'library:official/piper', kind: 'library', name: 'official/piper', from: 'v1', to: 'v2', startable: false, reason: 'not signed', default_selected: true, risk: [] },
  ],
  // The plan runs backends, then models, then add-ons.
  order: ['gallery:kokoro', 'library:official/piper', 'model:qwen3-8b', 'addon:notes'],
}

const reduce = (actions, state = initialRunState) => actions.reduce(upgradeRunReducer, state)

test('upgrade run: a row is named to the server by its kind and the part of its id after the kind', () => {
  assert.deepEqual(requestItemOf({ id: 'gallery:kokoro', kind: 'gallery', name: 'kokoro' }), { kind: 'gallery', name: 'kokoro' })
  assert.deepEqual(requestItemOf({ id: 'library:official/piper', kind: 'library', name: 'piper' }), { kind: 'library', name: 'official/piper' })
  assert.deepEqual(requestItemOf({ id: 'addon:notes', kind: 'addon', name: 'Notes sync' }), { kind: 'addon', name: 'notes' })
  assert.deepEqual(requestItemOf({ id: 'model:qwen3-8b', kind: 'model', name: 'qwen3-8b' }), { kind: 'model', name: 'qwen3-8b' })
})

test('upgrade run: the review selects what the server selects by default, never a model, never what cannot start', () => {
  const s = reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview }])
  assert.equal(s.review.status, 'ready')
  assert.deepEqual(s.review.selected, ['gallery:kokoro', 'addon:notes'])
  assert.deepEqual(defaultSelection(preview.items), ['gallery:kokoro', 'addon:notes'])
  assert.deepEqual(reviewGroups(preview.items).map(g => [g.id, g.items.length]), [['backends', 2], ['addons', 1], ['models', 1]])
})

test('upgrade run: select all, select none and a toggle; an item that cannot start is never selected', () => {
  let s = reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview }])
  s = upgradeRunReducer(s, { type: 'selectAll' })
  assert.deepEqual(s.review.selected, ['gallery:kokoro', 'addon:notes', 'model:qwen3-8b'])
  s = upgradeRunReducer(s, { type: 'selectNone' })
  assert.deepEqual(s.review.selected, [])
  s = upgradeRunReducer(s, { type: 'toggle', key: 'library:official/piper' })
  assert.deepEqual(s.review.selected, [], 'blocked')
  s = upgradeRunReducer(s, { type: 'toggle', key: 'model:qwen3-8b' })
  s = upgradeRunReducer(s, { type: 'toggle', key: 'gallery:kokoro' })
  assert.deepEqual(s.review.selected, ['model:qwen3-8b', 'gallery:kokoro'])
  s = upgradeRunReducer(s, { type: 'toggle', key: 'model:qwen3-8b' })
  assert.deepEqual(s.review.selected, ['gallery:kokoro'])
})

test('upgrade run: the start body follows the order of the plan and pins the version the review showed', () => {
  let s = reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview }, { type: 'selectAll' }])
  assert.deepEqual(startItems(s.review), [
    { kind: 'gallery', name: 'kokoro', expected_to: 'v0.4.0' },
    { kind: 'model', name: 'qwen3-8b', expected_to: 'sha256:9f' },
    { kind: 'addon', name: 'notes', expected_to: 'v0.8.2' },
  ])
  s = upgradeRunReducer(s, { type: 'selectNone' })
  assert.deepEqual(startItems(s.review), [])
  assert.deepEqual(startItems(null), [])
})

test('upgrade run: a review of one row selects that row, a model too', () => {
  const one = { items: [preview.items[2]], order: ['model:qwen3-8b'] }
  const s = reduce([{ type: 'reviewOpen', single: true }, { type: 'reviewReady', preview: one }])
  assert.equal(s.review.single, true)
  assert.deepEqual(s.review.selected, ['model:qwen3-8b'])
  const blocked = reduce([{ type: 'reviewOpen', single: true }, { type: 'reviewReady', preview: { items: [preview.items[3]] } }])
  assert.deepEqual(blocked.review.selected, [])
})

test('upgrade run: a refused start keeps the review open with the code; a change of selection clears it', () => {
  let s = reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview }, { type: 'busy', busy: 'start' }, { type: 'startFailed', code: 'stale', error: 'stale' }])
  assert.equal(s.busy, '')
  assert.equal(s.review.code, 'stale')
  s = upgradeRunReducer(s, { type: 'toggle', key: 'gallery:kokoro' })
  assert.equal(s.review.code, '')
  const started = upgradeRunReducer(s, { type: 'started', run: runOf([]) })
  assert.equal(started.review, null)
  assert.equal(started.run.id, 'r1')
  const failed = reduce([{ type: 'reviewOpen' }, { type: 'reviewFailed', error: 'HTTP 500' }])
  assert.equal(failed.review.status, 'error')
})

test('upgrade run: a poll answers only for the run it asked about, and a dismissed run never comes back', () => {
  let s = reduce([{ type: 'run', run: runOf([item('gallery:kokoro', 'running')]) }])
  assert.equal(s.loaded, true)
  const other = { ...runOf([]), id: 'r2' }
  assert.equal(upgradeRunReducer(s, { type: 'run', run: other, from: 'poll' }).run.id, 'r1', 'a late poll of another run is dropped')
  s = upgradeRunReducer(s, { type: 'run', run: runOf([item('gallery:kokoro', 'updated')], { state: 'finished' }), from: 'poll' })
  assert.equal(s.run.state, 'finished')
  s = upgradeRunReducer(s, { type: 'dismissed' })
  assert.equal(s.run, null)
  assert.deepEqual(s.dismissed, ['r1'])
  assert.equal(upgradeRunReducer(s, { type: 'run', run: runOf([]) }).run, null, 'the active list may still name it for a moment')
  assert.equal(upgradeRunReducer(s, { type: 'run', run: other }).run.id, 'r2', 'a new run shows')
  assert.equal(upgradeRunReducer(s, { type: 'run', run: other, from: 'poll' }).run, null, 'a poll never brings a run of its own')
})

test('upgrade run: an action that fails says why and frees the buttons', () => {
  const s = reduce([{ type: 'run', run: runOf([]) }, { type: 'busy', busy: 'cancel' }, { type: 'error', error: 'HTTP 409' }])
  assert.equal(s.busy, '')
  assert.equal(s.error, 'HTTP 409')
  assert.equal(upgradeRunReducer(s, { type: 'busy', busy: 'retry' }).error, '', 'a new action clears the old words')
  assert.equal(reduce([{ type: 'loadFailed', error: 'down' }]).loaded, true)
})

test('upgrade run: counts, progress and the items to retry', () => {
  const run = runOf([
    item('gallery:piper', 'updated'),
    item('gallery:kokoro', 'running', { progress: 35 }),
    item('gallery:llama-cpp', 'running', { progress: 64 }),
    item('addon:notes', 'rolled_back', { retryable: true }),
    item('addon:tables', 'failed', { retryable: false }),
    item('model:qwen3-8b', 'queued'),
  ])
  const counts = countsOf(run)
  assert.equal(counts.total, 6)
  assert.equal(counts.updated, 1)
  assert.equal(counts.running, 2)
  assert.equal(attentionCount(counts), 2)
  assert.equal(runProgress(run), Math.round((100 + 35 + 64 + 100 + 100 + 0) / 6))
  assert.deepEqual(retryableKeys(run), ['addon:notes'])
  assert.equal(firstAttention(run).key, 'addon:notes')
  assert.deepEqual([...runKeys(run)].length, 6)
  assert.equal(isRunning(run), true)
  // The server's counts win over the items.
  assert.equal(countsOf({ ...run, counts: { total: 9, updated: 4 } }).updated, 4)
  assert.equal(runProgress(runOf([], { state: 'finished' })), 100)
  assert.equal(countsOf(null).total, 0)
})

// ---- One failure-words map for every kind ---------------------------------------

const LOCALES = readdirSync(new URL('../../public/locales', import.meta.url)).sort()
async function addonsT(lng) {
  const resources = {}
  for (const l of ['en', lng]) resources[l] = { addons: JSON.parse(readFileSync(new URL(`../../public/locales/${l}/addons.json`, import.meta.url), 'utf8')) }
  const i = i18next.createInstance()
  await i.init({ lng, fallbackLng: 'en', resources, defaultNS: 'addons', ns: ['addons'], interpolation: { escapeValue: false }, initAsync: false })
  return i.t.bind(i)
}

test('failure words: backend steps, run refusals and add-on keys share one map, in every language', async () => {
  const en = await addonsT('en')
  assert.equal(upgradeFailureWords('download', en), 'The new version could not be downloaded')
  assert.equal(upgradeFailureWords('start', en), 'The new version did not start')
  assert.match(upgradeFailureWords('stale', en), /newer version came out/)
  assert.match(upgradeFailureWords('upgrade_failed', en), /installed version is kept/, 'the add-on keys are unchanged')
  // A key this build does not know is said with the server's words, else plainly.
  assert.equal(upgradeFailureWords('future_key', en, 'disk full'), 'disk full')
  assert.equal(upgradeFailureWords('future_key', en), 'The update failed')
  // Without a key the add-on store's answer is unchanged.
  assert.match(upgradeFailureWords(undefined, en), /add-on service is not available/)
  for (const l of LOCALES) {
    const t = await addonsT(l)
    for (const key of ['download', 'verify', 'swap', 'start', 'not_installed', 'no_update', 'stale', 'held', 'unknown', 'unconfirmed']) {
      const words = upgradeFailureWords(key, t)
      assert.ok(words && !words.startsWith('updates.'), `${l} ${key}: ${words}`)
    }
  }
})
