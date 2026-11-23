import { Suspense, useEffect, useRef, useState } from 'react'
import { Link, Outlet, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { preloadRoute } from '../../router'
import RouteFallback from '../RouteFallback'
import { resolveTabs, visibleItems } from './hubConfig'
import { useHubAuth } from './useHubAuth'
import { useAddonNavigation, useSlotContributions } from '../../addons/useAddonRegistry'
import './hub.css'
// eslint-disable-next-line no-unused-vars
import AddonSlot from '../../addons/AddonSlot'

function HubTab({ tab, current, auth, t }) {
  const items = visibleItems(tab, auth)
  // A tab links to its first visible route. A tab that is only on screen
  // because the viewer stands on one of its routes links to the route they
  // are on.
  const target = (items[0] || tab.items[0]).path
  const label = t(tab.labelKey)
  return (
    <Link
      to={target}
      className="dk-hubtab"
      aria-current={current ? 'page' : undefined}
      data-hub-tab={tab.id}
      onMouseEnter={() => preloadRoute(target)}
      onFocus={() => preloadRoute(target)}
    >
      <span>{label}</span>
    </Link>
  )
}

// The second level, for a tab that owns several routes: a segmented control,
// so it never reads as a second row of tabs.
function SubNav({ tab, auth, pathname, t }) {
  const items = visibleItems(tab, auth)
  // Add-on components in the app.nav slot join the second row of the hub that
  // lists add-on navigation; the slot draws nothing when none is contributed.
  const navSlot = useSlotContributions('app.nav', Boolean(tab.addonNavigation))
  if (items.length < 2 && navSlot.length === 0) return null
  return (
    <nav className="hub-subnav" aria-label={t(tab.labelKey)}>
      {items.map(item => {
        // An item marked `exact` is the hub's own landing page: the pages under
        // it have items of their own, so a prefix match would light two links.
        const active = item.exact ? pathname === item.path : (pathname === item.path || pathname.startsWith(item.path + '/'))
        // An add-on page link carries its own title, not an interface key.
        const label = item.label || t(item.labelKey)
        return (
          <Link
            key={item.path}
            to={item.path}
            className="hub-subnav__link"
            aria-current={active ? 'page' : undefined}
            onMouseEnter={() => preloadRoute(item.path)}
            onFocus={() => preloadRoute(item.path)}
          >
            {label}
          </Link>
        )
      })}
      {tab.addonNavigation && <AddonSlot slot="app.nav" context={null} />}
    </nav>
  )
}

// A tab row that scrolls sideways fades at the edge that has more, and brings
// the current tab into view when the route changes.
function useTabRow(pathname) {
  const ref = useRef(null)
  const [fade, setFade] = useState('')
  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    const measure = () => {
      const max = el.scrollWidth - el.clientWidth
      const parts = []
      if (max > 1 && el.scrollLeft > 1) parts.push('start')
      if (max > 1 && el.scrollLeft < max - 1) parts.push('end')
      setFade(parts.join(' '))
    }
    const currentTab = el.querySelector('[aria-current="page"]')
    if (currentTab && el.scrollWidth > el.clientWidth) {
      const left = currentTab.offsetLeft
      const right = left + currentTab.offsetWidth
      if (left < el.scrollLeft || right > el.scrollLeft + el.clientWidth) {
        el.scrollLeft = Math.max(0, left - 24)
      }
    }
    measure()
    el.addEventListener('scroll', measure, { passive: true })
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null
    observer?.observe(el)
    return () => {
      el.removeEventListener('scroll', measure)
      observer?.disconnect()
    }
  }, [pathname])
  return { ref, fade }
}

function HubLayoutInner({ config }) {
  const { t } = useTranslation('nav')
  const hubAuth = useHubAuth()
  // Add-on navigation is read only on the hub that lists it, so the registry
  // does not poll on pages that never show it.
  const addonNavigation = useAddonNavigation(config.id === 'addons')
  const auth = config.id === 'addons' ? { ...hubAuth, addonNavigation } : hubAuth
  const location = useLocation()
  // Forward the App-level outlet context (e.g. addToast): a nested bare
  // <Outlet/> would otherwise shadow it with undefined and crash pages.
  const outletContext = useOutletContext()
  const { ref: tabsRef, fade } = useTabRow(location.pathname)

  const { tabs, current } = resolveTabs(config, auth, location.pathname)
  return (
    <div className="hub-layout" data-hub={config.id}>
      <div className="hub-bar">
        <nav className="dk-hubtabs" aria-label={t(config.titleKey)} ref={tabsRef} data-fade={fade || undefined}>
          {tabs.map(tab => (
            <HubTab key={tab.id} tab={tab} current={current?.id === tab.id} auth={auth} t={t} />
          ))}
        </nav>
        {current && <SubNav tab={current} auth={auth} pathname={location.pathname} t={t} />}
      </div>
      <div className="hub-body" key={location.pathname}>
        {/* Own Suspense so a lazy page shows the loader in the body while the
            tab bar stays put (instead of bubbling to App's boundary). */}
        <Suspense fallback={<RouteFallback />}>
          <Outlet context={outletContext} />
        </Suspense>
      </div>
    </div>
  )
}

export default function HubLayout({ config }) {
  return <HubLayoutInner config={config} />
}
