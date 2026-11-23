// Hub configuration shared by HubLayout (renders the tab bar of the legacy
// Add-ons hub), AdminSidebar (renders the groups of Administration), the
// Sidebar (renders the single entry for each hub and computes its active
// state) and App (page-transition key).
//
// A hub is either a page-level tab bar (`tabs`) or, for Administration, the
// groups of its own sidebar (`groups`, which have the same shape and the same
// gates). Each tab owns one or more existing routes
// (`items`). A tab with several routes shows a second, quieter row of links
// under the bar. Routes stay flat (`/app/nodes/:id`); a tab is
// highlighted when the current path is one of its item paths or sits under
// one, so sub-pages such as `nodes/:id` keep the right tab lit.
//
// Gating is per item and decides the tab: a tab shows when at least one of its
// items is visible to the viewer, so a tab nobody can use is never drawn.
// `/api/features` only emits: collections, mcp, fine_tuning, quantization,
// distributed, p2p, backend_install, localai_assistant. Capability-style flags (face_recognition,
// skills, ...) come from hasFeature(), not the features map.
//
// Item and tab fields:
//   path          route the item links to
//   labelKey      `nav` i18n key of the item label
//   adminOnly, authOnly, feature, requiresCollections, unlessFeature  gating
//                 (unlessFeature takes one flag or a list: the item hides
//                 while any of them is not answered false)
//   match         extra path prefixes that belong to the tab (sub-pages)
//   showWhen      tab only: `/api/features` flag that must be on, or a list
//                 of flags of which one must be on
//   showWhenOff   tab only: `/api/features` flag that must be off (answered false)
//   revealOnRoute tab only: draw the tab while the viewer is on one of its
//                 routes, even when `showWhen` is off
//   signals       tab only: values from the Operate summary (see
//                 OperateSummaryContext), each drawn as a neutral count
//                 (kind `count`) or an attention badge (kind `attention`,
//                 or `error` for failures); kind `update` is the accent count
//                 of the Updates tab, red when an update failed

import { FEATURE_ADDONS } from '../../utils/capabilities.js'

// The Add-ons drawer of the legacy layout (LOCALAI_LEGACY_NAVIGATION). It is
// what is left of the Build hub: memory and the agents have sidebar items of
// their own, Import is part of adding a model and the other tools are add-ons.
// With the new layout the drawer is a page of its own and this hub has no
// visible tab, so the sidebar does not list it.
export const legacyAddonsHub = {
  id: 'addons',
  titleKey: 'items.addons',
  icon: 'puzzle',
  overviewPath: '/app/addons',
  tabs: [
    {
      id: 'addons',
      labelKey: 'items.addons',
      descriptionKey: 'hub.descriptions.addons',
      icon: 'puzzle',
      showWhenOff: 'addon_navigation',
      // The page serves everyone with the feature; the management tabs are
      // administrator-only and checked by the server on every call.
      items: [{ path: '/app/addons', labelKey: 'items.addons', feature: FEATURE_ADDONS, exact: true }],
      // Add-on pages that declare navigation are listed in the second row.
      addonNavigation: true,
    },
  ],
}

