// Pure rules of the four-item sidebar. No React, no imports, so a node test
// drives it directly.

// Chat, Models, Add-ons and Settings. Home and the Build and Operate hubs keep
// the place they have today (see hubConfig.js and destinations.js).
export const primaryDestinations = Object.freeze([
  { path: '/app/chat', icon: 'chat', labelKey: 'items.chat' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
  // Shown to a viewer who holds the addons feature or sees a built-in tile.
  { path: '/app/addons', icon: 'puzzle', labelKey: 'items.addons', addonsEntry: true },
  { path: '/app/settings', icon: 'settings', labelKey: 'items.settings', adminOnly: true },
])

export function visiblePrimary(items, { isAdmin, hasFeature, hasBuiltin }) {
  return items.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.addonsEntry && !isAdmin && !hasFeature('addons') && !hasBuiltin) return false
    return true
  })
}
