import test from 'node:test'
import assert from 'node:assert/strict'
import { approvalForm, approvalPayload, approvalProblem, bytesToMib, canSave, connectorForm, connectorPayload, emptyConnector, fileDirection, fileLimitProblem, formatBytes, mibToBytes, modeText, previewLimit, senderLine } from '../../../internal/connector-ui/src/connector.js'

test('chat connectors: the token goes only when it was typed', () => {
  const create = connectorPayload({ ...emptyConnector(), name: ' Bot ', token: ' 1:abc ', agent_id: 'a1', allow: ['11', ' 11', '@ada_l', ''] }, 'telegram')
  assert.equal(create.token, '1:abc')
  assert.deepEqual(create.allow, ['11', '@ada_l'])
  assert.equal(create.name, 'Bot')
  assert.equal(create.kind, 'telegram')
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Bot', agent_id: 'a1' }, 'telegram')
  assert.equal('token' in update, false)
})

test('chat connectors: accept any drops the allowlist from the request', () => {
  const body = connectorPayload({ ...emptyConnector(), name: 'B', agent_id: 'a', accept_any: true, allow: ['11'] }, 'telegram')
  assert.equal(body.accept_any, true)
  assert.deepEqual(body.allow, [])
})

test('chat connectors: who a binding accepts, in words', () => {
  assert.equal(modeText({ accept_any: true }), 'anyone')
  assert.equal(modeText({ allow: [] }), 'nobody yet')
  assert.equal(modeText({ allow: ['1'] }), '1 listed sender')
  assert.equal(modeText({ allow: ['1', '2'] }), '2 listed senders')
})

test('chat connectors: a sender line names the person, the count and the id', () => {
  assert.equal(senderLine({ name: 'Ada', count: 1, id: '11' }), 'Ada · 1 message · 11')
  assert.equal(senderLine({ name: '', count: 3, id: '12' }), '12 · 3 messages · 12')
})

test('chat connectors: saving needs a name, an agent, a token for a new binding, and the warning confirmed for accept any', () => {
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }
  assert.equal(canSave(ok, false), true)
  assert.equal(canSave({ ...ok, name: ' ' }, false), false)
  assert.equal(canSave({ ...ok, agent_id: '' }, false), false)
  assert.equal(canSave({ ...ok, token: '' }, false), false)
  assert.equal(canSave({ ...ok, token: '', id: 'b1' }, false), true)
  assert.equal(canSave({ ...ok, accept_any: true }, false), false)
  assert.equal(canSave({ ...ok, accept_any: true }, true), true)
})

test('chat connectors: a new binding shows progress, one chat per person and sends files', () => {
  const c = emptyConnector()
  assert.equal(c.reply_mode, 'progress')
  assert.equal(c.conversation, 'sender')
  assert.equal(c.send_files, true)
})

test('chat connectors: the request carries the reply mode, the conversation mode and the file switch', () => {
  const base = { ...emptyConnector(), name: 'B', agent_id: 'a' }
  const off = connectorPayload({ ...base, reply_mode: 'stream', conversation: 'thread', send_files: false, window_minutes: 30 }, 'telegram')
  assert.equal(off.reply_mode, 'stream')
  assert.equal(off.conversation, 'thread')
  assert.equal(off.send_files, false)
  assert.equal('window_minutes' in off, false, 'the window goes only with the window mode')
  const win = connectorPayload({ ...base, conversation: 'window', window_minutes: '45' }, 'telegram')
  assert.equal(win.window_minutes, 45)
  assert.equal(win.send_files, true)
})

test('chat connectors: a window needs 1 to 10080 minutes', () => {
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x', conversation: 'window', window_minutes: 30 }
  assert.equal(canSave(ok, false), true)
  assert.equal(canSave({ ...ok, window_minutes: 0 }, false), false)
  assert.equal(canSave({ ...ok, window_minutes: 20000 }, false), false)
})

test('chat connectors: the request carries the choices of the kind and no thread setting', () => {
  const c = emptyConnector()
  assert.equal('thread_context' in c, false)
  assert.deepEqual(c.options, {})
  const base = { ...c, name: 'B', agent_id: 'a' }
  const body = connectorPayload({ ...base, options: { followThreads: 'any', empty: '' } }, 'slack')
  assert.equal('thread_context' in body, false)
  assert.deepEqual(body.options, { followThreads: 'any' }, 'an empty choice is left out')
  assert.deepEqual(connectorPayload({ ...base, options: undefined }, 'slack').options, {})
})

test('chat connectors: file limits are typed in MiB and sent in bytes, and empty means none', () => {
  const base = { ...emptyConnector(), name: 'B', agent_id: 'a' }
  const none = connectorPayload(base, 'telegram')
  assert.equal(none.file_receive_max_bytes, 0)
  assert.equal(none.file_send_max_bytes, 0)
  const set = connectorPayload({ ...base, file_receive_mib: '0.5', file_send_mib: '2' }, 'telegram')
  assert.equal(set.file_receive_max_bytes, 512 * 1024)
  assert.equal(set.file_send_max_bytes, 2 * 1024 * 1024)
  assert.equal(mibToBytes(' '), 0)
  assert.equal(mibToBytes('abc'), 0)
})

