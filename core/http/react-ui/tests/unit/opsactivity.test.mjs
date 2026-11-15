import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import i18next from 'i18next'
import {
  isFailedOp, splitOps, meanProgress, ringOf, openingTab, verbOf, titleOf, failedTitleOf, reasonOf, detailOf,
  canCancel, recentRecords, recentTitle, nextTab, RECENT_MAX,
} from '../../src/utils/opsActivity.js'

const LOCALES = readdirSync(new URL('../../public/locales', import.meta.url)).sort()
async function translator(lng) {
  const resources = {}
  for (const l of ['en', lng]) {
    resources[l] = {}
    for (const ns of ['chats', 'operate']) resources[l][ns] = JSON.parse(readFileSync(new URL(`../../public/locales/${l}/${ns}.json`, import.meta.url), 'utf8'))
  }
  const i = i18next.createInstance()
  await i.init({ lng, fallbackLng: 'en', resources, defaultNS: 'chats', ns: ['chats', 'operate'], interpolation: { escapeValue: false }, initAsync: false })
  return i.t.bind(i)
}

const op = (over = {}) => ({ id: 'qwen3-14b', name: 'qwen3-14b', jobID: 'j1', progress: 40, taskType: 'installation', isDeletion: false, isBackend: false, isQueued: false, cancellable: true, ...over })

test('ops activity: an operation with an error, or an add-on upgrade in state failed, needs attention', () => {
  assert.equal(isFailedOp(op()), false)
  assert.equal(isFailedOp(op({ error: 'no space left' })), true)
  assert.equal(isFailedOp({ state: 'failed' }), true)
  assert.equal(isFailedOp(null), false)
})

test('ops activity: the list splits into failed, running and queued, the queue last', () => {
  const list = [op({ jobID: 'q', isQueued: true }), op({ jobID: 'r' }), op({ jobID: 'f', error: 'x' })]
  const s = splitOps(list)
  assert.deepEqual(s.failed.map(o => o.jobID), ['f'])
  assert.deepEqual(s.running.map(o => o.jobID), ['r'])
  assert.deepEqual(s.queued.map(o => o.jobID), ['q'])
  assert.deepEqual(s.live.map(o => o.jobID), ['r', 'q'])
  assert.deepEqual(splitOps(undefined).live, [])
})

test('ops activity: the mean progress is of what runs, between 0 and 100', () => {
  assert.equal(meanProgress([]), 0)
  assert.equal(meanProgress([op({ progress: 20 }), op({ progress: 60 })]), 40)
  assert.equal(meanProgress([op({ progress: 250 })]), 100)
  assert.equal(meanProgress([op({ progress: undefined }), op({ progress: 50 })]), 25)
})

test('ops activity: the ring is idle with nothing live, fills with the mean, turns for a queue, and flags a failure', () => {
  assert.deepEqual(ringOf([]), { active: false, value: null, failed: false, failedCount: 0, count: 0 })
  assert.equal(ringOf([op({ progress: 33.4 }), op({ progress: 33.4 })]).value, 33)
  // Only a queue: nothing to average, the ring turns.
  const queued = ringOf([op({ isQueued: true })])
  assert.equal(queued.active, true)
  assert.equal(queued.value, null)
  // A failure alone is not live work, but it is flagged.
  const failed = ringOf([op({ error: 'boom' })])
  assert.equal(failed.active, false)
  assert.equal(failed.failed, true)
  assert.equal(failed.failedCount, 1)
  // A failure beside running work: both show; the mean is of the running ones.
  const both = ringOf([op({ progress: 80 }), op({ error: 'boom', progress: 10 })])
  assert.deepEqual([both.active, both.value, both.failed, both.count], [true, 80, true, 1])
})

