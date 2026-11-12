import test from 'node:test'
import assert from 'node:assert/strict'
import { commandsNotice, readHidden, writeHidden, DISMISS_KEY, consentExpired } from '../../src/utils/chatCommands.js'

test('chat commands: no notice when commands run or the server does not say', () => {
  assert.equal(commandsNotice('available', { isAdmin: true }), null)
  assert.equal(commandsNotice('', { isAdmin: true }), null)
  assert.equal(commandsNotice(undefined), null)
  assert.equal(commandsNotice('something-new', { isAdmin: true }), null)
})

test('chat commands: with no provider there is no specific installation recommendation', () => {
  assert.deepEqual(commandsNotice('none', { isAdmin: true }), {
    state: 'none', textKey: 'none', reason: '', link: null, askKey: null,
  })
  assert.deepEqual(commandsNotice('none', { isAdmin: false }), {
    state: 'none', textKey: 'none', reason: '', link: null, askKey: null,
  })
})

test('chat commands: an unavailable provider gives its reason without a specific add-on recommendation', () => {
  const admin = commandsNotice('unavailable', { isAdmin: true, reason: 'no sandbox backend works on this host' })
  assert.equal(admin.textKey, 'providerUnavailable')
  assert.equal(admin.reason, 'no sandbox backend works on this host')
  assert.equal(admin.link, null)
  const user = commandsNotice('unavailable', { isAdmin: false })
  assert.equal(user.textKey, 'providerUnavailable')
  assert.equal(user.link, null)
  assert.equal(user.askKey, 'askFix')
  assert.equal(commandsNotice('unavailable', { reason: 'x'.repeat(500) }).reason.length, 200)
})

