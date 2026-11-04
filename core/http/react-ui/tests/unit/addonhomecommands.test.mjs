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
 const commands = ui.homeCommands.map(c => ({...c, page:c.page || ui.mainPage, label:c.label || (typeof ui.title === 'string' ? ui.title : ui.title.en), icon:c.icon || 'puzzle'}))
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

test('Home addons: resolved inherited titles accept 61 through 80 Unicode characters and localized titles', () => {
 for (let length = 61; length <= 80; length++) {
  const label = 'é'.repeat(length), localized = '界'.repeat(length)
  const a = app('paint', [{id:'open',page:'canvas',label,labels:{it:localized},icon:'palette'}])
  const projected = projectHomeCommands(snapshot([a]), {language:'it'})
  assert.equal(projected.states[0].status, 'ready', `title length ${length}`)
  assert.equal(projected.commands[0]?.label, localized)
  assert.equal(projectHomeCommands(snapshot([a])).commands[0]?.label, label)
 }
})
test('Home addons: resolved secondary page fallback accepts the exact maximum and rejects overflow', () => {
 const page = 'p'.repeat(64), label = 'é'.repeat(80) + ' — ' + page
 const localized = '界'.repeat(80) + ' — ' + page
 assert.equal([...label].length, 147)
 const a = app('paint', [{id:'open',page,label,labels:{it:localized},icon:'palette'}])
 a.ui.pages.push(page)
 assert.equal(projectHomeCommands(snapshot([a])).commands[0]?.label, label)
 assert.equal(projectHomeCommands(snapshot([a]), {language:'it'}).commands[0]?.label, localized)
 for (const patch of [{label:label + 'x'}, {labels:{it:localized + 'x'}}, {label:'safe\u0085unsafe'}, {labels:{it:'safe\u202eunsafe'}}, {label:'<unsafe>'}]) {
  const bad = structuredClone(a); Object.assign(bad.ui.homeCommands[0], patch)
  const projected = projectHomeCommands(snapshot([bad]))
  assert.equal(projected.commands.length, 0)
  assert.equal(projected.states[0].status, 'invalid-metadata')
 }
})
test('Home addons: immutable package icons accept dotted segments and all contract image extensions', () => {
 for (const path of ['ui.v1/icon.png','ui/icon.dark.png','ui.v1/icon.dark.JPEG','ui/icon.webp','ui/icon.jpg', 'a'.repeat(252) + '.png']) {
  const a = app(); a.ui.homeCommands[0].icon = entry.slice(0, -5) + path
  const projected = projectHomeCommands(snapshot([a]))
  assert.equal(projected.states[0].status, 'ready', path)
  assert.equal(projected.commands[0]?.iconUrl, a.ui.homeCommands[0].icon, path)
 }
})
test('Home addons: package icons reject traversal encodings foreign prefixes and non-image extensions', () => {
 const base = entry.slice(0, -5)
 const paths = ['../icon.png','./icon.png','ui/../icon.png','ui/./icon.png','ui//icon.png','/icon.png','ui/icon.png/', 'ui\\icon.png', '%2e%2e/icon.png','ui%2ficon.png','ui%5cicon.png','ui/icon%2epng','ui/icon.png?x=1','ui/icon.png#x','ui/icon.svg','ui/icon.gif','ui/icon.js','ui/icon.png\u0000', 'a'.repeat(253) + '.png']
 const urls = paths.map(p => base + p).concat(['https://evil/icon.png','//evil/icon.png','javascript:bad', base.replace('/paint/', '/other/') + 'icon.png', base.replace('a'.repeat(64), 'b'.repeat(64)) + 'icon.png'])
 for (const icon of urls) {
  const a = app(); a.ui.homeCommands[0].icon = icon
  const projected = projectHomeCommands(snapshot([a]))
  assert.equal(projected.commands.length, 0, icon)
  assert.equal(projected.states[0].status, 'invalid-metadata', icon)
 }
})

// Run the production provider, including its request admission and Home dispatch.
// Effects stay explicitly controlled so refresh races cannot rely on cleanup.
async function registrySessionHarness() {
 const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
 const { transformWithOxc } = await import('vite')
 const source = readFileSync(new URL('../../src/addons/registry.jsx', import.meta.url), 'utf8')
 const provider = source.slice(0, source.indexOf('  const boundaryLabels')) + '  return value\n}'
 const { code } = await transformWithOxc(provider, 'registry.jsx', { jsx: { runtime:'classic' } })
 let auth = { user:{id:'alice'}, isAdmin:true, hasFeature:() => true, sessionEpoch:1 }
 const reads = [], paths = [], revalidated = []
 const api = { apps:{list(signal) { const response = deferred(); reads.push({signal, ...response}); return response.promise }} }
 const manager = { snapshot:() => [], revalidate:apps => revalidated.push(apps), disposeAll() {} }
 const h = hookHarness('../../src/addons/registry.jsx', 'AddonRegistryProvider', {
  useAuth:() => auth, useNavigate:() => path => paths.push(path), useLocation:() => ({pathname:'/app/home',search:''}),
  useTranslation:() => ({t:key => key,i18n:{language:'en'}}), FEATURE_ADDONS:'addons', addonsApi:api,
  apiUrl:path => path, fetch:async () => ({json:async () => ({addons:true})}),
  createRegistryRequests, dispatchHomeCommand, createRuntimeManager:() => manager,
  resolveManage:() => false, resolveTab:() => 'apps', shouldPoll, pollDelay:() => 1000,
  buildNavigation:() => [], availableChatActions:() => [],
  document:{hidden:false}, setTimeout:() => 1, clearTimeout:() => {},
 }, () => code.replace(/^import .*\n/gm, '').replace('export function', 'function'))
 const render = () => h.render({api})
 render(); h.flush()
 await new Promise(resolve => setImmediate(resolve))
 render(); h.flush(); render()
 return { h, render, reads, paths, revalidated,
  refreshSession(user = auth.user) { auth = {...auth,user,sessionEpoch:auth.sessionEpoch + 1}; return render() },
  discardRefresh() { const live = auth; auth = {...auth,sessionEpoch:auth.sessionEpoch + 1}; try { return h.renderDiscarded({api}) } finally { auth = live } } }
}

