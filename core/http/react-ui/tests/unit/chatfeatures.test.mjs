import test from 'node:test'
import assert from 'node:assert/strict'
import { contextReading, settingsBody, settingsChanged, settingsErrors, settingsForm, tokenCount } from '../../src/utils/chatSettings.js'
import { changeValue, changeView, proposalEdit, proposalIds, proposalView, receiptWhen, triggerCards } from '../../src/utils/chatProposal.js'
import { BROWSER_CHATS_KEY, browserChatStores, importBrowserChats } from '../../src/utils/browserChats.js'
import { toolRefusal } from '../../src/utils/addonChange.js'

function memoryStorage(entries) {
  const map = new Map(Object.entries(entries))
  return {
    get length() { return map.size },
    key: (i) => [...map.keys()][i] ?? null,
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    has: (k) => map.has(k),
  }
}

test('chat settings: the form reads the stored settings and an empty field means the model default', () => {
  assert.deepEqual(settingsForm(undefined), { system_prompt: '', temperature: '', top_p: '', top_k: '', reasoning_effort: '', context_size: '' })
  const f = settingsForm({ system_prompt: 'Be brief', temperature: 0.2, top_k: 40, context_size: 8192, reasoning_effort: 'low' })
  assert.equal(f.temperature, '0.2')
  assert.equal(f.context_size, '8192')
  assert.deepEqual(settingsBody(f), { system_prompt: 'Be brief', temperature: 0.2, top_k: 40, context_size: 8192, reasoning_effort: 'low' })
  assert.equal(settingsChanged(f, { system_prompt: 'Be brief', temperature: 0.2, top_k: 40, context_size: 8192, reasoning_effort: 'low' }), false)
  assert.equal(settingsChanged({ ...f, top_p: '0.9' }, { system_prompt: 'Be brief' }), true)
})

test('chat settings: the form refuses what the server refuses', () => {
  const base = settingsForm({})
  assert.deepEqual(settingsErrors(base), {})
  assert.equal(settingsErrors({ ...base, temperature: '2.5' }).temperature, 'temperature')
  assert.equal(settingsErrors({ ...base, top_p: '0' }).top_p, 'topP')
  assert.equal(settingsErrors({ ...base, top_k: '1.5' }).top_k, 'topK')
  assert.equal(settingsErrors({ ...base, context_size: '100' }).context_size, 'contextSize')
  assert.equal(settingsErrors({ ...base, reasoning_effort: 'max' }).reasoning_effort, 'effort')
  assert.deepEqual(settingsErrors({ ...base, temperature: '0', top_p: '1', top_k: '0', context_size: '512' }), {})
})

test('chat settings: the context meter reads the usage event and warns near the limit', () => {
  assert.equal(contextReading(null), null)
  assert.deepEqual(contextReading({ used: 100, limit: 4096, completion: 10, tokens_per_second: 20 }), { used: 100, limit: 4096, pct: 2, left: 3996, tone: 'ok', speed: 20 })
  assert.equal(contextReading({ used: 3400, limit: 4096 }).tone, 'warn')
  assert.equal(contextReading({ used: 4000, limit: 4096 }).tone, 'err')
  const unknown = contextReading({ used: 900, completion: 3 })
  assert.equal(unknown.pct, null)
  assert.equal(unknown.left, null)
  // Past the window nothing is left, not a negative count.
  assert.equal(contextReading({ used: 5000, limit: 4096 }).left, 0)
  assert.equal(unknown.speed, null)
  assert.equal(tokenCount(950), '950')
  assert.equal(tokenCount(4096), '4.1k')
  assert.equal(tokenCount(131072), '131k')
})

