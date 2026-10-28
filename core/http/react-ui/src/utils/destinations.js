// The places the sidebar lists, as data, so the sidebar and the 404 page agree
// on what exists and who may see it. Each hub is one destination, opening on
// its overview; it is listed only when the viewer can see a tab in it.
import { hubs, hubEntryPath } from '../components/hub/hubConfig'
import { primaryDestinations, visiblePrimary } from './navigationModel.js'

// Studio and Talk are add-ons: they are listed in the Add-ons drawer when they
// are installed, not here.
// The legacy layout has no Build hub any more, so its list carries the pages
// that hub used to hold: agents, MCP connectors and memory. The add-ons drawer
// is the legacy Add-ons hub (hubConfig.js).
export const topDestinations = [
  { path: '/app', icon: 'home', labelKey: 'items.home' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
  { path: '/app/chats', icon: 'chat', labelKey: 'items.chats', feature: 'chats' },
  { path: '/app/agents', icon: 'robot', labelKey: 'items.agents', feature: 'agents' },
  { path: '/app/mcp-connectors', icon: 'plug', labelKey: 'items.mcpConnectors', feature: 'chats' },
  { path: '/app/collections', icon: 'database', labelKey: 'items.memory', feature: 'collections' },
]

function visible(item, { isAdmin, authEnabled, hasFeature, features }) {
  if (item.adminOnly && !isAdmin) return false
  if (item.authOnly && !authEnabled) return false
  if (item.feature && features[item.feature] === false) return false
  if (item.feature && !hasFeature(item.feature)) return false
  return true
}

// Home, Models, Chat and the hubs, in the sidebar's order.
export function destinationsFor(auth) {
  const hubItems = hubs.flatMap(hub => {
    const path = hubEntryPath(hub, auth)
    return path ? [{ path, icon: hub.icon, labelKey: hub.titleKey }] : []
  })
  return [
    ...topDestinations.filter(i => visible(i, auth)),
    ...hubItems,
  ]
}

// The four-item layout: Home, then Chat, Models and Add-ons, then the hubs.
export function destinationsForNavigation(auth) {
  const hubItems = hubs.flatMap(hub => {
    const path = hubEntryPath(hub, auth)
    return path ? [{ path, icon: hub.icon, labelKey: hub.titleKey }] : []
  })
  return [
    ...topDestinations.filter(i => i.path === '/app'),
    ...visiblePrimary(primaryDestinations, auth),
    ...hubItems,
  ]
}
