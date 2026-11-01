// Hub configuration shared by HubLayout (renders the tab bar), the Sidebar
// (renders the single entry for each hub and computes its active state), App
// (page-transition key).
//
// A hub is a page-level tab bar. Each tab owns one or more existing routes
// (`items`). A tab with several routes shows a second, quieter row of links
// under the bar. Routes stay flat (`/app/nodes/:id`); a tab is
// highlighted when the current path is one of its item paths or sits under
// one, so sub-pages such as `nodes/:id` keep the right tab lit.
//
// Gating is per item and decides the tab: a tab shows when at least one of its
// items is visible to the viewer, so a tab nobody can use is never drawn.
// `/api/features` only emits: collections, mcp, fine_tuning, quantization,
// distributed, backend_install, localai_assistant. Capability-style flags (face_recognition,
// skills, ...) come from hasFeature(), not the features map.
//
// Item and tab fields:
//   path          route the item links to
//   labelKey      `nav` i18n key of the item label
//   adminOnly, authOnly, feature, requiresCollections, unlessFeature  gating
//   match         extra path prefixes that belong to the tab (sub-pages)
//   showWhen      tab only: `/api/features` flag that must be on
//   showWhenOff   tab only: `/api/features` flag that must be off (answered false)
//   revealOnRoute tab only: draw the tab while the viewer is on one of its
//                 routes, even when `showWhen` is off
//   signals       tab only: values from the Operate summary (see
//                 OperateSummaryContext), each drawn as a neutral count
//                 (kind `count`) or an attention badge (kind `attention`,
//                 or `error` for failures)

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

// Six tabs for what used to be thirteen rail rows in four groups. Nothing is
// removed and no gate changes. Where several routes share a tab, the second
// row names them.
export const operateHub = {
  id: 'operate',
  titleKey: 'sections.operate',
  icon: 'sliders',
  overviewPath: '/app/operate',
  // The hub draws one page header above its tabs. A page fills the supporting
  // line and the actions with <HubHeader>; this line shows when it does not.
  header: { supportingKey: 'hub.operateSupporting' },
  tabs: [
    {
      id: 'status',
      labelKey: 'hub.status',
      icon: 'gauge',
      signals: [{ key: 'attention', kind: 'attention' }],
      items: [{ path: '/app/operate', labelKey: 'hub.status', adminOnly: true }],
    },
    {
      id: 'machine',
      labelKey: 'items.thisMachine',
      icon: 'monitor',
      // The Nodes route under the name it has on a single-node install, where
      // it shows this host and what is loaded on it. With distributed mode on,
      // the Swarm tab takes over the same route.
      signals: [{ key: 'running', kind: 'count' }],
      items: [{ path: '/app/nodes', labelKey: 'items.thisMachine', adminOnly: true, unlessFeature: 'distributed' }],
      match: ['/app/node-backend-logs'],
    },
    {
      id: 'swarm',
      labelKey: 'items.swarm',
      icon: 'network',
      showWhen: 'distributed',
      revealOnRoute: true,
      signals: [{ key: 'nodes', kind: 'count' }],
      items: [
        { path: '/app/nodes', labelKey: 'items.nodes', adminOnly: true, feature: 'distributed' },
        { path: '/app/scheduling', labelKey: 'items.scheduling', adminOnly: true, feature: 'distributed' },
        // Failover is a Swarm page on a cluster. A single install keeps it under
        // Runtime, where its model failover chains still apply.
        { path: '/app/failover', labelKey: 'items.failover', adminOnly: true, feature: 'distributed' },
        { path: '/app/p2p', labelKey: 'items.p2p', adminOnly: true },
      ],
      match: ['/app/node-backend-logs'],
    },
    {
      id: 'runtime',
      labelKey: 'hub.runtime',
      icon: 'server',
      signals: [{ key: 'backends', kind: 'attention' }, { key: 'activity', kind: 'count' }],
      items: [
        { path: '/app/backends', labelKey: 'items.backends', adminOnly: true },
        { path: '/app/activity', labelKey: 'items.activity', adminOnly: true },
        { path: '/app/backend-logs', labelKey: 'items.logs', adminOnly: true },
        { path: '/app/failover', labelKey: 'items.failover', adminOnly: true, unlessFeature: 'distributed' },
      ],
    },
    {
      id: 'traffic',
      labelKey: 'hub.traffic',
      icon: 'chart-line',
      signals: [{ key: 'traces', kind: 'error' }],
      // Eight pages behind one tab. Alerts is not among them: LocalAI has no
      // alert rules, so there is nothing for that page to hold.
      items: [
        { path: '/app/traffic', labelKey: 'items.overview', adminOnly: true, exact: true },
        { path: '/app/usage', labelKey: 'items.usage', adminOnly: true },
        { path: '/app/traffic/models', labelKey: 'items.trafficModels', adminOnly: true },
        { path: '/app/traffic/host', labelKey: 'items.gpuHost', adminOnly: true },
        { path: '/app/traces', labelKey: 'items.traces', adminOnly: true },
        { path: '/app/middleware', labelKey: 'items.middleware', adminOnly: true },
        { path: '/app/traffic/prometheus', labelKey: 'items.prometheus', adminOnly: true },
      ],
    },
    {
      id: 'settings',
      labelKey: 'items.settings',
      icon: 'settings',
      items: [
        { path: '/app/settings', labelKey: 'items.settings', adminOnly: true },
        { path: '/app/users', labelKey: 'items.users', adminOnly: true, authOnly: true },
        { path: '/app/chats-admin', labelKey: 'items.chatsAdmin', adminOnly: true, feature: 'chats' },
      ],
    },
  ],
  // Links that leave the app. Drawn at the end of the bar, not as a tab.
  external: [
    { href: '/swagger/index.html', icon: 'code', labelKey: 'items.api', adminOnly: true },
  ],
}