test('trigger proposals: the card says proposed, created (same id) or gone, and an edit sends every field back', () => {
  const p = { id: 'p1', name: 'Digest', prompt: 'Sum up', kind: 'schedule', schedule: 'daily 08:00', time_zone: 'Europe/Rome', daily_cap: 3, model: 'm', source_chat_id: 'c1' }
  const waiting = proposalView({ state: 'pending', trigger: p })
  assert.equal(waiting.kind, 'pending')
  assert.equal(waiting.trigger, p)
  const done = proposalView({ state: 'accepted', trigger: { ...p, enabled: true }, can_undo: true, answered_at: '2026-10-30T09:00:00Z' })
  assert.equal(done.kind, 'accepted')
  assert.equal(done.canUndo, true)
  assert.equal(done.blocked, '')
  const ran = proposalView({ state: 'accepted', trigger: p, can_undo: false, undo_blocked: 'ran' })
  assert.deepEqual([ran.canUndo, ran.blocked], [false, 'ran'])
  assert.equal(proposalView({ state: 'undone', trigger: p }).kind, 'undone')
  assert.equal(proposalView({ state: 'declined', trigger: p }).kind, 'declined')
  assert.equal(proposalView(null, Object.assign(new Error('nope'), { status: 404 })).kind, 'removed')
  const failed = proposalView(null, Object.assign(new Error('boom'), { status: 500 }))
  assert.deepEqual([failed.kind, failed.message], ['error', 'boom'])
  assert.equal(proposalView({ state: 'mystery', trigger: p }).kind, 'error', 'an unknown state is not guessed')
  assert.deepEqual(receiptWhen({ kind: 'schedule', summary: 'Every Monday at 09:00 (Europe/Rome)' }), { mode: 'schedule', text: 'Every Monday at 09:00 (Europe/Rome)' })
  assert.deepEqual(receiptWhen({ kind: 'file', summary: 'new files in /Contracts' }), { mode: 'file', text: 'new files in /Contracts' })
  assert.deepEqual(receiptWhen({ kind: 'schedule', schedule: 'daily 08:00' }), { mode: 'schedule', text: 'daily 08:00' })
  assert.deepEqual(proposalEdit(p, { name: ' News ', when: 'weekdays 09:30' }), {
    name: 'News', prompt: 'Sum up', kind: 'schedule', enabled: false, daily_cap: 3, schedule: 'weekdays 09:30', time_zone: 'Europe/Rome', model: 'm', source_chat_id: 'c1',
  })
  const f = { id: 'p2', name: 'Inbox', prompt: 'Read', kind: 'file', folder: '/In', quiet_seconds: 30 }
  assert.deepEqual(proposalEdit(f, { name: 'Inbox', when: '/Contracts' }), { name: 'Inbox', prompt: 'Read', kind: 'file', enabled: false, daily_cap: 0, folder: '/Contracts', quiet_seconds: 30 })
  assert.deepEqual(proposalIds([
    { role: 'tool', card: { kind: 'trigger_proposal', id: 'p1' } },
    { role: 'tool', card: { kind: 'mcp_app', id: 'x' } },
    { role: 'reasoning' },
  ]), ['p1'])
})

test('trigger changes: the card reads the state the server keeps and the cards keep their order', () => {
  assert.deepEqual(changeView(null, { status: 404, message: 'x' }), { kind: 'removed' })
  assert.equal(changeView(null, { status: 500, message: 'boom' }).message, 'boom')
  assert.equal(changeView({ state: 'odd' }).kind, 'error')
  const v = changeView({ state: 'applied', op: 'pause', before: { name: 'N', enabled: true }, after: { name: 'N', enabled: false }, fields: ['enabled'], can_undo: true })
  assert.equal(v.kind, 'applied')
  assert.equal(v.op, 'pause')
  assert.equal(v.canUndo, true)
  assert.deepEqual(v.fields, ['enabled'])
  assert.equal(changeView({ state: 'pending', op: 'delete', before: {} }).after, null)
  assert.equal(changeValue('when', { summary: 'Every day at 08:00' }), 'Every day at 08:00')
  assert.equal(changeValue('enabled', { enabled: false }), 'off')
  assert.equal(changeValue('writes', { plan: { writes: 'data/a.json' } }), 'data/a.json')
  assert.equal(changeValue('estimated_tokens', { cost: {} }), '')
  assert.equal(changeValue('name', { name: 'X' }), 'X')
  assert.deepEqual(triggerCards([
    { role: 'tool', card: { kind: 'trigger_change', id: 'c1' } },
    { role: 'tool', card: { kind: 'mcp_app', id: 'x' } },
    { role: 'tool', card: { kind: 'trigger_proposal', id: 'p1' } },
  ]), [{ kind: 'trigger_change', id: 'c1' }, { kind: 'trigger_proposal', id: 'p1' }])
})

