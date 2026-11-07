import { readFileSync } from 'node:fs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { DUPLICATE_CONFIRMATION, duplicateOther, duplicateRefusalName, isDuplicateRefusal, localizeAccess, localizeCapabilities, approvalForm, approvalPayload, approvalProblem, bytesToMib, canSave, connectorForm, connectorPayload, emptyConnector, REPLY_LANGUAGES, fileDirection, fileLimitProblem, formatBytes, mibToBytes, previewLimit, senderLine, listeningChannels, offeredPlaces, placeFor, withPlace } from '../../../internal/connector-ui/src/connector.js'
import { changedParts, droppedActs, removedActs } from '../../../internal/connector-ui/src/access.js'

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
  assert.deepEqual(connectorPayload(base, 'telegram').approval, { mode: 'ask', acts: [], max_pending: 0, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  const auto = connectorPayload({ ...base, approval: { mode: 'auto', max_pending: '3', min_gap_minutes: '', max_runs_per_day: ' 8 ', max_lifetime_days: '14' } }, 'telegram')
  assert.deepEqual(auto.approval, { mode: 'auto', acts: [], max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 8, max_lifetime_days: 14 })
  assert.equal(connectorPayload({ ...base, approval: { mode: 'sometimes' } }, 'telegram').approval.mode, 'ask', 'an unknown mode falls back to ask')
})

