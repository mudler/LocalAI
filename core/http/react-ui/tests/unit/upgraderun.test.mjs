import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import i18next from 'i18next'
import {
  CONFIRM_HEADERS, attentionOf, busyCount, endedNow, confirmedPost, isBusy, isEnded, needsAttention, countsAsAttention, countsOf, defaultSelection, runsUnavailable, initialRunState, isRunning, requestItemOf, retryableKeys,
  nodePhase, reviewGroups, riskText, runKeys, runProgress, startItems, upgradeRunReducer,
} from '../../src/utils/upgradeRun.js'
import { upgradeFailureWords } from '../../src/components/addons/updateReview.js'

const item = (key, phase, extra = {}) => {
  const [kind, name] = key.split(':')
  return { key, kind, name, from: 'v1', to: 'v2', phase, progress: 0, message: '', queued_reason: '', error_key: '', error: '', retryable: false, cancellable: false, nodes: [], ...extra }
}
const runOf = (items, extra = {}) => ({ id: 'r1', source: 'manual', state: 'running', created_at: '2026-11-23T10:00:00Z', items, ...extra })

const preview = {
  items: [
    { key: 'gallery:kokoro', kind: 'gallery', name: 'kokoro', from: 'v0.3.1', to: 'v0.4.0', expected_to: 'v0.4.0', startable: true, default_selected: true, risk: [] },
    { key: 'addon:notes', kind: 'addon', name: 'notes', title: 'Notes sync', from: '0.8.1', to: '0.8.2', expected_to: 'sha256:bb22', startable: true, default_selected: true, risk: [{ code: 'migrates_data', params: {} }] },
    { key: 'model:qwen3-8b', kind: 'model', name: 'qwen3-8b', from: 'sha256:1a', to: 'sha256:9f', expected_to: 'sha256:9f', startable: true, default_selected: false, risk: [] },
    { key: 'library:official/piper', kind: 'library', name: 'official/piper', from: 'v1', to: 'v2', expected_to: 'v2', startable: false, reason: 'not signed', default_selected: true, risk: [] },
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

test('upgrade run: the start body follows the order of the plan and sends back the pin of each item (an add-on\'s digest, not its version)', () => {
  let s = reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview }, { type: 'selectAll' }])
  assert.deepEqual(startItems(s.review), [
    { kind: 'gallery', name: 'kokoro', expected_to: 'v0.4.0' },
    { kind: 'model', name: 'qwen3-8b', expected_to: 'sha256:9f' },
    { kind: 'addon', name: 'notes', expected_to: 'sha256:bb22' },
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

test('upgrade run: an answer older than the last one shown is dropped, so a cancel never shows running again', () => {
  // A poll asked (1), then the cancel (2); the cancel answers first.
  let s = reduce([{ type: 'run', run: runOf([item('gallery:kokoro', 'running')]), seq: 0 }])
  s = upgradeRunReducer(s, { type: 'run', run: runOf([item('gallery:kokoro', 'cancelled')], { state: 'cancelled' }), seq: 2 })
  assert.equal(s.run.state, 'cancelled')
  const late = upgradeRunReducer(s, { type: 'run', run: runOf([item('gallery:kokoro', 'running')]), from: 'poll', seq: 1 })
  assert.equal(late.run.state, 'cancelled', 'the late poll is dropped')
  assert.equal(upgradeRunReducer(s, { type: 'run', run: runOf([], { state: 'cancelled' }), from: 'poll', seq: 3 }).run.items.length, 0, 'a newer poll shows')
})

test('upgrade run: the end of a run is told once, whatever answers come after it', () => {
  const finished = runOf([], { state: 'finished' })
  let fired = ''
  const step = (was, run) => {
    const out = endedNow(was, run, fired)
    if (out) fired = run.id
    return out
  }
  assert.equal(step(false, runOf([])), false, 'a run that runs did not end')
  assert.equal(step(true, finished), true)
  assert.equal(step(true, finished), false, 'the same end is not told again')
  assert.equal(step(true, { ...finished, id: 'r2' }), true, 'the end of another run is told')
  assert.equal(step(false, finished), false, 'a run that was not watched running is not told')
})

test('upgrade run: an action that fails says why, with the code of the refusal, and frees the buttons', () => {
  const s = reduce([{ type: 'run', run: runOf([]) }, { type: 'busy', busy: 'cancel' }, { type: 'error', error: 'cannot cancel', code: 'not_cancellable' }])
  assert.equal(s.busy, '')
  assert.equal(s.error, 'cannot cancel')
  assert.equal(s.errorCode, 'not_cancellable')
  const next = upgradeRunReducer(s, { type: 'busy', busy: 'retry' })
  assert.equal(next.error, '', 'a new action clears the old words')
  assert.equal(next.errorCode, '')
  assert.equal(upgradeRunReducer(next, { type: 'busy', busy: '' }).busy, '')
  assert.equal(reduce([{ type: 'loadFailed', error: 'down' }]).loaded, true)
})

test('upgrade run: a retry answers a new run, and the page follows it; the old one is not polled any more', () => {
  let s = reduce([{ type: 'run', run: runOf([item('addon:notes', 'failed', { retryable: true })], { state: 'finished' }) }])
  s = upgradeRunReducer(s, { type: 'run', run: { ...runOf([item('addon:notes', 'queued')]), id: 'r2' } })
  assert.equal(s.run.id, 'r2')
  assert.equal(upgradeRunReducer(s, { type: 'run', run: runOf([], { state: 'finished' }), from: 'poll' }).run.id, 'r2', 'a late poll of the old run is dropped')
})

test('upgrade run: a start and a retry carry the confirmation of the browser', () => {
  assert.deepEqual(CONFIRM_HEADERS, { 'X-LocalAI-Confirm': 'upgrade-run' })
  const start = confirmedPost({ items: [{ kind: 'addon', name: 'notes', expected_to: 'v2' }], source: 'manual' })
  assert.equal(start.method, 'POST')
  assert.equal(start.headers['X-LocalAI-Confirm'], 'upgrade-run')
  assert.equal(start.headers['Content-Type'], 'application/json')
  assert.deepEqual(JSON.parse(start.body), { items: [{ kind: 'addon', name: 'notes', expected_to: 'v2' }], source: 'manual' })
  assert.equal(confirmedPost({}).headers['X-LocalAI-Confirm'], 'upgrade-run')
})

test('upgrade run: a server that keeps no runs is said once and blocks the start', () => {
  assert.equal(runsUnavailable({ status: 503 }), true)
  assert.equal(runsUnavailable({ status: 409, body: { code: 'unavailable' } }), true)
  assert.equal(runsUnavailable({ status: 409, body: { code: 'run_active' } }), false)
  assert.equal(reduce([{ type: 'loadFailed', unavailable: true }]).unavailable, true)
  assert.equal(reduce([{ type: 'reviewOpen' }, { type: 'reviewFailed', code: 'unavailable' }]).unavailable, true)
  assert.equal(reduce([{ type: 'loadFailed', unavailable: true }, { type: 'run', run: runOf([]) }]).unavailable, false)
})

test('upgrade run: a preview that names an active run keeps it for the review', () => {
  const s = reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview: { ...preview, active_run_id: 'r9' } }])
  assert.equal(s.review.activeRun, 'r9')
  assert.equal(reduce([{ type: 'reviewOpen' }, { type: 'reviewReady', preview }]).review.activeRun, '')
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
  assert.equal(attentionOf(run), 2)
  assert.equal(runProgress(run), Math.round((100 + 35 + 64 + 100 + 100 + 0) / 6))
  assert.deepEqual(retryableKeys(run), ['addon:notes'])
  assert.deepEqual([...runKeys(run)].length, 6)
  assert.equal(isRunning(run), true)
  // The server's counts win over the items.
  assert.equal(countsOf({ ...run, counts: { total: 9, updated: 4 } }).updated, 4)
  assert.equal(runProgress(runOf([], { state: 'finished' })), 100)
  assert.equal(countsOf(null).total, 0)
})