// Administration has its own sidebar (components/admin/AdminSidebar.jsx): the
// groups below are its sections. Nothing is removed and no gate changes from
// the tab bar this replaces. A group is drawn when at least one of its items is
// visible, and it follows the same gate fields as a tab (showWhen,
// showWhenOff, revealOnRoute, match), so the rules below are the single place
// that says which page exists when.
//
// Item fields besides the gates above:
//   icon          kit icon of the item
//   query         the query string that tells two items of one route apart
//                 (`tab=keys`); the item without one is the route's default
//   signal        a value of the Operate summary (see OperateSummaryContext)
//                 drawn at the end of the item: kind `attention`, `count`,
//                 `error` or `update`
// Group fields: `labelKey` (none: the group has no heading), `folded` (closed
// until one of its pages is open), `signal` (drawn on a folded group).
export const operateHub = {
  id: 'operate',
  titleKey: 'sections.operate',
  icon: 'sliders',
  overviewPath: '/app/operate',
  groups: [
    {
      id: 'landing',
      items: [{ path: '/app/operate', labelKey: 'admin.nav.overview', icon: 'gauge', adminOnly: true, exact: true, signal: { key: 'attention', kind: 'attention' } }],
    },
    {
      // A single install: the Nodes route under the name it has there, where it
      // shows this host and what is loaded on it. With distributed mode on the
      // Cluster group takes over the same route.
      id: 'machine',
      labelKey: 'admin.groups.machine',
      items: [
        { path: '/app/nodes', labelKey: 'items.thisMachine', icon: 'monitor', adminOnly: true, unlessFeature: 'distributed', signal: { key: 'running', kind: 'count' } },
        // With P2P off on a single server its pages sit here, where the
        // administrator switches it on. With P2P on, or in distributed mode,
        // they are in the Cluster group.
        { path: '/app/p2p', labelKey: 'items.p2p', icon: 'share', adminOnly: true, unlessFeature: ['distributed', 'p2p'] },
      ],
      match: ['/app/node-backend-logs'],
    },
    {
      id: 'cluster',
      labelKey: 'admin.groups.cluster',
      // A cluster, or P2P on (a single server too: its paired devices, remotes
      // and relays are a cluster of their own).
      showWhen: ['distributed', 'p2p'],
      revealOnRoute: true,
      items: [
        { path: '/app/nodes', labelKey: 'items.nodes', icon: 'server', adminOnly: true, feature: 'distributed', signal: { key: 'nodes', kind: 'count' } },
        // Placement holds the rules and the failover chains as two views of one
        // page; the old failover address leads to the second view.
        { path: '/app/scheduling', labelKey: 'items.scheduling', icon: 'route', adminOnly: true, feature: 'distributed', match: ['/app/failover'] },
        { path: '/app/p2p', labelKey: 'items.p2p', icon: 'share', adminOnly: true },
      ],
      match: ['/app/node-backend-logs'],
    },
    {
      id: 'runtime',
      labelKey: 'admin.groups.runtime',
      items: [
        { path: '/app/backends', labelKey: 'items.backends', icon: 'boxes', adminOnly: true },
        { path: '/app/activity', labelKey: 'items.activity', icon: 'chart-line', adminOnly: true, signal: { key: 'activity', kind: 'count' } },
        { path: '/app/backend-logs', labelKey: 'items.logs', icon: 'terminal', adminOnly: true },
        // Failover is a Placement view on a cluster. A single install keeps it
        // here, where its model failover chains still apply.
        { path: '/app/failover', labelKey: 'items.failover', icon: 'shuffle', adminOnly: true, unlessFeature: 'distributed' },
        // What is out of date and what is changing: gallery and library
        // backends and add-ons. The number is what needs a person
        // (utils/updates badgeOf), from the shell-level UpdatesProvider.
        { path: '/app/updates', labelKey: 'hub.updates', icon: 'arrow-up', adminOnly: true, signal: { key: 'updates', kind: 'update' } },
      ],
    },
    {
      id: 'traffic',
      labelKey: 'hub.traffic',
      folded: true,
      signal: { key: 'traces', kind: 'error' },
      // Seven pages. Alerts is not among them: LocalAI has no alert rules, so
      // there is nothing for that page to hold.
      items: [
        { path: '/app/traffic', labelKey: 'items.overview', icon: 'chart-line', adminOnly: true, exact: true },
        { path: '/app/usage', labelKey: 'items.usage', icon: 'chart-bar', adminOnly: true },
        { path: '/app/traffic/models', labelKey: 'items.trafficModels', icon: 'cube', adminOnly: true },
        { path: '/app/traffic/host', labelKey: 'items.gpuHost', icon: 'cpu', adminOnly: true },
        { path: '/app/traces', labelKey: 'items.traces', icon: 'search', adminOnly: true },
        { path: '/app/middleware', labelKey: 'items.middleware', icon: 'layers', adminOnly: true },
        { path: '/app/traffic/prometheus', labelKey: 'items.prometheus', icon: 'gauge', adminOnly: true },
      ],
    },
    {
      id: 'access',
      labelKey: 'admin.groups.access',
      items: [
        { path: '/app/users', labelKey: 'items.users', icon: 'users', adminOnly: true, authOnly: true, signal: { key: 'pendingUsers', kind: 'count' } },
        { path: '/app/api-keys', labelKey: 'items.apiKeys', icon: 'key', adminOnly: true, authOnly: true },
        { path: '/app/chats-admin', labelKey: 'items.chatsAdmin', icon: 'chat', adminOnly: true, feature: 'chats' },
      ],
    },
    {
      id: 'settings',
      items: [{ path: '/app/settings', labelKey: 'items.settings', icon: 'settings', adminOnly: true }],
    },
  ],
  // Links that leave the app. Drawn at the end of the sidebar, not as an item.
  external: [
    { href: '/swagger/index.html', icon: 'external-link', labelKey: 'admin.nav.apiReference', adminOnly: true },
  ],
}

export const hubs = [legacyAddonsHub, operateHub]

// A hub draws either a tab bar (`tabs`) or, for Administration, the groups of
// its own sidebar (`groups`). Both have the same shape, so the gating below
// treats them alike.
function hubTabs(hub) {
  return hub.tabs || hub.groups || []
}

// Single source of truth for item visibility.
export function isHubItemVisible(item, { isAdmin, authEnabled, hasFeature, features }) {
  if (item.adminOnly && !isAdmin) return false
  if (item.authOnly && !authEnabled) return false
  if (item.requiresCollections && features.collections === false) return false
  if (item.feature && features[item.feature] === false) return false
  if (item.feature && !hasFeature(item.feature)) return false
  // Hidden until /api/features has answered: showing it and then swapping it
  // for the cluster tab would move the bar under the user's pointer.
  if (item.unlessFeature && flags(item.unlessFeature).some(f => features[f] !== false)) return false
  return true
}

function flags(v) {
  return Array.isArray(v) ? v : [v]
}

