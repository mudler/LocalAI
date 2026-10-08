// The places the sidebar lists, as data, so the sidebar and the 404 page agree
// on what exists and who may see it. Each hub is one destination, opening on
// its overview; it is listed only when the viewer can see a tab in it.
import { hubs, hubEntryPath } from '../components/hub/hubConfig'

export const topDestinations = [
  { path: '/app', icon: 'home', labelKey: 'items.home' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
]

export const createDestinations = [
  { path: '/app/chat', icon: 'chat', labelKey: 'items.chat' },
  { path: '/app/studio', icon: 'palette', labelKey: 'items.studio' },
  { path: '/app/talk', icon: 'phone', labelKey: 'items.talk' },
]

function visible(item, { isAdmin, authEnabled, hasFeature, features }) {
  if (item.adminOnly && !isAdmin) return false
  if (item.authOnly && !authEnabled) return false
  if (item.feature && features[item.feature] === false) return false
  if (item.feature && !hasFeature(item.feature)) return false
  return true
}

// Home, Models, Chat, Studio, Talk and the hubs, in the sidebar's order.
export function destinationsFor(auth) {
  const hubItems = hubs.flatMap(hub => {
    const path = hubEntryPath(hub, auth)
    return path ? [{ path, icon: hub.icon, labelKey: hub.titleKey }] : []
  })
  return [
    ...topDestinations.filter(i => visible(i, auth)),
    ...createDestinations.filter(i => visible(i, auth)),
    ...hubItems,
  ]
}