test('upgrade run: an item the server still checks is busy, not ended, while one no check could answer is settled', () => {
  const checking = item('gallery:kokoro', 'unconfirmed', { verifying: true, progress: 100 })
  const settled = item('gallery:piper', 'unconfirmed', { retryable: true })
  const verifyingRun = item('gallery:whisper', 'running', { verifying: true, progress: 100 })
  assert.equal(isBusy(checking), true)
  assert.equal(isEnded(checking), false)
  assert.equal(needsAttention(checking), false)
  assert.equal(isBusy(verifyingRun), true)
  assert.equal(isBusy(settled), false)
  assert.equal(isEnded(settled), true)
  assert.equal(needsAttention(settled), true)
  const run = runOf([checking, settled, verifyingRun, item('model:m', 'queued')])
  // Two items are busy (the strip says two run), one needs a look.
  assert.equal(busyCount(run), 2)
  assert.equal(attentionOf(run), 1)
  assert.deepEqual(retryableKeys(run), ['gallery:piper'])
  assert.equal(runProgress(run), Math.round((100 + 100 + 100 + 0) / 4))
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
  assert.match(upgradeFailureWords('run_active', en), /update run is in progress/)
  assert.match(upgradeFailureWords('source_unchecked', en), /check is unavailable\. Check again/)
  assert.match(upgradeFailureWords('upgrade_failed', en), /installed version is kept/, 'the add-on keys are unchanged')
  // In a run, the run's own words win for keys the add-on pages word otherwise.
  assert.equal(upgradeFailureWords('not_human_surface', en, '', 'run'), 'This update needs you to confirm it in the browser.')
  assert.equal(upgradeFailureWords('unavailable', en, '', 'run'), 'Update runs are not available on this server.')
  assert.match(upgradeFailureWords('not_human_surface', en), /administrator must make this change/)
  assert.equal(upgradeFailureWords('forbidden', en, '', 'run'), 'Your account cannot update add-ons.')
  // The reasons a review item cannot start (the planner's reasons).
  assert.equal(upgradeFailureWords('in_run', en, '', 'run'), 'It is already in the update run in progress')
  assert.equal(upgradeFailureWords('installs_disabled', en, '', 'run'), 'Installing backends is turned off on this server')
  assert.equal(upgradeFailureWords('edited_keys', en, '', 'run'), 'You edited settings of this model. Use Upgrade on its row to choose what to keep')
  // A key this build does not know is said with the server's words, else plainly.
  assert.equal(upgradeFailureWords('future_key', en, 'disk full'), 'disk full')
  assert.equal(upgradeFailureWords('future_key', en), 'The update failed')
  // Without a key the add-on store's answer is unchanged.
  assert.match(upgradeFailureWords(undefined, en), /add-on service is not available/)
  for (const l of LOCALES) {
    const t = await addonsT(l)
    for (const key of ['download', 'verify', 'swap', 'start', 'not_installed', 'no_update', 'stale', 'held', 'unknown', 'unconfirmed', 'edited_keys',
      'run_active', 'source_unchecked', 'run_not_finished', 'nothing_to_retry', 'not_cancellable', 'run_finished', 'invalid', 'not_found',
      'forbidden', 'not_human_surface', 'unavailable', 'in_run', 'installs_disabled']) {
      const words = upgradeFailureWords(key, t, '', 'run')
      assert.ok(words && !words.startsWith('updates.'), `${l} ${key}: ${words}`)
    }
  }
})

