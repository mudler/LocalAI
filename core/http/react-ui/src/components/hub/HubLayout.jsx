import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, Outlet, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../../utils/basePath'
import { preloadRoute } from '../../router'
import RouteFallback from '../RouteFallback'
import { resolveTabs, visibleItems } from './hubConfig'
import { useHubAuth } from './useHubAuth'
import { OperateSummaryProvider, useOperateSummary } from '../../contexts/OperateSummaryContext'
import Icon from '../Icon'
import { useAddonNavigation, useSlotContributions } from '../../addons/useAddonRegistry'
// eslint-disable-next-line no-unused-vars
import { HubHeaderContext } from './hubHeaderContext'
import './hub.css'
// eslint-disable-next-line no-unused-vars
import AddonSlot from '../../addons/AddonSlot'

// Signals come from the Operate summary and only exist inside Operate. They
// are never the only place a fact appears: the Status page states the same
// things in prose. The attention count is the one that asks for a look.
function TabBadge({ signal, summary }) {
  const value = signal && summary?.signals ? summary.signals[signal.key] : null
  if (value == null) return null
  if (signal.kind === 'attention') {
    return <span className="dk-hubtab-attn">{value}</span>
  }
  if (signal.kind === 'error') {
    return <span className="dk-hubtab-attn" data-level="error" title={String(value)}>{value}</span>
  }
  return <span className="dk-hubtab-count">{value}</span>
}

function HubTab({ tab, current, auth, summary, t }) {
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
      {(tab.signals || []).map(signal => (
        <TabBadge key={signal.key} signal={signal} summary={summary} />
      ))}
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

// The nodes the page portals its supporting line and actions into, and how
// many pages fill the line (the hub's own line shows only when none does).
function useHeaderSlots() {
  const [supporting, setSupporting] = useState(null)
  const [actions, setActions] = useState(null)
  const [filled, setFilled] = useState(0)
  const claim = useCallback((delta) => setFilled(n => Math.max(0, n + delta)), [])
  const value = useMemo(() => ({ supporting, actions, claim }), [supporting, actions, claim])
  return { value, filled, setSupporting, setActions }
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
  const summary = useOperateSummary()
  // Forward the App-level outlet context (e.g. addToast): a nested bare
  // <Outlet/> would otherwise shadow it with undefined and crash pages.
  const outletContext = useOutletContext()
  const slots = useHeaderSlots()
  const { ref: tabsRef, fade } = useTabRow(location.pathname)

  const { tabs, current } = resolveTabs(config, auth, location.pathname)
  const external = (config.external || []).filter(item => !item.adminOnly || auth.isAdmin)
  const header = config.header ? (
    <header className="hub-head">
      <div className="hub-head__lead">
        {/* Not a heading: the page under the tabs owns the one h1. The tab
            row is labelled with the same name for assistive technology. */}
        <p className="hub-title">{t(config.titleKey)}</p>
        <div className="hub-head__supporting" data-testid="hub-supporting">
          {slots.filled === 0 && config.header.supportingKey
            ? <span className="hub-head__default">{t(config.header.supportingKey)}</span>
            : null}
          {/* The page's line is portalled here; the node has no React children. */}
          <div className="hub-head__slot" ref={slots.setSupporting} />
        </div>
      </div>
      <div className="hub-head__actions" ref={slots.setActions} />
    </header>
  ) : null

  return (
    <HubHeaderContext.Provider value={slots.value}>
    <div className="hub-layout" data-hub={config.id}>
      <div className="hub-bar">
        {header}
        <nav className="dk-hubtabs" aria-label={t(config.titleKey)} ref={tabsRef} data-fade={fade || undefined}>
          {tabs.map(tab => (
            <HubTab key={tab.id} tab={tab} current={current?.id === tab.id} auth={auth} summary={summary} t={t} />
          ))}
          {external.map(item => (
            <a
              key={item.href}
              className="dk-hubtab"
              href={apiUrl(item.href)}
              target="_blank"
              rel="noopener noreferrer"
            >
              <span>{t(item.labelKey)}</span>
              <Icon name="external-link" aria-hidden="true" />
            </a>
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
    </HubHeaderContext.Provider>
  )
}

// The summary provider wraps the Operate hub and nothing else. That is the
// whole of "poll only while the user is in Operate": elsewhere the provider is
// not mounted, so no timer exists to gate. Build gets the plain layout.
export default function HubLayout({ config }) {
  if (config.id !== 'operate') return <HubLayoutInner config={config} />
  return (
    <OperateSummaryProvider>
      <HubLayoutInner config={config} />
    </OperateSummaryProvider>
  )
}
