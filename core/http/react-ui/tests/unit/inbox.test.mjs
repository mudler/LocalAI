import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import i18next from 'i18next'
import {
  badgeText, sourceOf, toneOf, iconOf, targetOf, reasonText, bodyOf, dayOf, groupByDay, filterItems,
  hasUnreadFailure, entryVisible, inboxOff, streamWanted, textOf, WATCH_CODES, linkDisabled, runProgressOf, runItemKind,
} from '../../src/utils/inbox.js'

const note = (extra = {}) => ({ id: 1, kind: 'run_ended', status: 'ok', title: 'T', body: '', link: '', read: false, created: '', ...extra })

test('inbox: the badge shows the unread count and stops at 99+', () => {
  assert.equal(badgeText(0), '')
  assert.equal(badgeText(undefined), '')
  assert.equal(badgeText(1), '1')
  assert.equal(badgeText(99), '99')
  assert.equal(badgeText(100), '99+')
})

test('inbox: the source comes from the server, else from the kind', () => {
  assert.equal(sourceOf(note({ source: 'connector' })), 'connector')
  assert.equal(sourceOf(note({ source: 'system' })), 'system')
  // Items stored before sources existed.
  assert.equal(sourceOf(note({ kind: 'run_failed' })), 'trigger')
  assert.equal(sourceOf(note({ kind: 'needs_attention' })), 'system')
  assert.equal(sourceOf(note({ source: 'other', kind: 'run_ended' })), 'trigger')
})

test('inbox: a failure is an error tile, a finished run ok, news accent', () => {
  assert.equal(toneOf(note({ status: 'failed', kind: 'run_failed' })), 'error')
  assert.equal(toneOf(note({ status: 'failed', kind: 'needs_attention' })), 'error')
  assert.equal(toneOf(note()), 'ok')
  assert.equal(toneOf(note({ kind: 'needs_attention' })), 'accent')
  assert.equal(iconOf(note({ source: 'trigger' })), 'bolt')
  assert.equal(iconOf(note({ source: 'connector', status: 'failed' })), 'plug-off')
  assert.equal(iconOf(note({ source: 'system', kind: 'needs_attention' })), 'info')
})

test('inbox: only paths in the app are opened; a connector without one opens its page', () => {
  assert.equal(targetOf(note({ link: '/app/chats/c1' })), '/app/chats/c1')
  assert.equal(targetOf(note({ link: 'https://example.com/x' })), '')
  assert.equal(targetOf(note({ link: '//example.com' })), '')
  assert.equal(targetOf(note({ source: 'connector', kind: 'needs_attention' })), '/app/chats/connectors')
  assert.equal(targetOf(note({ source: 'trigger', kind: 'needs_attention' })), '')
})

test('inbox: with the chats off a link into the chats opens nothing and says why', () => {
  const chat = note({ link: '/app/chats/c1' })
  assert.equal(targetOf(chat, false), '')
  assert.equal(linkDisabled(chat, false), true)
  assert.equal(linkDisabled(chat, true), false)
  assert.equal(targetOf(note({ source: 'connector', kind: 'needs_attention' }), false), '')
  assert.equal(linkDisabled(note({ source: 'connector', kind: 'needs_attention' }), false), true)
  // A page that is not a chat page stays a link, and an item with no link is not "disabled".
  assert.equal(targetOf(note({ link: '/app/updates?filter=ready' }), false), '/app/updates?filter=ready')
  assert.equal(linkDisabled(note({ link: '/app/updates' }), false), false)
  assert.equal(linkDisabled(note({ link: '' }), false), false)
  assert.equal(targetOf(note({ link: '/app/chatsx' }), false), '/app/chatsx')
})

test('inbox: a reason starts with a capital', () => {
  assert.equal(reasonText('took longer than 15m'), 'Took longer than 15m')
  assert.equal(reasonText('  '), '')
  assert.equal(reasonText(undefined), '')
})