test('chat connectors: a saved binding opens with its limits in MiB', () => {
  const form = connectorForm({ id: 'b1', name: 'B', file_receive_max_bytes: 0, file_send_max_bytes: 262144, token: 'x' })
  assert.equal(form.file_receive_mib, '')
  assert.equal(form.file_send_mib, '0.25')
  assert.equal(form.token, '')
  assert.equal(bytesToMib(1536 * 1024), '1.5')
})

test('chat connectors: a limit above what the kind allows, or not a positive number, cannot be saved', () => {
  const allowed = 1024 * 1024
  assert.equal(fileLimitProblem('', allowed), '')
  assert.equal(fileLimitProblem('1', allowed), '')
  assert.equal(fileLimitProblem('1.5', allowed), 'tooHigh')
  assert.equal(fileLimitProblem('0', allowed), 'invalid')
  assert.equal(fileLimitProblem('-2', allowed), 'invalid')
  assert.equal(fileLimitProblem('x', allowed), 'invalid')
  assert.equal(fileLimitProblem('50', 0), '', 'the server checks when the page does not know the limit')
  const files = { receive: { allowed_bytes: allowed }, send: { allowed_bytes: allowed } }
  const form = { ...emptyConnector(), name: 'B', agent_id: 'a', token: 't' }
  assert.equal(canSave({ ...form, file_send_mib: '0.5' }, false, true, files), true)
  assert.equal(canSave({ ...form, file_send_mib: '2' }, false, true, files), false)
})

test('chat connectors: a typed limit can only lower the limit in effect, and names its layer', () => {
  const base = { effective_bytes: 1024 * 1024, layer: 'addon' }
  assert.deepEqual(previewLimit(base, ''), { bytes: 1024 * 1024, layer: 'addon' })
  assert.deepEqual(previewLimit(base, '0.25'), { bytes: 256 * 1024, layer: 'owner' })
  assert.deepEqual(previewLimit(base, '5'), { bytes: 1024 * 1024, layer: 'addon' }, 'a higher number changes nothing')
  assert.equal(previewLimit(null, '1'), null)
})

test('chat connectors: the page tells what the add-on declares for a direction', () => {
  assert.equal(fileDirection(null), 'unknown')
  assert.equal(fileDirection({ effective_bytes: 0, allowed_bytes: 0, declared_bytes: 0, layer: 'addon' }), 'off')
  assert.equal(fileDirection({ effective_bytes: 100, allowed_bytes: 100, declared_bytes: 0, layer: 'host' }), 'open')
  assert.equal(fileDirection({ effective_bytes: 50, allowed_bytes: 50, declared_bytes: 50, layer: 'addon' }), 'declared')
})

test('chat connectors: sizes are written in KiB and MiB', () => {
  assert.equal(formatBytes(900), '900 bytes')
  assert.equal(formatBytes(1), '1 byte')
  assert.equal(formatBytes(512 * 1024), '512 KiB')
  assert.equal(formatBytes(1024 * 1024), '1 MiB')
  assert.equal(formatBytes(5.5 * 1024 * 1024), '5.5 MiB')
})

test('chat connectors: a saved limit reads back to the same number of bytes', () => {
  for (const bytes of [1, 1000, 100 * 1024, 256 * 1024, 5 * 1024 * 1024 + 7]) assert.equal(mibToBytes(bytesToMib(bytes)), bytes)
})

test('chat connectors: the approval of chat triggers asks by default and sends its limits as numbers', () => {
  const base = { ...emptyConnector(), name: 'B', agent_id: 'a' }
  assert.deepEqual(connectorPayload(base, 'telegram').approval, { mode: 'ask', max_pending: 0, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  const auto = connectorPayload({ ...base, approval: { mode: 'auto', max_pending: '3', min_gap_minutes: '', max_runs_per_day: ' 8 ', max_lifetime_days: '14' } }, 'telegram')
  assert.deepEqual(auto.approval, { mode: 'auto', max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 8, max_lifetime_days: 14 })
  assert.equal(connectorPayload({ ...base, approval: { mode: 'sometimes' } }, 'telegram').approval.mode, 'ask', 'an unknown mode falls back to ask')
})

test('chat connectors: a saved approval reads back, with a zero limit shown as empty', () => {
  assert.deepEqual(approvalForm({ mode: 'auto_readonly', max_pending: 4, min_gap_minutes: 0 }), { mode: 'auto_readonly', max_pending: '4', min_gap_minutes: '', max_runs_per_day: '', max_lifetime_days: '' })
  assert.equal(connectorForm({ id: 'b' }).approval.mode, 'ask')
})

test('chat connectors: an approval limit is a whole number within its bound, or empty', () => {
  assert.equal(approvalProblem({ mode: 'auto', max_pending: '5' }), '')
  assert.equal(approvalProblem({ mode: 'auto', max_pending: '51' }), 'max_pending')
  assert.equal(approvalProblem({ mode: 'auto', max_runs_per_day: '2.5' }), 'max_runs_per_day')
  assert.equal(approvalProblem({ mode: 'auto', max_lifetime_days: '0' }), 'max_lifetime_days')
  assert.equal(approvalProblem({ mode: 'bad' }), 'mode')
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }
  assert.equal(canSave({ ...ok, approval: { mode: 'auto', max_pending: '500' } }, false), false)
  assert.equal(canSave(ok, false), true)
})
