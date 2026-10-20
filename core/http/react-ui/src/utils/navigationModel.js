// Pure rules of the four-item sidebar. No React, no imports, so a node test
// drives it directly.

// Chat, Models and Add-ons. Home and the Build and Operate hubs keep the place
// they have today (see hubConfig.js and destinations.js). Settings is a tab of
// the Operate hub, not a primary item.
export const primaryDestinations = Object.freeze([
  { path: '/app/chat', icon: 'chat', labelKey: 'items.chat' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
  // Shown to a viewer who holds the addons feature or sees a built-in tile.
  { path: '/app/addons', icon: 'puzzle', labelKey: 'items.addons', addonsEntry: true },
])

export function visiblePrimary(items, { isAdmin, hasFeature, hasBuiltin }) {
  return items.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.addonsEntry && !isAdmin && !hasFeature('addons') && !hasBuiltin) return false
    return true
  })
}
