import { Suspense, useCallback, useMemo, useState } from 'react'
import { Outlet, useLocation, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import RouteFallback from '../RouteFallback'
import { HubHeaderContext } from '../hub/hubHeaderContext'
import { useHeaderSlots } from '../hub/useHeaderSlots'
import { operateHub, resolveGroups } from '../hub/hubConfig'
import { useHubAuth } from '../hub/useHubAuth'
import { AdminShellContext } from './adminShellContext'
import AdminCrumbs from './AdminCrumbs'
import CommandPalette from './CommandPalette'
import './admin.css'

// The page frame of Administration. There is no tab bar: the sidebar
// (AdminSidebar) names the groups and the pages, and this layout adds the
// breadcrumb, the command palette and a header for the pages that portal a
// line and actions into it (HubHeader). A page built with AdminPage draws the
// breadcrumb in its own header instead.
export default function AdminLayout() {
  const { t } = useTranslation('nav')
  const auth = useHubAuth()
  const location = useLocation()
  // Forward the App-level outlet context (e.g. addToast): a nested bare
  // <Outlet/> would otherwise shadow it with undefined and crash pages.
  const outletContext = useOutletContext()
  const slots = useHeaderSlots()
  const [claimed, setClaimed] = useState(0)
  const claimCrumbs = useCallback((delta) => setClaimed(n => Math.max(0, n + delta)), [])

  const { current, owner } = resolveGroups(operateHub, auth, location)
  const crumbs = useMemo(() => {
    // A person who is not an administrator can reach Usage: it is not part of
    // an Administration they cannot see, so it has no trail.
    if (!auth.isAdmin) return []
    const trail = [{ label: t('sections.operate'), to: operateHub.overviewPath }]
    const group = (current && current.group) || owner
    if (group && group.id !== 'landing' && group.id !== 'settings' && group.labelKey) trail.push({ label: t(group.labelKey) })
    if (current && current.item.path !== operateHub.overviewPath) trail.push({ label: t(current.item.labelKey), to: current.item.path })
    return trail
  }, [auth.isAdmin, current, owner, t])
  const shell = useMemo(() => ({ crumbs, claimCrumbs }), [crumbs, claimCrumbs])

  return (
    <AdminShellContext.Provider value={shell}>
      <HubHeaderContext.Provider value={slots.value}>
        <div className="admin-layout" data-hub="operate">
          {claimed === 0 && (
            <div className="admin-frame">
              <AdminCrumbs />
            </div>
          )}
          <div className="admin-frame admin-head" data-testid="admin-head">
            <div className="admin-head__slot" ref={slots.setSupporting} />
            <div className="admin-head__actions" ref={slots.setActions} />
          </div>
          <div className="admin-body" key={location.pathname}>
            {/* Own Suspense so a lazy page shows the loader in the body while
                the sidebar stays put. */}
            <Suspense fallback={<RouteFallback />}>
              <Outlet context={outletContext} />
            </Suspense>
          </div>
          {auth.isAdmin && <CommandPalette />}
        </div>
      </HubHeaderContext.Provider>
    </AdminShellContext.Provider>
  )
}