test('ops activity: the panel opens on Activity when something runs, waits or failed', () => {
  assert.equal(openingTab(ringOf([])), 'notifications')
  assert.equal(openingTab(ringOf([op()])), 'activity')
  assert.equal(openingTab(ringOf([op({ isQueued: true })])), 'activity')
  assert.equal(openingTab(ringOf([op({ error: 'x' })])), 'activity')
  assert.equal(openingTab(undefined), 'notifications')
})

test('ops activity: the verb names the work, a removal and a staging job before an upgrade', () => {
  assert.equal(verbOf(op()), 'installing')
  assert.equal(verbOf(op({ taskType: 'upgrade' })), 'upgrading')
  assert.equal(verbOf(op({ isDeletion: true, taskType: 'deletion' })), 'removing')
  assert.equal(verbOf(op({ taskType: 'staging' })), 'staging')
  assert.equal(verbOf(op({ isDeletion: true, taskType: 'upgrade' })), 'removing')
})

test('ops activity: titles and failure titles are worded from the name', async () => {
  const t = await translator('en')
  assert.equal(titleOf(op(), t), 'Installing qwen3-14b')
  assert.equal(titleOf(op({ name: 'llama-cpp', taskType: 'upgrade', isBackend: true }), t), 'Updating llama-cpp')
  assert.equal(titleOf(op({ isDeletion: true }), t), 'Removing qwen3-14b')
  assert.equal(titleOf(op({ taskType: 'staging' }), t), 'Staging qwen3-14b')
  assert.equal(failedTitleOf(op(), t), "Couldn't install qwen3-14b")
  assert.equal(failedTitleOf(op({ taskType: 'upgrade' }), t), "Couldn't update qwen3-14b")
  assert.equal(failedTitleOf(op({ isDeletion: true }), t), "Couldn't remove qwen3-14b")
  // The name falls back to the id.
  assert.equal(titleOf({ id: 'x' }, t), 'Installing x')
})

test('ops activity: the reason is the server text, and an add-on code is worded by the UI', async () => {
  const t = await translator('en')
  assert.equal(reasonOf(op({ error: 'no space left on device' }), t), 'no space left on device')
  assert.match(reasonOf({ isAddon: true, failureCode: 'interrupted', error: 'raw' }, t), /interrupted/i)
  assert.equal(reasonOf({ isAddon: true, failureCode: 'unknown_code', error: 'raw' }, t), 'raw')
  assert.equal(reasonOf({}, t), '')
})

test('ops activity: the detail line joins node, phase, bytes and rate, and leaves out what is not reported', async () => {
  const t = await translator('en')
  const GB = 1024 ** 3
  const nodes = ['success', 'success', 'downloading', 'queued', 'queued'].map((status, i) => ({ node_id: `n${i}`, status }))
  assert.equal(
    detailOf(op({ nodes, phase: 'downloading', currentBytes: 1.2 * GB, totalBytes: 1.9 * GB, bytesPerSecond: 48 * 1024 * 1024 }), t),
    'Node 3 of 5 · Downloading · 1.2 GB / 1.9 GB · 48 MB/s',
  )
  assert.equal(detailOf(op({ phase: 'verifying' }), t), 'Verifying')
  // One node is the whole story: no node count.
  assert.equal(detailOf(op({ nodes: [{ node_id: 'a', status: 'downloading' }], phase: 'downloading' }), t), 'Downloading')
  // Every node done: the count stops at the last one.
  assert.equal(detailOf(op({ nodes: [{ status: 'success' }, { status: 'success' }] }), t), 'Node 2 of 2')
  // A legacy install has no phase: the server's message is all there is.
  assert.equal(detailOf(op({ message: 'processing model: x' }), t), 'processing model: x')
  assert.equal(detailOf(op({ taskType: 'staging', nodeName: 'gpu-1', phase: 'downloading' }), t), 'to gpu-1 · Downloading')
  assert.equal(detailOf(op(), t), '')
})

test('ops activity: a queued operation says it waits, whatever else it carries', async () => {
  const t = await translator('en')
  assert.equal(detailOf(op({ isQueued: true, phase: 'queued', message: 'queued' }), t), 'Waiting')
})