export const hubs = [legacyAddonsHub, operateHub]

// Single source of truth for item visibility.
export function isHubItemVisible(item, { isAdmin, authEnabled, hasFeature, features }) {
  if (item.adminOnly && !isAdmin) return false
  if (item.authOnly && !authEnabled) return false
  if (item.requiresCollections && features.collections === false) return false
  if (item.feature && features[item.feature] === false) return false
  if (item.feature && !hasFeature(item.feature)) return false
  // Hidden until /api/features has answered: showing it and then swapping it
  // for the cluster tab would move the bar under the user's pointer.
  if (item.unlessFeature && features[item.unlessFeature] !== false) return false
  return true
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
  return !tab.showWhen || auth.features[tab.showWhen] === true
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
  const shown = hub.tabs.filter(tab => isTabVisible(tab, auth))
  const owned = shown.some(tab => tabOwnsPath(tab, pathname))
  // Only when no drawn tab owns the route does a gated-off tab step in.
  const tabs = owned ? shown : hub.tabs.filter(tab => shown.includes(tab)
    || (tab.revealOnRoute && tab.items.some(i => (!i.adminOnly || auth.isAdmin) && pathIn(pathname, i.path))))
  const current = tabs.find(tab => tabOwnsPath(tab, pathname)) || null
  return { tabs, current }
}

// With `auth`, a tab whose gate is closed owns nothing: the Add-ons tab of the
// Build hub only exists in the legacy navigation, so in the four-item sidebar
// /app/addons belongs to the Apps entry alone and must not light a hub entry.
export function hubOwnsPath(hub, pathname, auth) {
  return hub.tabs.some(tab => (!auth || tabGateOpen(tab, auth)) && tabOwnsPath(tab, pathname))
}

// The page the hub's single sidebar entry links to: its overview, when the
// viewer can see at least one tab besides it. Returns null when nothing is
// visible (so the entry can be hidden entirely).
export function hubEntryPath(hub, auth) {
  const real = hub.tabs.filter(tab => tab.id !== 'overview' && isTabVisible(tab, auth))
  if (real.length === 0) return null
  return hub.overviewPath
}
