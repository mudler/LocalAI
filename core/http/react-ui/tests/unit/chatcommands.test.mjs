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

test('typed safety: any actual addon host warns and unknown never appears isolated', async () => {
  const { commandsState } = await import('../../src/utils/chatCommands.js')
  assert.equal(commandsState({ commands: 'available', commands_provider: 'renamed', commands_isolation: 'host' }).state, 'provider-host')
  assert.equal(commandsState({ commands: 'available', commands_provider: 'renamed' }).state, 'unknown')
  assert.equal(commandsNotice('unknown').tone, 'warn')
  assert.equal(commandsNotice('switched_off').textKey, 'disabled')
})

test('typed safety: dismissal tracks binding, observation, chat and actor epoch', async () => {
  const { noticeIdentity } = await import('../../src/utils/chatCommands.js')
  const base = { commands: 'available', commands_provider: 'renamed', execution_status: { readiness: 'available', isolation: 'host', generation: '1', incarnation: 'a', selection_revision: 'r1' } }
  const id = noticeIdentity(base, 'chat', 'actor-session')
  for (const field of ['generation', 'incarnation', 'selection_revision', 'isolation', 'readiness']) {
    assert.notEqual(noticeIdentity({ ...base, execution_status: { ...base.execution_status, [field]: 'changed' } }, 'chat', 'actor-session'), id)
  }
  assert.notEqual(noticeIdentity(base, 'other', 'actor-session'), id)
  assert.notEqual(noticeIdentity(base, 'chat', 'new-session'), id)
})

test('typed safety: explanation is bounded plain data with no install advice', async () => {
  const { commandsState } = await import('../../src/utils/chatCommands.js')
  const state = commandsState({ commands: 'unavailable', commands_provider: 'renamed', execution_status: { readiness: 'unknown', isolation: 'unknown', explanation: '<img src=x onerror=alert(1)>'.repeat(30) } })
  const notice = commandsNotice(state.state, { reason: state.reason })
  assert.equal(notice.link, null)
  assert.equal(notice.askKey, null)
  assert.ok([...notice.reason].length <= 200)
  assert.equal(typeof notice.reason, 'string')
})

test('typed safety: actual notice hook resets dismissal through loading, identity changes and logout', async () => {
  const { readFileSync } = await import('node:fs')
  const source = readFileSync(new URL('../../src/hooks/useCommandsNotice.js', import.meta.url), 'utf8').replace(/^import .*\n/gm, '').replace('export function', 'function')
  let hidden = '', previous, effect
  const hook = new Function('useState', 'useEffect', 'useCallback', 'commandsNotice', `${source}; return useCommandsNotice`)(
    () => [hidden, v => { hidden = v }],
    (fn, deps) => { if (previous !== deps[0]) { effect = fn; previous = deps[0] } },
    fn => fn, commandsNotice)
  const render = (state, identity = 'binding-1', actor = 'session-1') => {
    const value = hook({ state, reason: '', identity }, false, [actor, 'chat'])
    if (effect) { effect(); effect = null }
    return value
  }
  render('provider-host').hide()
  assert.equal(render('provider-host').hidden, true)
  assert.equal(render('unknown', '').hidden, false)
  assert.equal(render('provider-host').hidden, false, 'loading invalidates even the same binding')
  render('provider-host').hide()
  assert.equal(render('provider-host', 'binding-2').hidden, false)
  assert.equal(render('provider-host', 'binding-1').hidden, false, 'replacement ABA does not restore dismissal')
  render('provider-host').hide()
  assert.equal(render('provider-host', 'binding-1', 'logged-out').hidden, false)
})

test('typed safety: actual explanation JSX escapes provider markup as React text', async () => {
  const { readFileSync } = await import('node:fs')
  const React = await import('react')
  const { renderToStaticMarkup } = await import('react-dom/server')
  const { transformWithOxc } = await import('vite')
  const source = readFileSync(new URL('../../src/components/chat/CommandsNotice.jsx', import.meta.url), 'utf8')
  assert.ok(!source.includes('dangerouslySetInnerHTML'))
  const jsx = source.slice(source.indexOf('<p>{info.reason}</p>'), source.indexOf('<p>{info.reason}</p>') + '<p>{info.reason}</p>'.length)
  const { code } = await transformWithOxc(`return (${jsx})`, 'notice.jsx', { jsx: { runtime: 'classic' } })
  const element = new Function('React', 'info', code)(React, { reason: '<img src=x onerror=alert(1)>' })
  const html = renderToStaticMarkup(element)
  assert.match(html, /&lt;img/)
  assert.ok(!html.includes('<img'))
})

test('typed safety: nonavailable and inaccessible observations never suggest installing Sandbox', async () => {
  const { commandsState } = await import('../../src/utils/chatCommands.js')
  for (const commands of ['none', 'unavailable', 'available']) {
    const projected = commandsState({ commands, execution_status: { readiness: 'unknown', isolation: 'isolated' } })
    const notice = commandsNotice(projected.state, { isAdmin: true })
    assert.ok(notice)
    assert.equal(notice.link, null)
    assert.equal(notice.askKey, null)
  }
})