test('ops activity: an add-on upgrade shows its versions and stage', async () => {
  const t = await translator('en')
  assert.equal(detailOf({ isAddon: true, fromVersion: '1.0', toVersion: '1.1', stage: 'backing-up' }, t), '1.0 to 1.1 · Creating and verifying backup')
  assert.equal(detailOf({ isAddon: true, message: 'ignored' }, t), '')
})

test('ops activity: Cancel is for work that can be called off, not for a failure or a cancel already waiting', () => {
  assert.equal(canCancel(op()), true)
  assert.equal(canCancel(op({ cancellable: false })), false)
  assert.equal(canCancel(op({ error: 'x' })), false)
  assert.equal(canCancel(op(), true), false)
})

const rec = (over = {}) => ({ id: 'a', name: 'a', jobID: 'r1', taskType: 'installation', outcome: 'completed', startedAt: '2026-11-09T10:00:00Z', finishedAt: '2026-11-09T10:05:00Z', ...over })
const NOW = Date.parse('2026-11-09T12:00:00Z')

test('ops activity: Recent lists what completed, newest first, and nothing cancelled or failed', () => {
  const history = [
    rec({ jobID: 'old', finishedAt: '2026-11-09T09:00:00Z' }),
    rec({ jobID: 'cancelled', outcome: 'cancelled', finishedAt: '2026-11-09T11:59:00Z' }),
    rec({ jobID: 'failed', outcome: 'failed', finishedAt: '2026-11-09T11:58:00Z' }),
    rec({ jobID: 'new', finishedAt: '2026-11-09T11:00:00Z' }),
  ]
  assert.deepEqual(recentRecords(history, { now: NOW }).map(r => r.jobID), ['new', 'old'])
})

test('ops activity: Recent stops at five, a day back, and shows a job once', () => {
  const many = Array.from({ length: 9 }, (_, i) => rec({ jobID: `j${i}`, finishedAt: new Date(NOW - (i + 1) * 60_000).toISOString() }))
  assert.equal(recentRecords(many, { now: NOW }).length, RECENT_MAX)
  assert.equal(RECENT_MAX, 5)
  assert.deepEqual(recentRecords([rec({ finishedAt: '2026-11-08T11:00:00Z' })], { now: NOW }), [])
  assert.equal(recentRecords([rec(), rec()], { now: NOW }).length, 1)
  // A record with no usable time is not shown: its age is unknown.
  assert.deepEqual(recentRecords([rec({ finishedAt: '' })], { now: NOW }), [])
  assert.deepEqual(recentRecords(undefined, { now: NOW }), [])
})

test('ops activity: Recent titles say what finished', async () => {
  const t = await translator('en')
  assert.equal(recentTitle(rec(), t), 'Installed a')
  assert.equal(recentTitle(rec({ taskType: 'upgrade' }), t), 'Updated a')
  assert.equal(recentTitle(rec({ taskType: 'deletion' }), t), 'Removed a')
  assert.equal(recentTitle(rec({ taskType: 'staging' }), t), 'Staged a')
})

test('ops activity: the arrows, Home and End move between the two tabs', () => {
  assert.equal(nextTab('notifications', 'ArrowRight'), 'activity')
  assert.equal(nextTab('activity', 'ArrowRight'), 'notifications')
  assert.equal(nextTab('notifications', 'ArrowLeft'), 'activity')
  assert.equal(nextTab('activity', 'Home'), 'notifications')
  assert.equal(nextTab('notifications', 'End'), 'activity')
  assert.equal(nextTab('notifications', 'a'), '')
  assert.equal(nextTab('nope', 'ArrowRight'), '')
})

