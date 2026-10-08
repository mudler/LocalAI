import { Suspense } from 'react'
import { Link, Outlet, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../../utils/basePath'
import { preloadRoute } from '../../router'
import RouteFallback from '../RouteFallback'
import { resolveTabs, visibleItems } from './hubConfig'
import { useHubAuth } from './useHubAuth'
import { OperateSummaryProvider, useOperateSummary } from '../../contexts/OperateSummaryContext'
import Icon from '../Icon'

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
      {tab.icon && <Icon name={tab.icon} aria-hidden="true" />}
      <span>{label}</span>
      {(tab.signals || []).map(signal => (
        <TabBadge key={signal.key} signal={signal} summary={summary} />
      ))}
    </Link>
  )
}

// The second row, for a tab that owns several routes. Quiet text links, so the
// bar above stays the only strong navigation.
function SubNav({ tab, auth, pathname, t }) {
  const items = visibleItems(tab, auth)
  if (items.length < 2) return null
  return (
    <nav className="hub-subnav" aria-label={t(tab.labelKey)}>
      {items.map(item => {
        // An item marked `exact` is the hub's own landing page: the pages under
        // it have items of their own, so a prefix match would light two links.
        const active = item.exact ? pathname === item.path : (pathname === item.path || pathname.startsWith(item.path + '/'))
        return (
          <Link
            key={item.path}
            to={item.path}
            className="hub-subnav__link"
            aria-current={active ? 'page' : undefined}
            onMouseEnter={() => preloadRoute(item.path)}
            onFocus={() => preloadRoute(item.path)}
          >
            {t(item.labelKey)}
          </Link>
        )
      })}
    </nav>
  )
}

function HubLayoutInner({ config }) {
  const { t } = useTranslation('nav')
  const auth = useHubAuth()
  const location = useLocation()
  const summary = useOperateSummary()
  // Forward the App-level outlet context (e.g. addToast): a nested bare
  // <Outlet/> would otherwise shadow it with undefined and crash pages.
  const outletContext = useOutletContext()

  const { tabs, current } = resolveTabs(config, auth, location.pathname)
  const external = (config.external || []).filter(item => !item.adminOnly || auth.isAdmin)

  return (
    <div className="hub-layout" data-hub={config.id}>
      <div className="hub-bar">
        <nav className="dk-hubtabs" aria-label={t(config.titleKey)}>
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
              <Icon name={item.icon} aria-hidden="true" />
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
