import test from 'node:test'
import assert from 'node:assert/strict'
import { commandsNotice, readHidden, writeHidden, DISMISS_KEY, consentExpired } from '../../src/utils/chatCommands.js'

test('chat commands: no notice when commands run or the server does not say', () => {
  assert.equal(commandsNotice('available', { isAdmin: true }), null)
  assert.equal(commandsNotice('', { isAdmin: true }), null)
  assert.equal(commandsNotice(undefined), null)
  assert.equal(commandsNotice('something-new', { isAdmin: true }), null)
})

test('chat commands: with no Sandbox an administrator gets the install link and others are told to ask', () => {
  assert.deepEqual(commandsNotice('none', { isAdmin: true }), {
    state: 'none', textKey: 'none', reason: '', link: { to: '/app/addons?tab=browse&q=sandbox', labelKey: 'install' }, askKey: null,
  })
  assert.deepEqual(commandsNotice('none', { isAdmin: false }), {
    state: 'none', textKey: 'none', reason: '', link: null, askKey: 'askInstall',
  })
})

test('chat commands: a Sandbox that cannot run gives its reason and the add-on page to an administrator', () => {
  const admin = commandsNotice('unavailable', { isAdmin: true, reason: 'no sandbox backend works on this host' })
  assert.equal(admin.textKey, 'unavailableWhy')
  assert.equal(admin.reason, 'no sandbox backend works on this host')
  assert.deepEqual(admin.link, { to: '/app/addons?detail=sandbox', labelKey: 'openSandbox' })
  const user = commandsNotice('unavailable', { isAdmin: false })
  assert.equal(user.textKey, 'unavailable')
  assert.equal(user.link, null)
  assert.equal(user.askKey, 'askFix')
  assert.equal(commandsNotice('unavailable', { reason: 'x'.repeat(500) }).reason.length, 200)
})

test('chat commands: a Sandbox switched off in the chat is the user\'s own fix, with no link', () => {
  assert.deepEqual(commandsNotice('off', { isAdmin: false }), { state: 'off', textKey: 'off', link: null, askKey: null, reason: '' })
})

test('chat commands: hiding remembers the state, and storage that throws is ignored', () => {
  const box = new Map()
  const storage = { getItem: k => box.get(k) ?? null, setItem: (k, v) => box.set(k, v), removeItem: k => box.delete(k) }
  assert.equal(readHidden(storage), '')
  writeHidden(storage, 'none')
  assert.equal(box.get(DISMISS_KEY), 'none')
  assert.equal(readHidden(storage), 'none')
  writeHidden(storage, '')
  assert.equal(readHidden(storage), '')
  const broken = { getItem() { throw new Error('off') }, setItem() { throw new Error('off') }, removeItem() { throw new Error('off') } }
  assert.equal(readHidden(broken), '')
  assert.doesNotThrow(() => writeHidden(broken, 'none'))
  assert.equal(readHidden(null), '')
})

test('chat commands: the built-in bash on this machine is a state of its own', async () => {
  const { commandsState } = await import('../../src/utils/chatCommands.js')
  assert.deepEqual(commandsState({ commands: 'available', commands_provider: 'sandbox', commands_isolation: 'isolated' }), { state: 'available', reason: '' })
  assert.deepEqual(commandsState({ commands: 'available', commands_provider: 'builtin' }), { state: 'host', reason: '' })
  assert.deepEqual(commandsState({ commands: 'available', commands_provider: 'builtin', commands_fallback: 'sandbox', commands_fallback_reason: 'off' }), { state: 'host-off', reason: '' })
  assert.deepEqual(commandsState({ commands: 'available', commands_provider: 'builtin', commands_fallback: 'sandbox', commands_fallback_reason: 'no backend works' }), { state: 'host-unavailable', reason: 'no backend works' })
  assert.deepEqual(commandsState({ commands: 'none' }), { state: 'none', reason: '' })
  assert.deepEqual(commandsState(undefined), { state: '', reason: '' })
})

test('chat commands: a chat that runs commands here keeps a warning notice with the install link', async () => {
  const { SANDBOX_INSTALL_LINK } = await import('../../src/utils/chatCommands.js')
  const admin = commandsNotice('host', { isAdmin: true })
  assert.equal(admin.titleKey, 'hostTitle')
  assert.equal(admin.textKey, 'host')
  assert.equal(admin.tone, 'warn')
  assert.deepEqual(admin.link, { to: SANDBOX_INSTALL_LINK, labelKey: 'install' })
  assert.equal(commandsNotice('host', { isAdmin: false }).askKey, 'askInstall')
  assert.equal(commandsNotice('host-off', { isAdmin: true }).link, null, 'the person switches the Sandbox on in the menu')
  const why = commandsNotice('host-unavailable', { isAdmin: true, reason: 'no backend works' })
  assert.equal(why.textKey, 'hostUnavailableWhy')
  assert.deepEqual(why.link, { to: '/app/addons?detail=sandbox', labelKey: 'openSandbox' })
})

test('chat commands: the tool line says who served a call', async () => {
  const { providerTag } = await import('../../src/utils/chatCommands.js')
  assert.deepEqual(providerTag({ kind: 'tool_provider', id: 'sandbox' }), { key: 'sandbox' })
  assert.deepEqual(providerTag({ kind: 'tool_provider', id: 'other' }), { key: 'addon', values: { name: 'other' } })
  assert.deepEqual(providerTag({ kind: 'host_exec', id: 'ran' }), { key: 'host' })
  assert.deepEqual(providerTag({ kind: 'host_exec', id: 'once' }), { key: 'hostOnce' })
  assert.deepEqual(providerTag({ kind: 'host_exec', id: 'denied' }), { key: 'hostDenied' })
  assert.equal(providerTag({ kind: 'files_change', id: '1:/a' }), null)
  assert.equal(providerTag(null), null)
})

test('chat commands: a refused answer reads as an expired card only for 410 consent_expired', () => {
  assert.equal(consentExpired({ status: 410 }), true)
  assert.equal(consentExpired({ status: 500, body: { error: { type: 'consent_expired' } } }), true)
  assert.equal(consentExpired({ status: 403, body: { error: { type: 'authorization_error' } } }), false)
  assert.equal(consentExpired(new Error('network')), false)
  assert.equal(consentExpired(undefined), false)
})