test('ops activity: every language words every title and label with no key or placeholder left over', async () => {
  const GB = 1024 ** 3
  for (const lng of LOCALES) {
    const t = await translator(lng)
    const lines = []
    for (const o of [op(), op({ taskType: 'upgrade' }), op({ isDeletion: true }), op({ taskType: 'staging', nodeName: 'n1' })]) {
      lines.push(titleOf(o, t), failedTitleOf(o, t))
    }
    for (const taskType of ['installation', 'upgrade', 'deletion', 'staging']) lines.push(recentTitle(rec({ taskType }), t))
    lines.push(detailOf(op({ isQueued: true }), t))
    lines.push(detailOf(op({ nodes: [{ status: 'success' }, { status: 'queued' }, { status: 'queued' }], phase: 'committing', currentBytes: GB, totalBytes: 2 * GB }), t))
    for (const phase of ['resolving', 'downloading', 'verifying', 'committing', 'persisting']) lines.push(detailOf(op({ phase }), t))
    for (const key of [
      'inbox.tabs.label', 'inbox.tabs.notifications', 'inbox.tabs.activity', 'inbox.ops.needsAttention', 'inbox.ops.inProgress',
      'inbox.ops.recent.title', 'inbox.ops.seeAll', 'inbox.ops.emptyTitle', 'inbox.ops.emptyBody',
    ]) lines.push(t(key))
    for (const key of ['cancel', 'retry', 'moveToHistory', 'undo', 'cancelNow', 'cancelling']) lines.push(t(`activity.${key}`, { ns: 'operate' }))
    lines.push(t('activity.cancelLabel', { ns: 'operate', name: 'x' }), t('activity.retryLabel', { ns: 'operate', name: 'x' }), t('activity.dismissLabel', { ns: 'operate', name: 'x' }))
    for (const line of lines) {
      assert.ok(line && line.trim(), `${lng}: an empty string`)
      assert.ok(!/\{\{|\}\}/.test(line), `${lng}: a placeholder is left in "${line}"`)
      assert.ok(!/^(inbox|activity)\./.test(line), `${lng}: a key shows as text: "${line}"`)
    }
    // The name goes into every title.
    assert.ok(titleOf(op({ name: 'zzz' }), t).includes('zzz'), `${lng}: the title lacks the name`)
    assert.ok(failedTitleOf(op({ name: 'zzz' }), t).includes('zzz'), `${lng}: the failed title lacks the name`)
  }
})

test('ops activity: no language falls back to English for a string of the tab', () => {
  const read = (l, ns) => JSON.parse(readFileSync(new URL(`../../public/locales/${l}/${ns}.json`, import.meta.url), 'utf8'))
  const leaves = (v, p = '', out = []) => {
    if (v && typeof v === 'object') for (const [k, c] of Object.entries(v)) leaves(c, p ? `${p}.${k}` : k, out)
    else out.push(p)
    return out
  }
  const want = {
    chats: leaves(read('en', 'chats').inbox.tabs, 'inbox.tabs').concat(leaves(read('en', 'chats').inbox.ops, 'inbox.ops')),
    nav: ['inboxOps_one', 'inboxOps_other', 'inboxOpsFailed_one', 'inboxOpsFailed_other', 'inboxOpsFailedAnnounce', 'activityEntry'],
  }
  // Words that are the same in English and the language.
  const same = new Set(['fr/chats:inbox.tabs.notifications'])
  const get = (d, path) => path.split('.').reduce((a, k) => (a == null ? a : a[k]), d)
  for (const l of LOCALES.filter(x => x !== 'en')) {
    for (const ns of Object.keys(want)) {
      const d = read(l, ns)
      for (const key of want[ns]) {
        const v = ns === 'nav' ? d[key] : get(d, key)
        assert.ok(typeof v === 'string' && v, `${l}/${ns}: ${key} is missing`)
        if (same.has(`${l}/${ns}:${key}`)) continue
        assert.notEqual(v, ns === 'nav' ? read('en', ns)[key] : get(read('en', ns), key), `${l}/${ns}: ${key} is still English`)
      }
    }
  }
})
