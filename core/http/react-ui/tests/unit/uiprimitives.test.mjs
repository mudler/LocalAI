// The pure rules behind the UI primitives: button classes, the status
// vocabulary, the toast stack and the load state.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buttonClass } from '../../src/components/ui/buttonClass.js'
import { resolveStatus, legacyPillTone, STATUS } from '../../src/components/ui/status.js'
import { toastDuration, pushToast, markLeaving, toastLift, TOAST_MS, TOAST_MAX } from '../../src/components/ui/toastModel.js'
import { initialLoad, loadReducer, errorMessage } from '../../src/utils/loadState.js'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')

test('button classes: the kit button with one variant and size', () => {
  assert.equal(buttonClass(), 'dk-btn dk-btn--secondary')
  assert.equal(buttonClass({ variant: 'primary', size: 'sm' }), 'dk-btn dk-btn--primary dk-btn--sm')
  assert.equal(buttonClass({ variant: 'danger-ghost', iconOnly: true, className: 'x' }), 'dk-btn dk-btn--danger-ghost dk-btn--icon x')
  // An unknown variant falls back to secondary, never to an unstyled button.
  assert.equal(buttonClass({ variant: 'loud' }), 'dk-btn dk-btn--secondary')
  assert.equal(buttonClass({ size: 'lg' }), 'dk-btn dk-btn--secondary dk-btn--lg')
})

test('status vocabulary: each state has a tone, an icon and a label', () => {
  for (const [word, s] of Object.entries(STATUS)) {
    assert.ok(['ok', 'warn', 'error', 'idle', 'accent'].includes(s.tone), word)
    assert.ok(s.icon, word)
    assert.ok(s.label, word)
  }
  assert.deepEqual(resolveStatus({ status: 'failed' }), { tone: 'error', icon: 'alert-circle', label: 'failed', busy: false })
  assert.equal(resolveStatus({ status: 'starting' }).busy, true)
  // Aliases used by failover and the add-on SDK.
  assert.equal(resolveStatus({ status: 'healthy' }).tone, 'ok')
  assert.equal(resolveStatus({ status: 'degraded' }).tone, 'error')
  assert.equal(resolveStatus({ status: 'fallback' }).tone, 'warn')
  assert.equal(resolveStatus({ status: 'loading' }).tone, 'accent')
  // An unknown word claims nothing.
  assert.equal(resolveStatus({ status: 'whatever' }).tone, 'idle')
  // An explicit tone wins and keeps the tone's icon.
  assert.deepEqual(resolveStatus({ tone: 'warn' }), { tone: 'warn', icon: 'warning', label: undefined, busy: false })
  assert.equal(legacyPillTone('ok'), 'success')
  assert.equal(legacyPillTone('idle'), 'muted')
})

test('status vocabulary: every label has English text in common.json', () => {
  const en = JSON.parse(readFileSync(join(root, 'public/locales/en/common.json'), 'utf8'))
  for (const s of Object.values(STATUS)) assert.ok(en.chip[s.label], s.label)
  for (const key of ['tryAgain', 'undo']) assert.ok(en.ui[key], key)
  assert.match(en.ui.loadError.serverSaid, /\{\{message\}\}/)
})

test('toast timing: 4 s news, 8 s with an action or a warning, errors stay', () => {
  assert.equal(toastDuration({ type: 'success' }), TOAST_MS.short)
  assert.equal(toastDuration({ type: 'info' }), 4000)
  assert.equal(toastDuration({ type: 'success', action: { label: 'Undo' } }), 8000)
  assert.equal(toastDuration({ type: 'warning' }), 8000)
  assert.equal(toastDuration({ type: 'error' }), 0)
  // A caller's own duration is kept.
  assert.equal(toastDuration({ type: 'error', duration: 3000 }), 3000)
  assert.equal(toastDuration({ type: 'success', duration: -1 }), 0)
})

test('toast stack: at most three live toasts, the oldest leaves first', () => {
  let list = []
  for (let id = 1; id <= TOAST_MAX; id++) list = pushToast(list, { id })
  assert.equal(list.filter(t => !t.exiting).length, 3)
  list = pushToast(list, { id: 4 })
  assert.deepEqual(list.map(t => [t.id, !!t.exiting]), [[1, true], [2, false], [3, false], [4, false]])
  // A leaving toast does not count against the limit.
  list = pushToast(list, { id: 5 })
  assert.deepEqual(list.filter(t => !t.exiting).map(t => t.id), [3, 4, 5])
  assert.deepEqual(markLeaving([{ id: 1 }, { id: 2 }], 2), [{ id: 1 }, { id: 2, exiting: true }])
})

test('toast lift: clears a bar at the bottom, ignores anything else', () => {
  assert.equal(toastLift(800, { top: 680, height: 120 }), 120)
  assert.equal(toastLift(800, null), 0)
  assert.equal(toastLift(800, { top: 100, height: 120 }), 0)  // high on the page: not a bottom bar
  assert.equal(toastLift(800, { top: 820, height: 120 }), 0)  // below the fold
  assert.equal(toastLift(800, { top: 700, height: 0 }), 0)    // hidden
})

test('load state: loading, ready and error, and old answers are ignored', () => {
  let s = loadReducer(initialLoad, { type: 'start', request: 1 })
  assert.equal(s.status, 'loading')
  s = loadReducer(s, { type: 'failure', request: 1, error: new Error('database is locked') })
  assert.equal(s.status, 'error')
  s = loadReducer(s, { type: 'start', request: 2 })
  // The first request answers late: ignored.
  assert.equal(loadReducer(s, { type: 'success', request: 1, data: ['old'] }), s)
  s = loadReducer(s, { type: 'success', request: 2, data: [] })
  assert.deepEqual([s.status, s.data, s.error], ['ready', [], null])
  // A reload keeps the ready data on screen.
  const r = loadReducer({ ...s, data: ['a'] }, { type: 'start', request: 3, keep: true })
  assert.deepEqual([r.status, r.data, r.refreshing], ['ready', ['a'], true])
})

test('load state: the error text is the server message', () => {
  assert.equal(errorMessage(new Error('database is locked')), 'database is locked')
  assert.equal(errorMessage({ error: { message: 'no such model' } }), 'no such model')
  assert.equal(errorMessage('timeout'), 'timeout')
  assert.equal(errorMessage(null), '')
})