test('chat connectors: a saved approval reads back, with a zero limit shown as empty', () => {
  assert.deepEqual(approvalForm({ mode: 'auto_readonly', max_pending: 4, min_gap_minutes: 0 }), { mode: 'auto_readonly', acts: [], max_pending: '4', min_gap_minutes: '', max_runs_per_day: '', max_lifetime_days: '' })
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

test('chat connectors: approval acts round trip, are sent with the mode and limits, and an empty list is sent as []', () => {
  const saved = { mode: 'auto', acts: ['act.spend', 'act.files'], max_pending: 3 }
  const form = approvalForm(saved)
  assert.deepEqual(form.acts, ['act.spend', 'act.files'])
  const out = approvalPayload(form)
  assert.deepEqual(out, { mode: 'auto', acts: ['act.files', 'act.spend'], max_pending: 3, min_gap_minutes: 0, max_runs_per_day: 0, max_lifetime_days: 0 })
  assert.deepEqual(approvalPayload({ ...form, acts: [] }).acts, [])
  assert.deepEqual(approvalPayload({ mode: 'ask' }).acts, [])
  assert.deepEqual(approvalForm({ mode: 'ask' }).acts, [])
})

test('chat connectors: approval acts replace the whole object, so a mode change keeps them, and only the four names are valid', () => {
  const base = { ...connectorForm({ id: 'b', approval: { mode: 'ask', acts: ['act.addons'] } }) }
  const changed = connectorPayload({ ...base, approval: { ...base.approval, mode: 'auto' } }, 'telegram')
  assert.equal(changed.approval.mode, 'auto')
  assert.deepEqual(changed.approval.acts, ['act.addons'])
  assert.deepEqual(approvalPayload({ mode: 'ask', acts: ['act.files', 'act.bogus'] }).acts, ['act.files'])
  assert.equal(approvalProblem({ mode: 'ask', acts: ['act.send'] }), '')
  assert.equal(approvalProblem({ mode: 'ask', acts: ['act.bogus'] }), 'acts')
})

test('chat connectors: a new binding follows the language of the sender', () => {
  assert.equal(emptyConnector().reply_language, 'auto')
  assert.deepEqual(REPLY_LANGUAGES, ['auto', 'en', 'it', 'de', 'es', 'fr', 'pt-BR'])
})

test('chat connectors: the request carries the reply language, and an unknown one becomes auto', () => {
  const base = { ...emptyConnector(), name: 'B', agent_id: 'a' }
  assert.equal(connectorPayload(base, 'telegram').reply_language, 'auto')
  for (const l of REPLY_LANGUAGES) assert.equal(connectorPayload({ ...base, reply_language: l }, 'telegram').reply_language, l)
  assert.equal(connectorPayload({ ...base, reply_language: 'klingon' }, 'telegram').reply_language, 'auto')
  assert.equal(connectorPayload({ ...base, reply_language: undefined }, 'telegram').reply_language, 'auto')
})

test('chat connectors: the form of a saved binding keeps its reply language, and an older one reads as auto', () => {
  assert.equal(connectorForm({ id: 'b1', name: 'B', agent_id: 'a', reply_language: 'de' }).reply_language, 'de')
  assert.equal(connectorForm({ id: 'b1', name: 'B', agent_id: 'a' }).reply_language, 'auto')
})

test('chat connectors: the shared words label every reply language and say what the setting changes', () => {
  const en = JSON.parse(readFileSync(new URL('../../../internal/connector-ui/src/i18n/connector.en.json', import.meta.url), 'utf8'))
  for (const l of REPLY_LANGUAGES) assert.ok(en[`answer.language.${l}`], `no label for ${l}`)
  assert.equal(en['answer.languageLabel'], 'Reply language')
  assert.equal(en['answer.languageHelp'], 'The notes the bot writes itself, like confirmations and refusals. The answers of the model follow the language of the conversation.')
  const src = readFileSync(new URL('../../../internal/connector-ui/src/AnswerFields.jsx', import.meta.url), 'utf8')
  assert.ok(src.includes('data-testid="connector-reply-language"'))
})

test('chat connectors: the host text in the page language replaces the English one, field by field', () => {
  const caps = localizeCapabilities([
    { name: 'a', title: 'Save', description: 'D', language: 'it', localized_title: 'Salva' },
    { name: 'b', title: 'Files', description: 'F' },
  ])
  assert.deepEqual(caps.map((c) => [c.title, c.description]), [['Salva', 'D'], ['Files', 'F']])
  const view = { binding: 'b', subjects: [{ id: 'listed', decisions: [
    { capability: 'a', code: 'x', message: 'English', localized_message: 'Italiano' },
    { capability: 'b', code: 'y', message: 'English only' },
    { capability: 'c', allowed: true },
  ] }] }
  const out = localizeAccess(view)
  assert.deepEqual(out.subjects[0].decisions.map((d) => d.message), ['Italiano', 'English only', undefined])
  assert.equal(out.subjects[0].decisions[0].code, 'x')
  assert.equal(view.subjects[0].decisions[0].message, 'English', 'the input is not changed')
  assert.equal(localizeAccess(null), null)
  assert.deepEqual(localizeCapabilities(undefined), [])
})

test('chat connectors: the host names the other connection of a bot that is connected twice', () => {
  assert.equal(duplicateOther({ id: 'a' }), null)
  assert.equal(duplicateOther({ id: 'a', duplicate: { others: [] } }), null)
  assert.deepEqual(duplicateOther({ id: 'a', duplicate: { code: 'duplicate_connection', others: [{ id: 'b', name: 'Team app' }] } }), { id: 'b', name: 'Team app' })
})

test('chat connectors: only a 409 of the type duplicate_connection is the question about a twin', () => {
  const refusal = (status, type, param) => Object.assign(new Error('x'), { status, body: { error: { type, param } } })
  assert.equal(isDuplicateRefusal(refusal(409, 'duplicate_connection', 'Team app')), true)
  assert.equal(isDuplicateRefusal(refusal(409, 'connectors_off')), false)
  assert.equal(isDuplicateRefusal(refusal(400, 'duplicate_connection')), false)
  assert.equal(isDuplicateRefusal(new Error('network')), false)
  assert.equal(duplicateRefusalName(refusal(409, 'duplicate_connection', 'Team app')), 'Team app')
  assert.equal(duplicateRefusalName(refusal(409, 'duplicate_connection')), '')
})

test('chat connectors: the confirmation of a twin is the text the host expects, and goes with the save only when given', () => {
  assert.equal(DUPLICATE_CONFIRMATION, 'answer twice')
  const body = connectorPayload({ ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }, 'telegram', '1:x', { confirm_duplicate: DUPLICATE_CONFIRMATION })
  assert.equal(body.confirm_duplicate, 'answer twice')
  assert.equal('confirm_duplicate' in connectorPayload({ ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }, 'telegram', '1:x', {}), false)
})

test('chat connectors: the listening channels are the channel entries of the list, none for anyone or a mixed list', () => {
  const isChannel = (x) => x.startsWith('C')
  const form = { allow: ['U1', 'C1', 'C2'], accept_any: false }
  assert.deepEqual(listeningChannels(form, isChannel, false), ['C1', 'C2'])
  assert.deepEqual(listeningChannels({ ...form, accept_any: true }, isChannel, false), [])
  assert.deepEqual(listeningChannels(form, isChannel, true), [])
  assert.deepEqual(listeningChannels({}, isChannel, false), [])
})

test('chat connectors: a channel that is a place is not offered again, and a new place is verified-only', () => {
  const form = { allow: ['C1', 'C2'], channels: [{ id: 'C1', label: 'ops', approved: true }] }
  assert.deepEqual(offeredPlaces(form, ['C1', 'C2']), ['C2'])
  assert.equal(placeFor(form, 'C1').label, 'ops')
  assert.equal(placeFor(form, 'C2'), null)
  const next = withPlace(form, 'C2', 'alerts')
  assert.deepEqual(next.channels, [{ id: 'C1', label: 'ops', approved: true }, { id: 'C2', label: 'alerts', approved: false }])
  assert.equal(withPlace(next, 'C1', 'x'), next, 'a place that exists stays as it is')
  assert.equal(withPlace(form, '', ''), form)
  assert.deepEqual(form.channels, [{ id: 'C1', label: 'ops', approved: true }], 'the form is not changed')
})

test('chat connectors: a save names the parts that changed, in a fixed order', () => {
  const saved = connectorForm({ id: 'b', name: 'Bot', agent_id: 'a1', allow: ['1'], caps: ['memory.write'], approval: { mode: 'ask' } })
  assert.deepEqual(changedParts(saved, saved), [])
  assert.deepEqual(changedParts({ ...saved, caps: ['memory.write', 'files.upload'], name: ' Bot 2 ', allow: ['1', '2'] }, saved), ['name', 'who', 'permissions'])
  assert.deepEqual(changedParts({ ...saved, approval: { ...saved.approval, acts: ['act.files'] } }, saved), ['approval'])
  assert.deepEqual(changedParts(saved, null), [], 'a new connection has no earlier state to compare')
})

test('chat connectors: the acts a save removes, and the triggers that lose them', () => {
  const saved = connectorForm({ id: 'b', approval: { mode: 'auto', acts: ['act.files', 'act.addons'] } })
  const form = { ...saved, approval: { ...saved.approval, acts: ['act.files', 'act.send'] } }
  assert.deepEqual(removedActs(saved, form), ['act.addons'])
  assert.deepEqual(removedActs(null, form), [])
  const triggers = [
    { id: 't1', name: 'Digest', binding: 'b', acts: ['act.files', 'act.addons'] },
    { id: 't2', name: 'Sweep', binding: 'b', acts: ['act.files'] },
    { id: 't3', name: 'Elsewhere', binding: 'c', acts: ['act.addons'] },
    { id: 't4', acts: ['act.addons'] },
  ]
  assert.deepEqual(droppedActs(triggers, 'b', ['act.addons']), [{ id: 't1', name: 'Digest', acts: ['act.addons'] }])
  assert.deepEqual(droppedActs(triggers, 'b', []), [])
  assert.deepEqual(droppedActs(null, 'b', ['act.addons']), [])
})