test('inbox: items group into today, yesterday and earlier in order', () => {
  const now = new Date(2026, 9, 31, 10, 0).getTime()
  const at = (d, h) => new Date(2026, 9, d, h, 0).toISOString()
  assert.equal(dayOf(at(31, 0), now), 'today')
  assert.equal(dayOf(at(30, 23), now), 'yesterday')
  assert.equal(dayOf(at(30, 0), now), 'yesterday')
  assert.equal(dayOf(at(29, 23), now), 'earlier')
  assert.equal(dayOf('', now), 'earlier')
  const items = [note({ id: 4, created: at(31, 9) }), note({ id: 3, created: at(31, 1) }), note({ id: 2, created: at(30, 8) }), note({ id: 1, created: at(2, 8) })]
  const groups = groupByDay(items, now)
  assert.deepEqual(groups.map(g => [g.day, g.items.map(n => n.id)]), [['today', [4, 3]], ['yesterday', [2]], ['earlier', [1]]])
  assert.deepEqual(groupByDay([], now), [])
})

test('inbox: the filters pick unread or failed items', () => {
  const items = [note({ id: 1 }), note({ id: 2, read: true, status: 'failed' }), note({ id: 3, status: 'failed' })]
  assert.deepEqual(filterItems(items, 'all').map(n => n.id), [1, 2, 3])
  assert.deepEqual(filterItems(items, 'unread').map(n => n.id), [1, 3])
  assert.deepEqual(filterItems(items, 'failed').map(n => n.id), [2, 3])
  assert.deepEqual(filterItems(undefined, 'all'), [])
})

test('inbox: the badge is red only when an unread item failed', () => {
  assert.equal(hasUnreadFailure([]), false)
  assert.equal(hasUnreadFailure(undefined), false)
  assert.equal(hasUnreadFailure([note(), note({ id: 2, status: 'failed', read: true })]), false)
  assert.equal(hasUnreadFailure([note(), note({ id: 2, status: 'failed' })]), true)
})

test('inbox: the navbar entry is for an administrator always and for others once they have items', () => {
  const base = { enabled: true, off: false, loaded: true, isAdmin: false, total: 0, everHad: false, open: false }
  // An administrator sees it with an empty inbox; anyone else does not.
  assert.equal(entryVisible({ ...base, isAdmin: true }), true)
  assert.equal(entryVisible(base), false)
  assert.equal(entryVisible({ ...base, total: 3 }), true)
  // It stays after the list was cleared, and while the panel is open.
  assert.equal(entryVisible({ ...base, everHad: true }), true)
  assert.equal(entryVisible({ ...base, open: true }), true)
})

test('inbox: nobody sees the entry when the inbox is off or not read yet', () => {
  const base = { enabled: true, off: false, loaded: true, isAdmin: true, total: 5, everHad: true, open: true }
  assert.equal(entryVisible({ ...base, enabled: false }), false)
  assert.equal(entryVisible({ ...base, off: true }), false)
  assert.equal(entryVisible({ ...base, loaded: false }), false)
})

test('inbox: 403, 404 and 409 mean there is no inbox, other failures are retried', () => {
  for (const s of [403, 404, 409]) assert.equal(inboxOff(s), true)
  for (const s of [undefined, 0, 401, 500, 502]) assert.equal(inboxOff(s), false)
})

test('inbox: the stream is open only for an enabled hook on a visible tab', () => {
  assert.equal(streamWanted({ enabled: true, visible: true, off: false }), true)
  assert.equal(streamWanted({ enabled: false, visible: true, off: false }), false)
  assert.equal(streamWanted({ enabled: true, visible: false, off: false }), false)
  assert.equal(streamWanted({ enabled: true, visible: true, off: true }), false)
})

// ---- Items worded by the UI ---------------------------------------------------

const LOCALES = readdirSync(new URL('../../public/locales', import.meta.url)).sort()
async function translator(lng) {
  const resources = {}
  for (const l of ['en', lng]) resources[l] = { chats: JSON.parse(readFileSync(new URL(`../../public/locales/${l}/chats.json`, import.meta.url), 'utf8')) }
  const i = i18next.createInstance()
  await i.init({ lng, fallbackLng: 'en', resources, defaultNS: 'chats', ns: ['chats'], interpolation: { escapeValue: false }, initAsync: false })
  return i.t.bind(i)
}

