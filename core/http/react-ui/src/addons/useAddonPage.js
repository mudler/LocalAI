import { useEffect } from 'react'
import { useAddonRegistry } from './useAddonRegistry'
import { appStatus } from './registryState'

// The address of a page of an installed add-on, or null while the add-on is
// absent, disabled, failed or unusable. A caller that links to an add-on page
// (a command, a button) shows the link only when this answers an address, so a
// person who has not installed the add-on never sees a dead end.
export function useAddonPage(addonId, page) {
  const { apps, acquire, allowed } = useAddonRegistry()
  // The list is read only for a user the server lets see add-ons.
  useEffect(() => (allowed ? acquire() : undefined), [acquire, allowed])
  const app = apps.find(a => a.id === addonId)
  const ready = appStatus(app) === 'ready' && Array.isArray(app.ui.pages) && app.ui.pages.includes(page)
  return ready ? `/app/addons/${encodeURIComponent(addonId)}/${encodeURIComponent(page)}` : null
}
