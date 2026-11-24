// The Administration sidebar: which groups and items a viewer sees, which is
// current, and what the command palette can search. Pure data, no browser.
import test from 'node:test'
import assert from 'node:assert/strict'

const hub = await import('../../src/components/hub/hubConfig.js')
const palette = await import('../../src/components/admin/paletteModel.js')

const auth = (features, extra = {}) => ({ isAdmin: true, authEnabled: true, hasFeature: () => true, features, ...extra })
const view = (features, pathname, search = '', extra) => {
  const { groups, current, owner } = hub.resolveGroups(hub.operateHub, auth(features, extra), { pathname, search })
  return {
    ids: groups.map(e => e.group.id),
    items: Object.fromEntries(groups.map(e => [e.group.id, e.items.map(i => i.path + (i.query ? `?${i.query}` : ''))])),
    current: current && `${current.group.id}:${current.item.path}${current.item.query ? `?${current.item.query}` : ''}`,
    owner: owner?.id,
  }
}

test('a cluster: Workers, Placement and Sharing under Cluster, no This machine group', () => {
  const v = view({ distributed: true, p2p: false, chats: true }, '/app/operate')
  assert.ok(v.ids.includes('cluster'))
  assert.deepEqual(v.items.cluster, ['/app/nodes', '/app/scheduling', '/app/p2p'])
  assert.ok(!v.ids.includes('machine'))
  // Failover is a Placement view on a cluster, not a Models and runtime page.
  assert.ok(!v.items.runtime.includes('/app/failover'))
})

test('a single server: This machine, and Failover among the runtime pages', () => {
  const v = view({ distributed: false, p2p: false, chats: true }, '/app/nodes')
  assert.ok(!v.ids.includes('cluster'))
  assert.deepEqual(v.items.machine, ['/app/nodes', '/app/p2p'])
  assert.ok(v.items.runtime.includes('/app/failover'))
  assert.equal(v.current, 'machine:/app/nodes')
})

test('before /api/features answers the cluster group is not drawn', () => {
  const v = view({}, '/app/operate')
  assert.ok(!v.ids.includes('cluster'))
  assert.ok(!v.ids.includes('machine') || v.items.machine.length === 0)
})

test('the failover address is the Placement item on a cluster and a runtime page otherwise', () => {
  assert.equal(view({ distributed: true }, '/app/failover').current, 'cluster:/app/scheduling')
  assert.equal(view({ distributed: true }, '/app/scheduling/failover').current, 'cluster:/app/scheduling')
  assert.equal(view({ distributed: false }, '/app/failover').current, 'runtime:/app/failover')
})

test('Traffic is folded and holds its seven pages; its group is the owner on one of them', () => {
  assert.equal(hub.operateHub.groups.find(g => g.id === 'traffic').folded, true)
  assert.equal(hub.operateHub.groups.find(g => g.id === 'traffic').items.length, 7)
  assert.equal(view({}, '/app/operate').owner, 'landing')
  for (const path of ['/app/traffic', '/app/usage', '/app/traffic/models', '/app/traffic/host', '/app/traces', '/app/traces/abc', '/app/middleware', '/app/traffic/prometheus']) {
    assert.equal(view({}, path).owner, 'traffic', path)
  }
  // Overview is not lit on a Traffic page, and Traffic overview not on Usage.
  assert.equal(view({}, '/app/traffic/models').current, 'traffic:/app/traffic/models')
  assert.equal(view({}, '/app/usage').current, 'traffic:/app/usage')
  assert.equal(view({}, '/app/traffic').current, 'traffic:/app/traffic')
})

test('Access: users and API keys are two pages', () => {
  const f = { chats: true }
  assert.deepEqual(view(f, '/app/users').items.access, ['/app/users', '/app/api-keys', '/app/chats-admin'])
  assert.equal(view(f, '/app/users').current, 'access:/app/users')
  assert.equal(view(f, '/app/users', '?tab=invites').current, 'access:/app/users')
  assert.equal(view(f, '/app/api-keys').current, 'access:/app/api-keys')
})

test('gates: no sign-in hides Users and API keys, a server without chats hides Chats of all users, a non-admin sees nothing', () => {
  const noAuth = view({ chats: true }, '/app/operate', '', { authEnabled: false })
  assert.deepEqual(noAuth.items.access, ['/app/chats-admin'])
  assert.ok(!view({ chats: false }, '/app/operate').items.access?.includes('/app/chats-admin'))
  const none = view({ chats: true }, '/app/operate', '', { isAdmin: false })
  assert.deepEqual(none.ids, [])
  assert.equal(hub.hubEntryPath(hub.operateHub, auth({}, { isAdmin: false })), null)
  assert.equal(hub.hubEntryPath(hub.operateHub, auth({})), '/app/operate')
})

test('every route of Administration is owned by the hub', () => {
  for (const path of ['/app/operate', '/app/backends', '/app/activity', '/app/updates', '/app/updates/backend/x', '/app/settings', '/app/node-backend-logs/a/b', '/app/nodes/add', '/app/nodes/w1', '/app/backend-logs/m', '/app/scheduling/failover']) {
    assert.equal(hub.hubOwnsPath(hub.operateHub, path), true, path)
  }
  assert.equal(hub.hubOwnsPath(hub.operateHub, '/app/models'), false)
  assert.equal(hub.hubOwnsPath(hub.operateHub, '/app/chats'), false)
})

test('palette: pages first, every word must match, a title that starts with the query ranks first', () => {
  const t = key => ({ 'sections.operate': 'Administration', 'admin.groups.cluster': 'Cluster', 'admin.groups.access': 'Access', 'admin.groups.runtime': 'Models and runtime', 'items.users': 'Users', 'items.nodes': 'Workers', 'items.activity': 'Activity', 'items.scheduling': 'Placement', 'items.p2p': 'Sharing' }[key] || key)
  const v = hub.resolveGroups(hub.operateHub, auth({ distributed: true, chats: true }), { pathname: '/app/operate', search: '' })
  const pages = palette.pageEntries(v.groups, t)
  const people = palette.personEntries([{ id: '1', name: 'Chen Wei', email: 'chen@example.com' }, { id: '2', email: 'dana@example.com' }], 'Administration / Access')
  const workers = palette.workerEntries([{ id: 'w1', name: 'worker-a' }], 'Administration / Cluster')
  const settings = palette.settingEntries([{ key: 'watchdog_enabled', group: 'memory', label: 'Watchdog', desc: 'Monitor backends' }], [{ id: 'memory', label: 'Models in memory' }], 'Administration')
  const all = [...pages, ...settings, ...workers, ...people]
  // No query: only the pages.
  assert.ok(palette.rank(all, '').every(e => e.kind === 'page'))
  assert.ok(palette.rank(all, '').length >= 8)
  // A word narrows down and spans kinds, in the order pages, settings, workers, people.
  assert.deepEqual(palette.rank(all, 'worker').map(e => e.kind), ['page', 'worker'])
  assert.equal(palette.rank(all, 'chen')[0].kind, 'person')
  assert.equal(palette.rank(all, 'chen')[0].to, '/app/users?q=chen%40example.com')
  assert.equal(palette.rank(all, 'watch')[0].to, '/app/settings?group=memory')
  assert.equal(workers[0].to, '/app/nodes/w1')
  // Every word must be present.
  assert.equal(palette.rank(all, 'chen dana').length, 0)
  assert.equal(palette.rank(all, 'zzzz').length, 0)
  // The title that starts with the word first: "Users" before "Chats of all users" for "users".
  assert.equal(palette.rank(all, 'users')[0].title, 'Users')
})