test('upgrade run: the status of a node reads as a phase of the run, with words in every locale', () => {
  assert.equal(nodePhase({ phase: 'success' }), 'updated')
  assert.equal(nodePhase({ phase: 'error' }), 'failed')
  assert.equal(nodePhase({ phase: 'downloading' }), 'running')
  assert.equal(nodePhase({ phase: 'running_on_worker' }), 'running')
  assert.equal(nodePhase({ phase: 'rolled_back' }), 'rolled_back')
  assert.equal(nodePhase({ phase: 'unconfirmed' }), 'unconfirmed')
  assert.equal(nodePhase({ phase: 'something_new' }), 'something_new', 'an unknown status is shown as it arrived')
  const statuses = ['queued', 'downloading', 'running_on_worker', 'success', 'error', 'rolled_back', 'unconfirmed']
  for (const locale of readdirSync(new URL('../../public/locales/', import.meta.url))) {
    const updates = JSON.parse(readFileSync(new URL(`../../public/locales/${locale}/updates.json`, import.meta.url), 'utf8'))
    const operate = JSON.parse(readFileSync(new URL(`../../public/locales/${locale}/operate.json`, import.meta.url), 'utf8'))
    for (const status of statuses) assert.ok(updates.run.phase[nodePhase({ phase: status })], `${locale}: run.phase for node status ${status}`)
    assert.ok(operate.activity.node.rolledBack && operate.activity.node.unconfirmed, `${locale}: the operation card words a rolled-back and an unconfirmed node`)
  }
})

