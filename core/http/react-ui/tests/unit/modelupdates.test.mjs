import test from 'node:test'
import assert from 'node:assert/strict'
import {
  adoptBody, adoptCandidates, canAdopt, canHold, conflictKeys, fileSummary, missingChoices, modelIndicator, modelRow, modelRows, modelRowsByName,
  modelSource, opForModel, upgradeBody, valueText,
} from '../../src/utils/modelUpdates.js'
import {
  badgeOf, bulkItemsOf, filterCounts, groupOf, groupRows, headlineOf, needsPersonRows, sourceViews,
} from '../../src/utils/updates.js'

const D1 = 'sha256:1111111111111111aaaa'
const D2 = 'sha256:2222222222222222bbbb'

const item = (name, state, extra = {}) => ({
  id: `model:${name}`, kind: 'model', name, state, needsPerson: ['update-available', 'edited', 'failed'].includes(state),
  gallery: 'localai', entryName: name, installedDigest: D1, availableDigest: D2, ...extra,
})

test('model rows: each read model state lands in its page state and group', () => {
  const cases = [
    [item('a', 'update-available'), 'ready', 'ready'],
    [item('b', 'edited', { editedKeys: [{ key: 'context_size', mine: 8192, gallery: 4096, conflict: true }] }), 'ready', 'ready'],
    [item('c', 'failed', { upgrade: { phase: 'rolled-back', error: 'load failed' } }), 'failed', 'needs'],
    [item('d', 'held', { held: true }), 'on-hold', 'other'],
    [item('e', 'unknown', { reason: 'legacy-sidecar', availableDigest: '' }), 'unknown', 'other'],
    [item('f', 'gone'), 'gone', 'other'],
    [item('g', 'up-to-date', { availableDigest: D1 }), 'current', 'current'],
    [item('h', 'up-to-date', { availableDigest: D1, upgrade: { phase: 'confirmed', removableOld: true, retainedFiles: ['old.gguf'] } }), 'cleanup', 'other'],
  ]
  for (const [it, state, group] of cases) {
    const row = modelRow(it)
    assert.equal(row.kind, 'model')
    assert.equal(row.state, state, it.name)
    assert.equal(groupOf(row), group, it.name)
    assert.equal(row.startable, false, 'a model never takes part in Update ready')
  }
  const failed = modelRow(cases[2][0])
  assert.equal(failed.rolledBack, true)
  assert.equal(failed.error, 'load failed')
  const edited = modelRow(cases[1][0])
  assert.equal(edited.edited, true)
  assert.equal(edited.conflicts, 1)
  assert.equal(modelRow(cases[7][0]).retained, 1)
  assert.equal(modelRow(cases[0][0]).from, 'sha256:111111111111')
  assert.equal(modelRow(cases[0][0]).to, 'sha256:222222222222')
})

test('model rows: a running upgrade follows the operation and its phase, cancellable only before the stop', () => {
  const it = item('qwen', 'update-available')
  const op = (phase, extra = {}) => ({ id: 'qwen', name: 'qwen', jobID: 'j1', isBackend: false, isDeletion: false, isQueued: false, cancellable: true, progress: 40, taskType: 'upgrade', phase, ...extra })
  const staging = modelRow(it, [op('staging')])
  assert.equal(staging.state, 'updating')
  assert.equal(staging.phase, 'staging')
  assert.equal(staging.progress, 40)
  assert.equal(staging.cancellable, true)
  assert.equal(groupOf(staging), 'updating')
  assert.equal(modelRow(it, [op('unloading')]).cancellable, false)
  assert.equal(modelRow(it, [op('loading')]).cancellable, false)
  assert.equal(modelRow(it, [op('', { isQueued: true })]).phase, 'queued')
  // A queued upgrade has no progress to show, whatever the operation reports.
  assert.equal(modelRow(it, [op('', { isQueued: true })]).queued, true)
  assert.equal(modelRow(it, [op('', { isQueued: true })]).progress, 0)
  assert.equal(modelRow(it, [op('staging')]).queued, false)
  // A backend operation of the same name is not the model's.
  assert.equal(modelRow(it, [op('staging', { isBackend: true })]).state, 'ready')
  assert.equal(opForModel([op('staging', { isDeletion: true })], 'qwen'), null)
  // An install of the same name is not an upgrade of the row.
  assert.equal(opForModel([op('staging', { taskType: 'installation' })], 'qwen'), null)
  assert.equal(modelRow(it, [op('staging', { taskType: 'installation' })]).state, 'ready')
  assert.equal(opForModel([op('staging', { taskType: 'installation' }), op('loading')], 'qwen').phase, 'loading')
  // The journal alone says running when the list has not shown it yet.
  const journal = modelRow({ ...it, upgrade: { phase: 'committing', running: true, opId: 'j2' } })
  assert.equal(journal.state, 'updating')
  assert.equal(journal.phase, 'committing')
  assert.equal(journal.jobID, 'j2')
  // A failed operation of an update that the read model still offers is failed.
  assert.equal(modelRow(it, [op('staging', { error: 'no space left' })]).state, 'failed')
})

