import test from 'node:test'
import assert from 'node:assert/strict'
import { commandsNotice, readHidden, writeHidden, DISMISS_KEY } from '../../src/utils/chatCommands.js'

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