// ---- The risk lines of the review --------------------------------------------------

async function updatesT(lng) {
  const resources = {}
  for (const l of ['en', lng]) resources[l] = { updates: JSON.parse(readFileSync(new URL(`../../public/locales/${l}/updates.json`, import.meta.url), 'utf8')) }
  const i = i18next.createInstance()
  await i.init({ lng, fallbackLng: 'en', resources, defaultNS: 'updates', ns: ['updates'], interpolation: { escapeValue: false }, initAsync: false })
  return i.t.bind(i)
}

// Every risk code the server sends (core/services/upgraderun/planner.go), with
// the params it sends: decimal strings.
const SERVER_RISKS = [
  { code: 'stops_models', params: { count: '2', nodes: '2' } },
  { code: 'stops_models', params: { count: '1', nodes: '1' } },
  { code: 'cluster', params: { nodes: '3' } },
  { code: 'drift', params: { count: '1' } },
  { code: 'config_only', params: {} },
  { code: 'migrates_data', params: {} },
]

test('risk lines: every code the server sends has words in every locale, with its numbers', async () => {
  const en = await updatesT('en')
  assert.equal(riskText(SERVER_RISKS[0], en), 'Stops 2 loaded models on 2 nodes while it switches')
  assert.equal(riskText(SERVER_RISKS[1], en), 'Stops 1 loaded model while it switches')
  assert.equal(riskText(SERVER_RISKS[2], en), 'Updates on 3 nodes')
  assert.equal(riskText(SERVER_RISKS[3], en), '1 node runs a different build now. The update moves every node to the new one.')
  assert.equal(riskText(SERVER_RISKS[4], en), "Changes only the model's settings. Nothing is downloaded.")
  assert.equal(riskText(SERVER_RISKS[5], en), 'Your data is migrated. A verified backup is taken first.')
  for (const l of LOCALES) {
    const t = await updatesT(l)
    for (const r of SERVER_RISKS) {
      const words = riskText(r, t)
      assert.ok(words && !words.startsWith('review.') && !words.includes('{{'), `${l} ${r.code}: ${words}`)
    }
  }
})

test('risk lines: no line when there is nothing to say, never a zero or a guess', async () => {
  const en = await updatesT('en')
  assert.equal(riskText({ code: 'stops_models', params: {} }, en), '', 'no count: nothing is known to stop')
  assert.equal(riskText({ code: 'stops_models', params: { count: '0', nodes: '3' } }, en), '')
  assert.equal(riskText({ code: 'cluster', params: { nodes: '1' } }, en), '')
  assert.equal(riskText({ code: 'drift', params: {} }, en), '')
  assert.equal(riskText({ code: 'future_code', params: {} }, en), '')
  assert.equal(riskText(null, en), '')
})

test('upgrade run: needs attention is counted as the end notification counts it', () => {
  // Failed, rolled back, unconfirmed and skipped for a reason count; a
  // cancelled item and a skip with no reason do not.
  const run = runOf([
    item('gallery:a', 'failed'),
    item('gallery:b', 'rolled_back'),
    item('gallery:c', 'unconfirmed'),
    item('addon:notes', 'skipped', { error_key: 'not_human_surface' }),
    item('model:m', 'skipped', { error_key: 'held' }),
    item('model:n', 'skipped'),
    item('gallery:d', 'cancelled'),
    item('gallery:e', 'updated'),
  ], { state: 'finished' })
  assert.equal(attentionOf(run), 5)
  assert.equal(countsAsAttention(item('gallery:d', 'cancelled')), false)
  assert.equal(countsAsAttention(item('model:n', 'skipped')), false)
  assert.equal(countsAsAttention(item('addon:notes', 'skipped', { error_key: 'not_human_surface' })), true)
})