export function visibleItems(tab, auth) {
  const own = tab.items.filter(item => isHubItemVisible(item, auth))
  // Pages that add-ons contribute to navigation join the tab that owns them.
  // They come from the registry's usable add-ons only, so a disabled, failed
  // or incompatible add-on has no link. The server checks every call.
  if (tab.addonNavigation && own.length > 0 && Array.isArray(auth.addonNavigation)) {
    return [...own, ...auth.addonNavigation.map(item => ({ path: item.path, label: item.label, addonId: item.addonId }))]
  }
  return own
}

// A tab with a `showWhen` flag stays hidden until /api/features says it is on.
function tabGateOpen(tab, auth) {
  if (tab.showWhenOff && auth.features[tab.showWhenOff] !== false) return false
  return !tab.showWhen || flags(tab.showWhen).some(f => auth.features[f] === true)
}

export function isTabVisible(tab, auth) {
  return tabGateOpen(tab, auth) && visibleItems(tab, auth).length > 0
}

function pathIn(pathname, p) {
  return pathname === p || pathname.startsWith(p + '/')
}

export function tabOwnsPath(tab, pathname) {
  return tab.items.some(i => pathIn(pathname, i.path)) || (tab.match || []).some(p => pathIn(pathname, p))
}

// The tabs to draw for a viewer on `pathname`, in order, and which one is
// current. A tab that is gated off still shows while the viewer stands on one
// of its routes when it says `revealOnRoute`, so a direct link to such a page
// keeps its tab.
export function resolveTabs(hub, auth, pathname) {
  const shown = hubTabs(hub).filter(tab => isTabVisible(tab, auth))
  const owned = shown.some(tab => tabOwnsPath(tab, pathname))
  // Only when no drawn tab owns the route does a gated-off tab step in.
  const tabs = owned ? shown : hubTabs(hub).filter(tab => shown.includes(tab)
    || (tab.revealOnRoute && tab.items.some(i => (!i.adminOnly || auth.isAdmin) && pathIn(pathname, i.path))))
  // A tab whose visible items hold the route wins over a tab that only lists
  // it: with P2P on, This machine still lists /app/p2p (hidden) and Swarm
  // shows it.
  const current = tabs.find(tab => visibleItems(tab, auth).some(i => pathIn(pathname, i.path)))
    || tabs.find(tab => tabOwnsPath(tab, pathname)) || null
  return { tabs, current }
}

// With `auth`, a tab whose gate is closed owns nothing: the Add-ons tab of the
// Build hub only exists in the legacy navigation, so in the four-item sidebar
// /app/addons belongs to the Apps entry alone and must not light a hub entry.
export function hubOwnsPath(hub, pathname, auth) {
  return hubTabs(hub).some(tab => (!auth || tabGateOpen(tab, auth)) && tabOwnsPath(tab, pathname))
}

// The page the hub's single sidebar entry links to: its overview, when the
// viewer can see at least one tab besides it. Returns null when nothing is
// visible (so the entry can be hidden entirely).
export function hubEntryPath(hub, auth) {
  const real = hubTabs(hub).filter(tab => tab.id !== 'overview' && isTabVisible(tab, auth))
  if (real.length === 0) return null
  return hub.overviewPath
}

// Whether an item is the current page. An item with a `query` is current only
// when the address carries it; the item that shares its route and has none is
// current when the address carries none of the sibling queries.
export function itemActive(item, pathname, search = '', siblings = []) {
  const onRoute = (item.exact ? pathname === item.path : pathIn(pathname, item.path)) || (item.match || []).some(p => pathIn(pathname, p))
  if (!onRoute) return false
  const params = new URLSearchParams(search)
  const matches = (candidate) => Object.entries(Object.fromEntries(new URLSearchParams(candidate.query))).every(([k, v]) => params.get(k) === v)
  if (item.query) return matches(item)
  return !siblings.some(other => other !== item && other.query && other.path === item.path && matches(other))
}

// The Administration sidebar for a viewer on `location`: the visible groups,
// their visible items and the current item. The group of the current page is
// the `current` one and is open whatever `folded` says. A gated-off group
// that stays on screen because the viewer stands on one of its routes keeps
// `revealOnRoute` behaviour of resolveTabs.
export function resolveGroups(hub, auth, location) {
  const { pathname, search = '' } = location
  const { tabs } = resolveTabs(hub, auth, pathname)
  const groups = tabs.map(group => {
    const items = visibleItems(group, auth)
    // A hidden gated item that the viewer stands on (a revealed group) still
    // draws, so the page does not lose its own entry.
    const shown = items.length > 0 ? items : group.items.filter(i => pathIn(pathname, i.path))
    return { group, items: shown }
  })
  let current = null
  for (const entry of groups) {
    const hit = entry.items.find(item => itemActive(item, pathname, search, entry.items))
    if (hit) { current = { group: entry.group, item: hit }; break }
  }
  // A route that no visible item names but a group still owns (a sub-page
  // such as a node's logs) keeps its group open.
  const owner = current ? current.group : (groups.find(e => tabOwnsPath(e.group, pathname)) || {}).group || null
  return { groups, current, owner }
}