test('model rows: models always need a person, the hold does not count, and the number matches the list', () => {
  const rows = modelRows({
    items: [
      item('a', 'update-available'),
      item('b', 'edited', { editedKeys: [{ key: 'k', conflict: true }] }),
      item('c', 'failed', { upgrade: { phase: 'failed', error: 'x' } }),
      item('d', 'held', { held: true }),
      item('e', 'gone'),
      item('f', 'unknown', { reason: 'not-checked' }),
      item('g', 'up-to-date', { availableDigest: D1 }),
    ],
  })
  const need = needsPersonRows(rows)
  assert.deepEqual(need.map(r => r.name).sort(), ['a', 'b', 'c'])
  assert.deepEqual(badgeOf(rows), { count: 3, failed: true })
  // Running upgrades are followed, not counted.
  const running = modelRows({ items: [item('a', 'update-available')] }, { operations: [{ name: 'a', jobID: 'j', isBackend: false, taskType: 'upgrade', phase: 'staging' }] })
  assert.equal(badgeOf(running).count, 0)
  // Update ready never runs a model.
  assert.deepEqual(bulkItemsOf(rows), [])
  const groups = groupRows(rows)
  assert.deepEqual(groups.other.map(r => r.name).sort(), ['d', 'e', 'f'])
  assert.equal(filterCounts(rows).all, 7)
  assert.equal(filterCounts(rows).current, 1)
})

test('model rows: the headline counts only what can be acted on or is running', () => {
  const rows = modelRows({ items: [item('d', 'held', { held: true }), item('e', 'gone'), item('g', 'up-to-date', { availableDigest: D1 })] })
  const sources = sourceViews(null, null, { state: 'ok', checkedAt: '2026-11-19T10:00:00Z' })
  assert.deepEqual(sources.map(s => s.id), ['models'])
  assert.equal(headlineOf({ rows, sources }).kind, 'current')
  const more = [...rows, modelRow(item('a', 'update-available'))]
  assert.deepEqual(headlineOf({ rows: more, sources }), { kind: 'updates', count: 1 })
})

test('model source: a server with no model gallery says nothing, a failed read is failed', () => {
  assert.equal(modelSource(null, 'loading').loading, true)
  assert.equal(modelSource(null, 'error').state, 'failed')
  const disabled = modelSource({ sources: { gallery: { state: 'disabled', reason: 'none' } }, items: [] })
  assert.equal(disabled.state, 'disabled')
  assert.deepEqual(sourceViews(null, null, disabled), [])
  assert.equal(modelSource({ items: [] }), null)
})

test('upgrade choices: every conflicting key needs mine or gallery, and only those are sent', () => {
  const it = item('m', 'edited', {
    configOnly: true,
    editedKeys: [
      { key: 'context_size', mine: 8192, gallery: 4096, conflict: true },
      { key: 'parameters.temperature', mine: 0.2, gallery: 0.7, conflict: true },
      { key: 'name', mine: 'mine' },
    ],
  })
  assert.deepEqual(conflictKeys(it), ['context_size', 'parameters.temperature'])
  assert.deepEqual(missingChoices(it, {}), ['context_size', 'parameters.temperature'])
  assert.deepEqual(missingChoices(it, { context_size: 'mine' }), ['parameters.temperature'])
  assert.deepEqual(missingChoices(it, { context_size: 'mine', 'parameters.temperature': 'nope' }), ['parameters.temperature'])
  assert.deepEqual(missingChoices(it, { context_size: 'mine', 'parameters.temperature': 'gallery' }), [])
  // A key the server named in a 409 needs a choice too.
  assert.deepEqual(missingChoices(it, { context_size: 'mine', 'parameters.temperature': 'gallery' }, ['top_k']), ['top_k'])
  assert.deepEqual(
    upgradeBody(it, { choices: { context_size: 'mine', 'parameters.temperature': 'gallery', name: 'gallery' }, configOnly: true }),
    { config_only: true, choices: { context_size: 'mine', 'parameters.temperature': 'gallery' } },
  )
  // config_only is sent only for an update that downloads nothing.
  assert.deepEqual(upgradeBody({ ...it, configOnly: false, editedKeys: [] }, { configOnly: true }), {})
})