test('Home addons: actual registry rejects same-user refresh late poll before effect cleanup', async () => {
 const r = await registrySessionHarness()
 const value = r.render()
 value.acquire(); r.render(); r.h.flush() // Start the actual production polling effect.
 assert.equal(r.reads.length, 1)
 const poll = r.reads[0]
 r.refreshSession() // Same ID, feature and admin; deliberately do not flush effects.
 poll.resolve([app()])
 await new Promise(resolve => setImmediate(resolve))
 assert.deepEqual(r.revalidated, [])
 assert.ok(poll.signal.aborted)
 assert.deepEqual(r.render().apps, [])
 // Now cleanup may run; permission and polling recovery still work.
 r.h.flush()
 assert.equal(r.reads.length, 2)
 r.reads[1].resolve([app()])
 await new Promise(resolve => setImmediate(resolve))
 assert.equal(r.render().apps[0].id, 'paint')
})

test('Home addons: actual registry pending NAV cannot cross same-user session refresh', async () => {
 const r = await registrySessionHarness()
 const command = projectHomeCommands(snapshot()).commands[0]
 const pending = r.render().navigateHomeCommand(command)
 assert.equal(r.reads.length, 1) // Fresh authenticated refetch already began.
 r.refreshSession() // No metadata, ID or permissions change, and no effect cleanup.
 r.reads[0].resolve([app()])
 assert.equal(await pending, false)
 assert.deepEqual(r.paths, [])
 assert.deepEqual(r.revalidated, [])
 const fresh = r.render().navigateHomeCommand(command)
 r.reads[1].resolve([app()])
 assert.equal(await fresh, true)
 assert.deepEqual(r.paths, ['/app/addons/paint/canvas'])
})

for (const accountSwitch of [false, true]) test(`Apps registry: late list cannot cross ${accountSwitch ? 'account switch' : 'same-user refresh'} before cleanup`, async () => {
 const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
 const r = await registrySessionHarness()
 let registry = r.render()
 let auth = { user:{id:'alice'}, sessionEpoch:1, isAdmin:true, hasFeature:() => true }
 const reads = []
 const api = { apps:{ read:() => { const d = deferred(); reads.push(d); return d.promise.then(apps => ({ apps })) } } }
 const h = hookHarness('../../src/components/addons/AppsView.jsx', 'AppsView', {
  useAuth:() => auth, useContext:() => registry, RegistryContext:{}, useTranslation:() => ({t:x=>x,i18n:{}}),
  FEATURE_ADDONS:'addons', pollDelay:() => 1000, setTimeout:()=>1, clearTimeout:()=>{},
 }, s => s.replace('export default function', 'function').split('  // The installed record')[0] + 'return state\n}')
 const render = () => h.render({api})
 render(); h.flush()
 const staleIngest = registry.ingest
 auth = {...auth, user:{id:accountSwitch ? 'bob' : 'alice'},sessionEpoch:2}
 registry = r.refreshSession(auth.user)
 render() // Deliberately withhold Apps effect cleanup.
 reads[0].resolve([app()]); await new Promise(resolve=>setImmediate(resolve))
 assert.deepEqual(render().apps, [])
 assert.deepEqual(r.render().apps, [])
 staleIngest([app()]) // Registry independently rejects the originating authority.
 assert.deepEqual(r.render().apps, [])
 h.flush(); reads[1].resolve([app()]); await new Promise(resolve=>setImmediate(resolve))
 assert.equal(render().apps[0].id, 'paint')
 assert.equal(r.render().apps[0].id, 'paint')
})

test('Home addons: a discarded render does not change the scope of live requests', async () => {
 const r = await registrySessionHarness()
 const value = r.render()
 value.acquire(); r.render(); r.h.flush()
 const poll = r.reads[0]
 const shown = r.discardRefresh() // React renders a new session epoch and throws the render away.
 assert.deepEqual(shown.apps, [], 'the discarded render itself never shows the old list')
 assert.equal(poll.signal.aborted, false, 'the live request keeps its scope')
 poll.resolve([app()])
 await new Promise(resolve => setImmediate(resolve))
 assert.equal(r.revalidated.length, 1, 'the live read still publishes')
 r.refreshSession() // The same epoch committed for real moves the scope at once.
 assert.equal(r.reads.length, 1)
})
