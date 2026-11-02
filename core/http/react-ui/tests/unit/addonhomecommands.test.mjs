import test from 'node:test'
import assert from 'node:assert/strict'
import { projectHomeCommands, resolveHomeCommand } from '../../src/addons/homeCommands.js'
import { shouldPoll } from '../../src/addons/registryState.js'
const entry = '/api/addons/paint/assets/' + 'a'.repeat(64) + '/ui.js'
const app = (id = 'paint', commands = [{ id: 'open', alias: 'paint', page: 'canvas', label: 'Paint', icon: 'palette' }]) => ({ id, generation: 3, enabled: true, state: 'running', ui: { entry: entry.replace('/paint/', `/${id}/`), sdk: { min: 2, max: 2 }, pages: ['canvas', 'history'], homeCommands: commands } })
const snapshot = (apps = [app()], extra = {}) => ({ apps, allowed: true, status: 'ready', ...extra })
test('Home addons: renamed addon and second creative command need no core branch', () => {
 const p = projectHomeCommands(snapshot([app('creative', [{ id: 'make', page: 'canvas', label: 'Make', icon: 'palette' }, { id: 'past', page: 'history', label: 'Past', icon: 'clock' }])]))
 assert.deepEqual(p.commands.map(c => c.cmd), ['/addon:creative:make', '/addon:creative:past'])
 assert.equal(resolveHomeCommand('/addon:creative:make', snapshot([app('creative', [{ id: 'make', page: 'canvas', label: 'Make', icon: 'palette' }])]))?.page, 'canvas')
})
test('Home addons: aliases reserve native and skills and suppress all colliders deterministically', () => {
 const apps = [app('a'), app('b')]
 const opts = { nativeNames: new Set(['paint']) }
 assert.deepEqual(projectHomeCommands(snapshot(apps), opts).diagnostics.map(d => d.reason), ['native-name', 'native-name'])
 assert.equal(projectHomeCommands(snapshot([app()]), { skillNames: ['paint'] }).commands.length, 1)
 const p = projectHomeCommands(snapshot(apps))
 assert.equal(p.commands.length, 2)
 assert.deepEqual(p, projectHomeCommands(snapshot([...apps].reverse())))
 assert.equal(p.diagnostics[0].reason, 'addon-alias-collision')
})
test('Home addons: metadata unknown nested duplicate and unsafe destinations fail closed', () => {
 for (const patch of [{ target: 'https://evil' }, { page: '../canvas' }, { label: {} }, { labels: { en: {} } }, { icon: 'javascript:bad' }, { page: 'missing' }]) {
 const a = app(); Object.assign(a.ui.homeCommands[0], patch)
 assert.equal(projectHomeCommands(snapshot([a])).commands.length, 0)
 }
 const a = app(); a.ui.homeCommands.push({ ...a.ui.homeCommands[0] })
 assert.equal(projectHomeCommands(snapshot([a])).commands.length, 0)
})
test('Home addons: installed lifecycle error and absence remain distinct and polling recovers', () => {
 for (const [patch, state] of [[{enabled:false}, 'installed-disabled'], [{state:'failed'}, 'installed-failed'], [{state:'starting'}, 'loading'], [{ui:null}, 'no-ui']]) {
 assert.equal(projectHomeCommands(snapshot([{...app(), ...patch}])).states[0].status, state)
 assert.equal(projectHomeCommands(snapshot([{...app(), ...patch}])).commands.length, 0)
 }
 assert.equal(projectHomeCommands(snapshot([], {status:'error'})).status, 'list-error')
 assert.equal(projectHomeCommands(snapshot([])).status, 'absent')
 for (const status of ['idle','ready','error']) assert.equal(shouldPoll({allowed:true, consumers:1, apps:[],status}), true)
 assert.equal(projectHomeCommands(snapshot()).status, 'ready')
})
test('Home addons: exact typed and captured clicks reject stale generation permissions and arguments', () => {
 const c = projectHomeCommands(snapshot()).commands[0]
 assert.ok(resolveHomeCommand(c, snapshot()))
 assert.equal(resolveHomeCommand(c, snapshot([{...app(),generation:4}])), null)
 assert.equal(resolveHomeCommand(c, snapshot([], {allowed:false})), null)
 assert.equal(resolveHomeCommand(c, snapshot([], {status:'error'})), null)
 assert.equal(resolveHomeCommand('/paint args', snapshot()), null)
 assert.equal(resolveHomeCommand('/pai', snapshot()), null)
})

import { readFileSync } from 'node:fs'
import { createRegistryRequests } from '../../src/addons/registryRequests.js'
import { dispatchHomeCommand } from '../../src/addons/homeCommands.js'
import { availableCommands, parseInvocation, reservedNames } from '../../src/utils/slashCommands.js'
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