test('upgrade files and values: counts by change, files without a checksum, values as text', () => {
  const s = fileSummary({
    changedFiles: [
      { path: 'a.gguf', role: 'weights', change: 'changed', checksum: true },
      { path: 'b.gguf', role: 'mmproj', change: 'added', checksum: false },
      { path: 'c.tmpl', role: 'template', change: 'changed', checksum: false },
      { path: 'old.gguf', role: 'weights', change: 'removed', checksum: false },
    ],
  })
  assert.deepEqual(s, { total: 4, added: 1, changed: 2, removed: 1, downloads: 2, unchecked: 1 })
  assert.equal(valueText(undefined), null)
  assert.equal(valueText('x'), 'x')
  assert.equal(valueText(4096), '4096')
  assert.equal(valueText({ a: 1 }), '{"a":1}')
})

test('models page: the indicator and the hold switch follow the row', () => {
  const rows = modelRows({ items: [item('a', 'update-available'), item('d', 'held', { held: true }), item('e', 'unknown', { reason: 'legacy-sidecar' })] })
  const by = modelRowsByName(rows)
  assert.equal(modelIndicator(by.a).key, 'update')
  assert.equal(modelIndicator(by.d).key, 'held')
  assert.equal(modelIndicator(by.e), null)
  assert.equal(modelIndicator(undefined), null)
  assert.equal(canHold(by.a), true)
  assert.equal(canHold(by.e), false, 'no recorded origin, no hold')
  assert.equal(canHold({ ...by.a, state: 'updating' }), false)
})

test('adopt: only a model with no recorded origin can be adopted', () => {
  const row = (state, extra = {}) => modelRow(item('m', state, { availableDigest: '', ...extra }))
  assert.equal(canAdopt(row('unknown', { reason: 'legacy-sidecar' })), true)
  for (const reason of ['unreadable', 'not-checked', 'check-failed', '']) {
    assert.equal(canAdopt(row('unknown', { reason })), false, reason)
  }
  for (const state of ['update-available', 'edited', 'failed', 'held', 'gone', 'up-to-date']) {
    assert.equal(canAdopt(row(state, { reason: 'legacy-sidecar' })), false, state)
  }
  assert.equal(canAdopt(null), false)
  assert.equal(canAdopt({ kind: 'gallery', state: 'unknown', reason: 'legacy-sidecar' }), false)
})

test('adopt: the candidates of a refusal split into gallery and entry', () => {
  assert.deepEqual(adoptCandidates({ candidates: ['localai@qwen3-4b', 'community@qwen3-4b-instruct', 'localai@qwen3-4b', '', 7] }), [
    { id: 'localai@qwen3-4b', gallery: 'localai', entry: 'qwen3-4b' },
    { id: 'community@qwen3-4b-instruct', gallery: 'community', entry: 'qwen3-4b-instruct' },
  ])
  assert.deepEqual(adoptCandidates({ candidates: ['plain'] }), [{ id: 'plain', gallery: '', entry: 'plain' }])
  assert.deepEqual(adoptCandidates({ candidates: ['a@b@c'] }), [{ id: 'a@b@c', gallery: 'a', entry: 'b@c' }])
  assert.deepEqual(adoptCandidates({ error: 'x' }), [])
  assert.deepEqual(adoptCandidates(null), [])
})

test('adopt: the body names the gallery and the entry only when picked', () => {
  assert.deepEqual(adoptBody(null), {})
  assert.deepEqual(adoptBody({ gallery: 'localai', entry: 'qwen3-4b' }), { gallery: 'localai', entry: 'qwen3-4b' })
  assert.deepEqual(adoptBody({ gallery: '', entry: 'qwen3-4b' }), { entry: 'qwen3-4b' })
})
