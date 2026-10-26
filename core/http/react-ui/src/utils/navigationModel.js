// Pure rules of the four-item sidebar. No React, no imports, so a node test
// drives it directly.

// Chat, Models and Add-ons. Home and the Build and Operate hubs keep the place
// they have today (see hubConfig.js and destinations.js). Settings is a tab of
// the Operate hub, not a primary item.
export const primaryDestinations = Object.freeze([
  { path: '/app/chat', icon: 'chat', labelKey: 'items.chat' },
  // Chats the server keeps. Shown to a viewer who holds the chats feature.
  { path: '/app/chats', icon: 'chat', labelKey: 'items.chats', feature: 'chats' },
  // The user's own files: a workspace of folders, with a Trash and history.
  { path: '/app/files', icon: 'folder', labelKey: 'items.files', feature: 'chats' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
  // Shown to a viewer who holds the addons feature or sees a built-in tile.
  { path: '/app/addons', icon: 'puzzle', labelKey: 'items.addons', addonsEntry: true },
])

// `features` is the /api/features map. An absent `addons` key (an older server)
// counts as on; only an explicit false hides the Add-ons entry.
export function visiblePrimary(items, { isAdmin, hasFeature, hasBuiltin, features }) {
  return items.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.feature && !hasFeature(item.feature)) return false
    if (item.addonsEntry && features?.addons === false) return false
    if (item.addonsEntry && !isAdmin && !hasFeature('addons') && !hasBuiltin) return false
    return true
  })
}