test('browser chats: every store of the old page is found, and one is cleared only when nothing failed', async () => {
  const st = memoryStorage({
    [BROWSER_CHATS_KEY]: JSON.stringify({ chats: [{ id: 1 }, { id: 2 }] }),
    [`${BROWSER_CHATS_KEY}:alice`]: JSON.stringify({ chats: [{ id: 3 }] }),
    [`${BROWSER_CHATS_KEY}:empty`]: JSON.stringify({ chats: [] }),
    other: '{}',
    [`${BROWSER_CHATS_KEY}:bad`]: 'not json',
  })
  const stores = browserChatStores(st)
  assert.deepEqual(stores.map(s => [s.key, s.count]), [[BROWSER_CHATS_KEY, 2], [`${BROWSER_CHATS_KEY}:alice`, 1]])
  const sent = []
  const total = await importBrowserChats(st, stores, async (raw) => {
    sent.push(raw)
    return sent.length === 1 ? { imported: 2, skipped: 0, failed: 0 } : { imported: 0, skipped: 0, failed: 1 }
  })
  assert.deepEqual(total, { imported: 2, skipped: 0, failed: 1 })
  assert.equal(sent[0], stores[0].raw)
  assert.equal(st.has(BROWSER_CHATS_KEY), false)
  assert.equal(st.has(`${BROWSER_CHATS_KEY}:alice`), true)
  assert.deepEqual(browserChatStores(null), [])
})

test('tool refusals: the typed codes of the add-on tools are read, an untyped error is not', () => {
  const line = (doc) => `addon__tables__write: rows\n${JSON.stringify(doc)}`
  assert.deepEqual(toolRefusal(line({ kind: 'error', code: 'not_permitted', note: 'x' })), { code: 'not_permitted', addonId: 'tables', message: '' })
  assert.equal(toolRefusal(line({ kind: 'error', code: 'resource_unreachable' })).code, 'resource_unreachable')
  assert.equal(toolRefusal(line({ kind: 'error', code: 'not_running' })).code, 'not_running')
  assert.equal(toolRefusal(line({ kind: 'error', code: 'invalid_request' })), null)
  assert.equal(toolRefusal(line({ kind: 'error', code: 'invalid_request', message: 'row 3 has no id' })).message, 'row 3 has no id')
  assert.equal(toolRefusal(line({ kind: 'error', code: 'select_resource' })), null)
  assert.equal(toolRefusal(line({ kind: 'result' })), null)
  assert.equal(toolRefusal('read_file: /a\nplain text'), null)
})

test('sandbox state: the Tools picker reads isolated, host, cannot-run or unknown from the status read', async () => {
  const { sandboxState } = await import('../../src/utils/sandboxStatus.js')
  assert.deepEqual(sandboxState({ ok: true, canRun: true, active: 'gvisor' }), { state: 'isolated', backend: 'gvisor', reason: '' })
  assert.equal(sandboxState({ ok: true, canRun: true, active: 'host', hostNotice: true }).state, 'host')
  assert.deepEqual(sandboxState({ ok: true, canRun: false, backends: [{ name: 'kvm', usable: false, reason: 'no /dev/kvm' }] }), { state: 'cannot', backend: '', reason: 'no /dev/kvm' })
  assert.equal(sandboxState(null).state, 'unknown')
  assert.equal(sandboxState({ ok: false }).state, 'unknown')
})

test('home actions: an administrator without Studio gets Install Studio under the same command', async () => {
  const { availableActions } = await import('../../src/components/home/homeActions.js')
  const ids = (ctx) => availableActions(ctx).filter(a => a.cmd === '/studio').map(a => a.id)
  assert.deepEqual(ids({ isAdmin: true, studioInstalled: false }), ['studioInstall'])
  assert.deepEqual(ids({ isAdmin: true, studioInstalled: true }), ['studio'])
  assert.deepEqual(ids({ isAdmin: false, studioInstalled: false }), [])
})