test('chat commands: a Sandbox switched off in the chat is the user\'s own fix, with no link', () => {
  assert.deepEqual(commandsNotice('off', { isAdmin: false }), { state: 'off', textKey: 'disabled', link: null, askKey: null, reason: '' })
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

test('chat commands: actual host execution keeps a generic isolation warning', () => {
  for (const state of ['host', 'host-off', 'host-unavailable']) {
    const info = commandsNotice(state, { isAdmin: true, reason: 'no backend works' })
    assert.equal(info.titleKey, 'hostTitle')
    assert.equal(info.textKey, 'providerHost')
    assert.equal(info.tone, 'warn')
    assert.equal(info.link, null)
    assert.equal(info.askKey, null)
  }
})

test('chat commands: the tool line says who served a call', async () => {
  const { providerTag } = await import('../../src/utils/chatCommands.js')
  assert.deepEqual(providerTag({ kind: 'tool_provider', id: 'sandbox' }), { key: 'addon', values: { name: 'sandbox' } })
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

test('typed safety: dismissal tracks binding, status, chat and actor epoch', async () => {
  const { noticeIdentity } = await import('../../src/utils/chatCommands.js')
  const base = { commands: 'available', commands_provider: 'renamed', execution_status: { readiness: 'available', isolation: 'host', generation: '1', incarnation: 'a' } }
  const id = noticeIdentity(base, 'chat', 'actor-session')
  for (const field of ['generation', 'incarnation', 'isolation', 'readiness']) {
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

test('typed safety: builtin and all fallback notices have no Sandbox advice for any role', async () => {
  const { commandsState } = await import('../../src/utils/chatCommands.js')
  const { readFileSync } = await import('node:fs')
  const translations = JSON.parse(readFileSync(new URL('../../public/locales/en/chats.json', import.meta.url))).tools.commands
  for (const isAdmin of [true, false]) {
    for (const fallback of [undefined, 'off', 'unavailable']) {
      const projected = commandsState({ commands: 'available', commands_provider: 'builtin', commands_fallback: fallback && 'renamed', commands_fallback_reason: fallback })
      const notice = commandsNotice(projected.state, { isAdmin, reason: projected.reason })
      assert.equal(notice.tone, 'warn')
      assert.equal(notice.link, null)
      assert.equal(notice.askKey, null)
      assert.match(translations[notice.textKey], /without isolation/)
      assert.doesNotMatch(translations[notice.textKey], /sandbox|install|repair|switch.*on/i)
    }
  }
})

test('typed safety: actual switches reject late actor replies and hide old status before effects', async () => {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const names = ['chatMCP', 'chatSwitchRows', 'chatSkills', 'chatCommands', 'chatDraftSwitches']
  const imports = Object.assign({}, ...await Promise.all(names.map(name => import(`../../src/utils/${name}.js`))))
  const requests = []
  const list = () => new Promise(resolve => requests.push(resolve))
  const h = hookHarness('../../src/hooks/useChatSwitches.js', 'useChatSwitches', {
    ...imports, chatAddonsApi: { list }, chatToolsApi: { list: async () => ({}) }, chatSkillsApi: { list: async () => ({}) },
  })
  const render = actor => h.render('same-chat', false, null, actor)
  const isolated = { commands: 'available', commands_provider: 'renamed', commands_isolation: 'isolated' }
  render('A:1'); h.flush()
  assert.equal(requests.length, 1)
  assert.equal(render('B:1').commands.state, 'unknown', 'render invalidates A before effect cleanup')
  requests[0](isolated); await new Promise(resolve => setImmediate(resolve))
  assert.equal(render('B:1').commands.state, 'unknown', 'late A cannot publish in B even before effects')
  h.flush()
  assert.equal(requests.length, 2, 'actor change starts a new request')
  requests[1](isolated); await new Promise(resolve => setImmediate(resolve))
  assert.equal(render('B:1').commands.state, 'available')
  assert.equal(render('logged-out:2').commands.state, 'unknown')
  h.flush()
  assert.equal(render('B:3').commands.state, 'unknown', 'same user login does not reuse the old session')
  h.flush()
  requests[2](isolated); await new Promise(resolve => setImmediate(resolve))
  assert.equal(render('B:3').commands.state, 'unknown', 'logout reply cannot populate new login')
  requests[3](isolated); await new Promise(resolve => setImmediate(resolve))
  assert.equal(render('B:3').commands.state, 'available')
})

test('typed safety: actual auth epoch reaches composer and changes on same-user logout/login', async () => {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const originalFetch = globalThis.fetch
  const originalDocument = globalThis.document
  const originalWindow = globalThis.window
  try {
    globalThis.fetch = async () => ({ ok: true, json: async () => ({ authEnabled: true, user: { id: 'A' } }) })
    globalThis.document = { cookie: '' }
    globalThis.window = { location: {} }
    const auth = hookHarness('../../src/context/AuthContext.jsx', 'AuthProvider', { apiUrl: path => path, addonUpgrades: (await import('../../src/utils/addonUpgrades.js')).createUpgradeStore(), ...(await import('../../src/utils/authSession.js')), ...(await import('../../src/utils/sessionEvents.js')) }, source => source.replace(/return \(\s*<AuthContext.Provider[\s\S]*?\n  \)/, 'return value'))
    auth.render({}); auth.flush()
    await new Promise(resolve => setImmediate(resolve))
    let currentAuth = auth.render({})
    const seen = []
    const composer = hookHarness('../../src/hooks/useChatComposer.js', 'useChatComposer', {
      useAuth: () => currentAuth,
      useChatDrafts: () => ({ setFiles() {} }),
      useModels: () => ({ models: [] }), CAP_CHAT: 'chat',
      useChatSwitches: (...args) => { seen.push(args); return {} },
    })
    composer.render({ chatId: 'same-chat', userId: 'A' })
    const initial = seen.at(-1)[3]
    assert.ok(initial)
    const beforeLogout = currentAuth.sessionEpoch
    const logout = currentAuth.logout()
    currentAuth = auth.render({})
    assert.equal(currentAuth.user, null, 'logout invalidates before network completion')
    assert.ok(currentAuth.sessionEpoch > beforeLogout)
    composer.render({ chatId: 'same-chat', userId: 'A' })
    assert.notEqual(seen.at(-1)[3], initial)
    await logout
    await currentAuth.refresh()
    currentAuth = auth.render({})
    assert.equal(currentAuth.user.id, 'A')
    composer.render({ chatId: 'same-chat', userId: 'A' })
    assert.notEqual(seen.at(-1)[3], initial, 'same actor has a new epoch after login')
  } finally {
    globalThis.fetch = originalFetch
    globalThis.document = originalDocument
    globalThis.window = originalWindow
  }
})

for (const provider of ['copper-runner', 'violet-executor']) test(`typed safety: production Composer uses current ${provider} DTO, never discovery isolation`, async () => {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const names = ['chatMCP', 'chatSwitchRows', 'chatSkills', 'chatCommands', 'chatDraftSwitches']
  const imports = Object.assign({}, ...await Promise.all(names.map(name => import(`../../src/utils/${name}.js`))))
  const requests = []
  const switchesHook = hookHarness('../../src/hooks/useChatSwitches.js', 'useChatSwitches', {
    ...imports, chatAddonsApi:{list:() => new Promise(resolve => requests.push(resolve))},
    chatToolsApi:{list:async () => ({})}, chatSkillsApi:{list:async () => ({})},
  })
  // The rows of the Tools list come from useToolGroups, which the Tools menu
  // of a chat and the agent form both use.
  const toolGroups = hookHarness('../../src/hooks/useToolGroups.js', 'useToolGroups', {
    useTranslation:() => ({t:key => key}), connectorRef: imports.connectorRef, unknownTextKey: imports.unknownTextKey, ...await import('../../src/utils/toolOptions.js'),
  }, source => source.replace(/export const/g, 'const'))
  const group = {group:'shell',providers:{bash:provider},provider,usable:true}
  const render = (actor = 'alice:1', override) => {
    const switches = { ...switchesHook.render('same-chat', false, null, actor), builtins: [group], toggleBuiltin() {} }
    if (override) switches.commands = override
    return toolGroups.render(switches, {}).find(g => g.key === 'builtins').rows[0]
  }
  const unknown = note => {
    assert.equal(note.note, 'tools.commands.unknown')
    assert.notEqual(note.note, 'tools.shell.isolated')
  }
  unknown(render()); switchesHook.flush()
  const reply = isolation => ({commands:'available',commands_provider:provider,execution_status:{readiness:'available',isolation,generation:2,incarnation:'run-2'}})
  requests[0](reply('isolated')); await new Promise(resolve => setImmediate(resolve))
  assert.equal(render().note, 'tools.shell.isolated')
  unknown(render('alice:2')) // Same user refresh before cleanup invalidates positive evidence.
  switchesHook.flush()
  unknown(render('bob:3')) // Withhold cleanup of the second request.
  requests[1](reply('isolated')); await new Promise(resolve => setImmediate(resolve))
  unknown(render('bob:3')) // Late authenticated generation cannot publish.
  switchesHook.flush()
  requests[2](reply('host')); await new Promise(resolve => setImmediate(resolve))
  assert.equal(render('bob:3').note, 'tools.commands.providerHost')
  assert.equal(render('bob:3').warn, true)
  for (const state of ['unknown', 'loading', '', null]) unknown(render('bob:3', {state,provider}))
  unknown(render('bob:3', {state:'available',provider:'different-current-provider'}))
  // Built-in bash remains a primitive choice, not an add-on identity.
  group.providers.bash = 'builtin'
  assert.equal(render('bob:3', {state:'host',provider:'builtin'}).warn, true)
})

test('unknown status: each reason code has its own text key, and a code from a newer server falls back', async () => {
  const { commandsState, unknownTextKey } = await import('../../src/utils/chatCommands.js')
  const locale = JSON.parse((await import('node:fs')).readFileSync(new URL('../../public/locales/en/chats.json', import.meta.url), 'utf8'))
  for (const code of ['owner_unreachable', 'not_reported', 'stale', 'invalid_status', 'not_selected', 'no_answer', 'reported_unknown']) {
    const res = { commands: 'available', commands_provider: 'sandbox', execution_status: { readiness: 'unknown', isolation: 'unknown', unknown_reason: code } }
    const state = commandsState(res)
    assert.equal(state.state, 'unknown')
    assert.equal(state.code, code)
    const info = commandsNotice('unknown', { code: state.code })
    assert.equal(info.textKey, `unknownBy.${code}`)
    assert.ok(locale.tools.commands.unknownBy[code], `missing text for ${code}`)
  }
  assert.equal(unknownTextKey('from_the_future'), 'unknown')
  assert.equal(unknownTextKey(''), 'unknown')
  assert.equal(commandsState({ commands: 'available', commands_provider: 'sandbox', execution_status: { readiness: 'unknown', isolation: 'unknown', unknown_reason: 'x' } }).code, '')
})

test('a draft chat has no execution status to warn about', async () => {
  const { commandsState } = await import('../../src/utils/chatCommands.js')
  const draft = { readiness: 'unknown', isolation: 'unknown', unknown_reason: 'draft' }
  const available = commandsState({ commands: 'available', commands_provider: 'sandbox', execution_status: draft })
  assert.equal(available.state, 'draft')
  assert.equal(commandsNotice(available.state), null)
  assert.equal(commandsState({ commands: 'none', execution_status: draft }).state, 'none')
})

test('the hide button hides a notice whose state has no binding identity yet', async () => {
  const { readFileSync } = await import('node:fs')
  const source = readFileSync(new URL('../../src/hooks/useCommandsNotice.js', import.meta.url), 'utf8').replace(/^import .*\n/gm, '').replace('export function', 'function')
  let hidden = ''
  const hook = new Function('useState', 'useEffect', 'useCallback', 'commandsNotice', `${source}; return useCommandsNotice`)(
    () => [hidden, v => { hidden = v }], () => {}, fn => fn, commandsNotice)
  const read = () => hook({ state: 'unknown', reason: '', identity: '' }, false, ['user', null, 'm', ''])
  assert.ok(read().info)
  assert.equal(read().hidden, false)
  read().hide()
  assert.equal(read().hidden, true, 'a click on Hide must hide the notice even with no identity')
})

test('a draft chat keeps its commands state when a tool is switched on and off', async () => {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const names = ['chatMCP', 'chatSwitchRows', 'chatSkills', 'chatCommands', 'chatDraftSwitches']
  const imports = Object.assign({}, ...await Promise.all(names.map(name => import(`../../src/utils/${name}.js`))))
  const draftReply = { commands: 'available', commands_provider: 'sandbox', addons: [{ id: 'sandbox', tools: 1 }], execution_status: { readiness: 'unknown', isolation: 'unknown', unknown_reason: 'draft' } }
  let draftCalls = 0
  const h = hookHarness('../../src/hooks/useChatSwitches.js', 'useChatSwitches', {
    ...imports,
    chatAddonsApi: { draft: async () => { draftCalls++; return draftReply }, list: async () => ({}), setOff: async () => ({}) },
    chatToolsApi: { draft: async () => ({ builtins: [{ group: 'shell', off: false }] }), list: async () => ({}), setBuiltinsOff: async () => ({}) },
    chatSkillsApi: { list: async () => ({ skills: [] }) },
  })
  const render = () => h.render(null, true, { model: 'm', agentId: '' }, 'A:1')
  const settle = () => new Promise(resolve => setImmediate(resolve))
  render(); h.flush(); await settle()
  assert.equal(render().commands.state, 'draft')
  assert.equal(commandsNotice(render().commands.state), null, 'no banner for a draft that is ready at its first message')
  for (const step of ['builtin off', 'builtin on', 'addon off', 'addon on']) {
    const s = render()
    if (step.startsWith('builtin')) await s.toggleBuiltin('shell', step.endsWith('on'))
    else await s.toggleAddon('sandbox', step.endsWith('on'))
    h.flush(); await settle()
    const now = render()
    assert.equal(commandsNotice(now.commands.state), null, `${step}: no banner`)
    assert.ok(now.commands.identity, `${step}: the state is the list's, not a reset`)
  }
})

test('chat commands: with the built-in bash off the notice says who can turn it on', async () => {
  const { HOST_EXEC_SETTING, ADDONS_PAGE } = await import('../../src/utils/chatCommands.js')
  const admin = commandsNotice('none', { isAdmin: true, hostExec: 'off' })
  assert.equal(admin.hostOff, true)
  assert.equal(admin.severity, 'warn')
  assert.equal(admin.whoKey, 'hostOff.whoAdmin')
  assert.deepEqual(admin.link, { to: HOST_EXEC_SETTING, labelKey: 'hostOff.fix' })
  assert.deepEqual(admin.sandboxLink, { to: ADDONS_PAGE, labelKey: 'hostOff.sandbox' })
  // Anyone else is told to ask: no setting link, no add-on link.
  const member = commandsNotice('none', { isAdmin: false, hostExec: 'off' })
  assert.equal(member.whoKey, 'hostOff.whoMember')
  assert.equal(member.link, null)
  assert.equal(member.sandboxLink, null)
  // A server that does not say the mode, or says it is on, keeps the plain notice.
  for (const hostExec of ['', 'ask', 'on', undefined]) assert.equal(commandsNotice('none', { isAdmin: true, hostExec }).textKey, 'none')
  // The setting link opens Settings, Tools, on the bash row.
  assert.match(HOST_EXEC_SETTING, /^\/app\/settings\?group=tools#settings-tools-row-bash$/)
})