const digest = (extra = {}) => note({ code: 'updates.digest', title: 'stored title', body: 'stored body', params: { family: 'backends', count: 3, names: ['llama-cpp', 'whisper', 'piper'] }, ...extra })
const result = (p) => note({ code: 'updates.result', title: 'stored title', body: 'stored body', params: { family: 'backends', ...p } })

test('inbox: the digest is worded from its params, with a plural title and the names', async () => {
  const t = await translator('en')
  assert.deepEqual(textOf(digest(), t), { title: '3 backend updates available', body: 'llama-cpp, whisper, piper' })
  assert.equal(textOf(digest({ params: { family: 'backends', count: 1, names: ['whisper'] } }), t).title, '1 backend update available')
  assert.equal(textOf(digest({ params: { family: 'backends', count: 5, names: ['a', 'b', 'c'] } }), t).body, 'a, b, c and 2 more')
})

test('inbox: a result names the family, the counts and what happened to which backend', async () => {
  const t = await translator('en')
  const out = textOf(result({ updated: 2, failed: 1, updatedItems: ['llama-cpp 0.9.4 to 0.9.7', 'piper'], failedItems: ['whisper (download interrupted)'] }), t)
  assert.equal(out.title, 'Backends: 2 updated automatically, 1 failed')
  assert.equal(out.body, 'Updated: llama-cpp 0.9.4 to 0.9.7, piper. Failed: whisper (download interrupted).')
  const held = textOf(note({ code: 'updates.result', params: { family: 'addons', held: 1, heldItems: ['Tables 2.4.0'] } }), t)
  assert.equal(held.title, 'Add-ons: 1 waiting for approval')
  assert.equal(held.body, 'Waiting for your approval: Tables 2.4.0.')
})

test('inbox: a failed check names the sources', async () => {
  const t = await translator('en')
  const out = textOf(note({ code: 'updates.checkFailed', params: { sources: ['gallery', 'library'] } }), t)
  assert.equal(out.title, 'The update check failed')
  assert.match(out.body, /Could not check: backend gallery, backend library\./)
})

test('inbox: the end of an operation is worded for every action and outcome, in every language', async () => {
  const en = await translator('en')
  const op = (code, params, extra = {}) => note({ code, title: 'stored', body: 'download interrupted', params, ...extra })
  assert.equal(textOf(op('updates.opDone', { kind: 'model', action: 'install', name: 'qwen3-8b' }), en).title, 'Model qwen3-8b installed')
  assert.equal(textOf(op('updates.opDone', { kind: 'backend', action: 'upgrade', name: 'llama-cpp' }), en).title, 'Backend llama-cpp upgraded')
  assert.equal(textOf(op('updates.opDone', { kind: 'model', action: 'delete', name: 'old' }), en).title, 'Model old removed')
  const bad = textOf(op('updates.opFailed', { kind: 'backend', action: 'install', name: 'whisper', outcome: 'failed' }), en)
  assert.deepEqual(bad, { title: 'Backend whisper: install failed', body: 'download interrupted' })
  assert.equal(textOf(op('updates.opFailed', { kind: 'model', action: 'install', name: 'm', outcome: 'cancelled' }), en).title, 'Model m: install cancelled')
  // Nonsense params show the stored text.
  assert.equal(textOf(op('updates.opDone', { kind: 'thing', action: 'install', name: 'x' }), en).title, 'stored')
  assert.equal(textOf(op('updates.opDone', { kind: 'model', action: 'install' }), en).title, 'stored')
  for (const l of LOCALES) {
    const t = await translator(l)
    for (const action of ['install', 'delete', 'upgrade']) {
      for (const [code, outcome] of [['updates.opDone'], ['updates.opFailed', 'failed'], ['updates.opFailed', 'cancelled']]) {
        const title = textOf(op(code, { kind: 'model', action, name: 'NAME', outcome }), t).title
        assert.ok(title.includes('NAME') && !title.includes('{{') && title !== 'stored', `${l} ${code} ${action}: ${title}`)
      }
    }
  }
  assert.equal(iconOf(op('updates.opDone', { kind: 'model', action: 'install', name: 'm' }, { status: 'ok' })), 'check-circle')
  assert.equal(iconOf(op('updates.opFailed', { kind: 'model', action: 'install', name: 'm', outcome: 'failed' }, { status: 'failed' })), 'alert-circle')
  assert.equal(iconOf(op('updates.opFailed', { kind: 'model', action: 'install', name: 'm', outcome: 'cancelled' }, { status: 'ok' })), 'info')
})