test('Home addons: outstanding logout poll aborts and cannot overwrite the next actor', async () => {
 const requests = createRegistryRequests(), old = deferred(), published = []
 requests.setScope('alice:allowed')
 let signal
 const pending = requests.read(s => { signal = s; return old.promise }, s => published.push(s))
 requests.setScope('bob:forbidden')
 assert.ok(signal.aborted)
 old.resolve([app()])
 assert.equal(await pending, null)
 assert.deepEqual(published, [])
 requests.setScope('bob:allowed')
 await requests.read(async () => [app('bob')], s => published.push(s))
 assert.equal(published[0].apps[0].id, 'bob')
})
test('Home addons: cancellation newer reads and registry errors withdraw stale authority', async () => {
 const requests = createRegistryRequests(), old = deferred(), published = [], ctl = new AbortController()
 const pending = requests.read(() => old.promise, s => published.push(s), ctl.signal)
 ctl.abort(); old.resolve([app()]); assert.equal(await pending, null)
 const late = deferred()
 const first = requests.read(() => late.promise, s => published.push(s))
 await requests.read(async () => { throw new Error('offline') }, s => published.push(s))
 late.resolve([app()]); assert.equal(await first, null)
 assert.equal(published.length, 1)
 assert.equal(projectHomeCommands({ ...published[0], allowed:true }).status, 'list-error')
 assert.deepEqual(published[0].apps, [])
})
test('Home addons: live authenticated dispatch navigates only the current installed generation', async () => {
 const command = projectHomeCommands(snapshot()).commands[0], paths = []
 let now = { actor:'alice', allowed:true, list:snapshot() }
 const requests = createRegistryRequests()
 requests.setScope(now.actor)
 const invoke = load => dispatchHomeCommand(command, {
  current: () => now, navigate: path => paths.push(path),
  refresh: () => requests.read(load, list => { now.list = list }),
 })
 assert.equal(await invoke(async () => [app()]), true)
 assert.deepEqual(paths, ['/app/addons/paint/canvas'])
 for (const patch of [{enabled:false}, {state:'failed'}, {generation:4}, {ui:{...app().ui, pages:['history']}}]) {
  assert.equal(await invoke(async () => [{...app(), ...patch}]), false)
 }
 const late = deferred(), clicking = invoke(() => late.promise)
 now = { actor:'bob', allowed:false, list:snapshot([]) }; requests.setScope(now.actor)
 late.resolve([app()]); assert.equal(await clicking, false)
 assert.equal(paths.length, 1)
})
test('Home addons: disabled failed ready transitions remain pollable and restart after permission returns', async () => {
 const requests = createRegistryRequests()
 let state = snapshot([])
 for (const patch of [{enabled:false}, {state:'failed'}, {}]) {
  assert.ok(shouldPoll({...state, consumers:1}))
  await requests.read(async () => [{...app(), ...patch}], next => { state = {...next,allowed:true} })
 }
 assert.ok(projectHomeCommands(state).commands.length > 0)
 assert.equal(shouldPoll({...state, allowed:false, consumers:1}), false)
 assert.equal(shouldPoll({...state, allowed:true, consumers:1}), true)
})
test('Home addons: Studio manifest is consumed generically and live Home has no identity branches', () => {
 const manifest = JSON.parse(readFileSync(new URL('../../../../../addons/studio/manifest.json', import.meta.url)))
 const ui = manifest.capabilities.ui
 const commands = ui.homeCommands.map(c => ({...c, page:c.page || ui.mainPage, label:c.label || ui.title, icon:c.icon || 'puzzle'}))
 const installed = app(manifest.id, commands); installed.ui.pages = ui.pages
 const menu = availableCommands('home', {addonRegistry:snapshot([installed])})
 assert.ok(menu.some(c => c.cmd === '/studio'))
 assert.ok(menu.some(c => c.cmd === '/addon:studio:open'))
 assert.ok(!availableCommands('chat', {addonRegistry:snapshot([installed])}).some(c => c.addonId))
 assert.ok(!availableCommands('home', {addonRegistry:snapshot([])}).some(c => c.addonId))
 for (const path of ['../../src/pages/Home.jsx','../../src/components/home/homeActions.js']) {
  assert.doesNotMatch(readFileSync(new URL(path, import.meta.url), 'utf8'), /studio/i)
 }
 const composer = readFileSync(new URL('../../src/components/home/HomeComposer.jsx', import.meta.url), 'utf8')
 assert.match(composer, /onRunAction\(entry.id, entry\)/)
 assert.match(composer, /onRunAction\(exact.id, exact\)/)
 const registry = readFileSync(new URL('../../src/addons/registry.jsx', import.meta.url), 'utf8')
 assert.match(registry, /dispatchHomeCommand\(command/)
 assert.match(registry, /requests.read\(s => api.apps.list\(s\)/)
})
test('Home addons: native hidden names and usable skills are reserved on the actual menu', () => {
 const apps = [app('creative', [{id:'open',alias:'settings',page:'canvas',label:'Create',icon:'palette'}])]
 assert.ok(!availableCommands('home', {addonRegistry:snapshot(apps)}).some(c => c.addonId && c.cmd === '/settings'))
 assert.ok(reservedNames().has('settings'))
 const menu = availableCommands('home', {addonRegistry:snapshot(),skills:[{name:'paint',enabled:true}]})
 assert.equal(menu.filter(c => c.cmd === '/paint').length, 1)
 assert.ok(!menu.find(c => c.cmd === '/paint').addonId)
 assert.equal(parseInvocation('/addon:paint:open', {skills:[{name:'addon:paint:open',enabled:true}]}), null)
})