test('inbox: an unknown code, missing params or nonsense params show the stored text', async () => {
  const t = await translator('en')
  const stored = { title: 'stored title', body: 'stored body' }
  assert.deepEqual(textOf(digest({ code: 'future.thing' }), t), stored)
  assert.deepEqual(textOf(digest({ params: undefined }), t), stored)
  assert.deepEqual(textOf(digest({ params: { family: 'backends', count: 'many' } }), t), stored)
  assert.deepEqual(textOf(result({ updated: 0, failed: 0, held: 0 }), t), stored)
  assert.deepEqual(textOf(result({ family: 'models', updated: 1 }), t), stored)
  assert.deepEqual(textOf(note({ code: 'updates.checkFailed', title: 'stored title', body: 'stored body', params: { sources: ['x'] } }), t), stored)
  // An item without a code is untouched.
  assert.deepEqual(textOf(note({ title: 'Run 1', body: 'b', params: { count: 3 } }), t), { title: 'Run 1', body: 'b' })
})

test('inbox: every language words every code with no key or placeholder left over', async () => {
  const cases = [
    digest(),
    digest({ params: { family: 'backends', count: 1, names: ['whisper'] } }),
    digest({ params: { family: 'backends', count: 6, names: ['a', 'b', 'c'] } }),
    result({ updated: 2, failed: 1, held: 1, updatedItems: ['a'], failedItems: ['b (x)'], heldItems: ['c'] }),
    result({ family: 'addons', updated: 1, updatedItems: ['Tables 2.4.0'] }),
    note({ code: 'updates.checkFailed', params: { sources: ['gallery', 'library', 'addons'] } }),
  ]
  for (const lng of LOCALES) {
    const t = await translator(lng)
    for (const c of cases) {
      const { title, body } = textOf(c, t)
      for (const text of [title, body]) {
        assert.ok(text && !/\{\{|inbox\.codes/.test(text), `${lng}: ${c.code}: ${JSON.stringify(text)}`)
      }
    }
  }
})

test('inbox: an update item has the arrow icon, whatever its source', () => {
  assert.equal(iconOf(digest()), 'arrow-up')
  assert.equal(iconOf(note({ code: 'updates.result', status: 'failed' })), 'arrow-up')
  assert.equal(iconOf(note({ source: 'trigger' })), 'bolt')
})

// ---- Model update items -----------------------------------------------------

const D1 = 'sha256:1111111111111111aaaa'
const D2 = 'sha256:2222222222222222bbbb'
const modelNote = (code, params, extra = {}) => ({ id: 'n', code, title: 'stored', body: 'stored body', status: 'ok', params, ...extra })

test('inbox: the model codes are worded from their params', async () => {
  const t = await translator('en')
  assert.deepEqual(textOf(modelNote('models.updateAvailable', { family: 'models', count: 2, names: ['qwen3-8b', 'whisper-1'] }), t), { title: '2 model updates available', body: 'qwen3-8b, whisper-1' })
  assert.equal(textOf(modelNote('models.updateAvailable', { family: 'models', count: 1, names: ['a'] }), t).title, '1 model update available')
  assert.deepEqual(textOf(modelNote('models.upgradeDone', { name: 'qwen3-8b', from: D1, to: D2 }), t), { title: 'Model qwen3-8b upgraded', body: 'From sha256:111111111111 to sha256:222222222222.' })
  assert.deepEqual(
    textOf(modelNote('models.upgradeFailed', { name: 'q', rolledBack: true, error: 'the model did not load' }, { status: 'failed' }), t),
    { title: 'Model q: the upgrade failed', body: 'The old version was put back and keeps serving. Reason: the model did not load' },
  )
  assert.equal(textOf(modelNote('models.upgradeFailed', { name: 'q', rolledBack: false }, { body: '' }), t).body, 'The old version keeps serving.')
  assert.deepEqual(textOf(modelNote('models.removedOld', { name: 'q', files: 3 }), t), { title: 'Model q: old version removed', body: '3 old files deleted.' })
  // Params that do not fit show the stored text.
  assert.deepEqual(textOf(modelNote('models.updateAvailable', { family: 'backends', count: 2 }), t), { title: 'stored', body: 'stored body' })
  assert.deepEqual(textOf(modelNote('models.upgradeDone', {}), t), { title: 'stored', body: 'stored body' })
  assert.deepEqual(textOf(modelNote('models.future', { name: 'x' }), t), { title: 'stored', body: 'stored body' })
  assert.equal(iconOf(modelNote('models.upgradeDone', { name: 'q' })), 'check-circle')
  assert.equal(iconOf(modelNote('models.upgradeFailed', { name: 'q' }, { status: 'failed' })), 'alert-circle')
  assert.equal(iconOf(modelNote('models.removedOld', { name: 'q', files: 1 })), 'trash')
  assert.equal(iconOf(modelNote('models.updateAvailable', { family: 'models', count: 1 })), 'arrow-up')
})

test('inbox: the model codes are worded in every language', async () => {
  for (const l of LOCALES) {
    const t = await translator(l)
    const out = [
      textOf(modelNote('models.updateAvailable', { family: 'models', count: 2, names: ['NAME'] }), t),
      textOf(modelNote('models.upgradeDone', { name: 'NAME', from: D1, to: D2 }), t),
      textOf(modelNote('models.upgradeFailed', { name: 'NAME', rolledBack: true, error: 'ERR' }), t),
      textOf(modelNote('models.upgradeFailed', { name: 'NAME', rolledBack: false }, { body: '' }), t),
      textOf(modelNote('models.removedOld', { name: 'NAME', files: 2 }), t),
    ]
    for (const o of out) {
      assert.ok(o.title !== 'stored' && !o.title.includes('{{') && !o.body.includes('{{'), `${l}: ${JSON.stringify(o)}`)
    }
    assert.ok(out[2].body.includes('ERR'), l)
  }
})

test('inbox: an add-on install is worded, and one that needs a new review says so, in every language', async () => {
  const en = await translator('en')
  const op = (code, params) => note({ code, title: 'stored', body: 'the add-on needs your approval again', params })
  assert.equal(textOf(op('updates.opDone', { kind: 'addon', action: 'install', name: 'notes' }), en).title, 'Add-on notes installed')
  assert.equal(textOf(op('updates.opFailed', { kind: 'addon', action: 'install', name: 'notes', outcome: 'failed' }), en).title, 'Add-on notes: install failed')
  const review = textOf(op('updates.opFailed', { kind: 'addon', action: 'install', name: 'notes', outcome: 'failed', needsApproval: true }), en)
  assert.deepEqual(review, { title: 'Add-on notes needs your review to install', body: 'the add-on needs your approval again' })
  // The flag only matters for a failure, not for a model or a cancel.
  assert.equal(textOf(op('updates.opFailed', { kind: 'model', action: 'install', name: 'm', outcome: 'failed', needsApproval: true }), en).title, 'Model m: install failed')
  assert.equal(textOf(op('updates.opFailed', { kind: 'addon', action: 'install', name: 'notes', outcome: 'cancelled', needsApproval: true }), en).title, 'Add-on notes: install cancelled')
  for (const l of LOCALES) {
    const t = await translator(l)
    const title = textOf(op('updates.opFailed', { kind: 'addon', action: 'install', name: 'NAME', outcome: 'failed', needsApproval: true }), t).title
    assert.ok(title.includes('NAME') && !title.includes('{{') && title !== 'stored', `${l}: ${title}`)
  }
})

// ---- Update runs --------------------------------------------------------------

const live = (p, extra = {}) => note({ kind: 'system', code: 'updates.run', title: 'stored title', body: 'stored body', params: { run: 'r1', total: 6, done: 2, progress: 33, source: 'manual', ...p }, ...extra })
const ended = (p, extra = {}) => note({ kind: 'system', code: 'updates.run.end', title: 'stored title', body: 'stored body', params: { run: 'r1', total: 6, updated: 5, failed: 1, source: 'manual', ...p }, ...extra })

test('inbox: the live item of an update run says how far it is, with its counts when the server sends them', async () => {
  const t = await translator('en')
  assert.deepEqual(textOf(live({}), t), { title: 'Updating 6 items', body: '2 of 6 done' })
  assert.deepEqual(textOf(live({ updated: 2, running: 2, queued: 2 }), t), { title: 'Updating 6 items', body: '2 updated, 2 running, 2 waiting' })
  assert.equal(textOf(live({ source: 'auto', total: 1 }), t).title, 'Automatic update: 1 item')
  assert.equal(runProgressOf(live({})), 33)
  assert.equal(runProgressOf(live({ progress: 140 })), 100)
  assert.equal(runProgressOf(live({ progress: 'x' })), 0)
  assert.equal(runProgressOf(ended({})), null, 'the summary has no bar')
  assert.equal(runItemKind(live({})), 'live')
  assert.equal(runItemKind(ended({})), 'end')
  assert.equal(iconOf(live({})), 'refresh')
  assert.equal(toneOf(live({})), 'accent')
  // A total that is not a count shows what the server stored.
  assert.deepEqual(textOf(live({ total: 0 }), t), { title: 'stored title', body: 'stored body' })
})

test('inbox: the end of an update run gives the counts and the first item that needs attention', async () => {
  const t = await translator('en')
  const out = textOf(ended({ first_name: 'Notes sync', first_from: 'v0.8.1', first_error: 'the new version did not start.' }), t)
  assert.deepEqual(out, { title: '5 updated, 1 needs attention', body: 'Notes sync is still on v0.8.1: the new version did not start.' })
  assert.equal(textOf(ended({ first_name: 'Notes sync', first_from: 'v0.8.1' }), t).body, 'Notes sync is still on v0.8.1.')
  assert.deepEqual(textOf(ended({ updated: 6, failed: 0 }), t), { title: '6 updated', body: 'stored body' })
  assert.equal(textOf(ended({ updated: 1, failed: 0, total: 1, source: 'auto' }), t).title, 'Automatic: 1 updated')
  assert.equal(toneOf(ended({})), 'warn')
  assert.equal(iconOf(ended({})), 'alert-circle')
  assert.equal(toneOf(ended({ failed: 0 })), 'ok')
  assert.equal(iconOf(ended({ failed: 0 })), 'check-circle')
})

test('inbox: the end of an update run keeps the case of the name it starts with', async () => {
  const t = await translator('en')
  const n = ended({ first_name: 'e2e-bad', first_from: '1.0.0', first_error: 'exit code 3' }, { status: 'failed' })
  assert.equal(bodyOf(n, textOf(n, t)), 'e2e-bad is still on 1.0.0: exit code 3.')
  // Any other failure starts with a capital, as before.
  const op = note({ kind: 'system', code: 'updates.opFailed', status: 'failed', title: 'x', body: 'took longer than 15m' })
  assert.equal(bodyOf(op, { title: 'x', body: 'took longer than 15m' }), 'Took longer than 15m')
})

test('inbox: the end of a run whose new build did not start says so before the cause', async () => {
  const t = await translator('en')
  const out = textOf(ended({ first_name: 'e2e-bad', first_from: '1.0.0', first_key: 'start', first_error: 'the backend process exited (exit code 3)' }), t)
  assert.equal(out.body, 'e2e-bad is still on 1.0.0: the new version did not start: the backend process exited (exit code 3).')
})

test('inbox: the end of a cancelled run says it was cancelled, as the Updates page does', async () => {
  const t = await translator('en')
  assert.equal(textOf(ended({ state: 'cancelled', cancelled: 2, updated: 1, failed: 0, total: 3 }), t).title, 'Update run cancelled: 1 updated')
  assert.equal(textOf(ended({ state: 'cancelled', cancelled: 1, updated: 1, failed: 1, total: 3 }), t).title, 'Update run cancelled: 1 updated, 1 needs attention')
  assert.equal(textOf(ended({ state: 'cancelled', updated: 1, failed: 0, total: 3, source: 'auto' }), t).title, 'Automatic: Update run cancelled: 1 updated')
})

test('inbox: both items of an update run lead to the run on the Updates page', () => {
  assert.equal(targetOf(live({}, { link: '/app/updates?run=r1' })), '/app/updates?run=r1')
  assert.equal(targetOf(live({})), '/app/updates?run=r1', 'no stored link: the run id makes one')
  assert.equal(targetOf(ended({ run: 'a b' })), '/app/updates?run=a%20b')
  assert.equal(targetOf(ended({ run: undefined })), '/app/updates')
  // The chats being off does not hide the Updates page.
  assert.equal(targetOf(live({}), false), '/app/updates?run=r1')
})

test('inbox: the update run items are worded in every language', async () => {
  for (const l of LOCALES) {
    const t = await translator(l)
    for (const n of [live({}), live({ updated: 1, running: 1, queued: 1 }), live({ source: 'auto' }), ended({ first_name: 'NAME', first_from: 'v1', first_error: 'why' }), ended({ failed: 0 }), ended({ failed: 0, source: 'auto' })]) {
      const out = textOf(n, t)
      assert.ok(out.title && out.title !== 'stored title' && !out.title.includes('{{'), `${l}: ${out.title}`)
      assert.ok(!out.body.includes('{{'), `${l}: ${out.body}`)
    }
    for (const key of ['inbox.viewProgress', 'inbox.seeRun', 'inbox.openUpdates', 'inbox.updatesNone']) {
      assert.notEqual(t(key), key, `${l}: ${key}`)
    }
    assert.ok(t('inbox.updatesPending', { count: 3 }).includes('3'), l)
  }
})

test('inbox: the lines of a watch are worded from their code, in the language of the person', async () => {
  const line = (code, params) => note({ code: `watch.${code}`, title: 'My calendar', body: 'stored English line', params })
  const en = await translator('en')
  const set = textOf(line('set', { title: 'Dentist', at: '2026-11-26T08:45:00Z', zone: 'UTC' }), en)
  assert.equal(set.title, 'My calendar', 'the watch keeps its name, so its lines group together')
  assert.match(set.body, /^Set the reminder for "Dentist": .*08:45( UTC)?\.$/)
  assert.equal(textOf(line('missed', { title: 'Dentist' }), en).body, 'Missed: Dentist. The server was down at the reminder time.')
  const it = await translator('it')
  assert.equal(textOf(line('cancelled', { title: 'Dentista' }), it).body, 'Promemoria per "Dentista" annullato.')
  const itSet = textOf(line('set', { title: 'Dentista', at: '2026-11-26T08:45:00Z', zone: 'UTC' }), it, 'it').body
  assert.match(itSet, /gio/i, `the time is written in the locale of the page: ${itSet}`)
  assert.equal(textOf(line('moved', { title: 'Dentist' }), en).body, 'stored English line', 'a timed line without its time keeps the stored line')
  assert.equal(textOf(line('unknown', { title: 'Dentist' }), en).body, 'stored English line')
  for (const lng of LOCALES) {
    const t = await translator(lng)
    for (const code of WATCH_CODES) {
      const out = textOf(line(code, { title: 'X', at: '2026-11-26T08:45:00Z' }), t)
      assert.ok(out.body && !out.body.includes('inbox.codes'), `${lng} ${code}: ${out.body}`)
    }
  }
})
