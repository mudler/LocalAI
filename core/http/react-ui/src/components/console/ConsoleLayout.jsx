import { useState, useEffect, Suspense } from 'react'
import { NavLink, Outlet, useOutletContext, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { apiUrl } from '../../utils/basePath'
import { preloadRoute } from '../../router'
import RouteFallback from '../RouteFallback'
import { isConsoleItemVisible } from './consoleConfig'

// The App wraps the outlet in key={pathname}, so this layout remounts on every
// sub-navigation. Tracking the last-entered console id across mounts lets us
// play the rail's entrance animation only when actually entering a console
// (from outside), not when switching items within it — otherwise it flashes.
let lastConsoleId = null

// Generic secondary-rail layout shared by the Build and Operate consoles.
// Driven entirely by a config from consoleConfig.js, so the rail, its gating,
// and the sidebar entry that opens it stay in sync. Mounted as a PATHLESS
// route in router.jsx — wrapped pages keep their existing flat URLs.

function RailItem({ item, label }) {
  if (item.external) {
    return (
      <a className="nav-item" href={apiUrl(item.href)} target="_blank" rel="noopener noreferrer">
        <i className={`${item.icon} nav-icon`} />
        <span className="nav-label">{label}</span>
        <i className="fas fa-external-link-alt nav-external" />
      </a>
    )
  }
  return (
    <NavLink
      to={item.path}
      className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}
      onMouseEnter={() => preloadRoute(item.path)}
      onFocus={() => preloadRoute(item.path)}
    >
      <i className={`${item.icon} nav-icon`} />
      <span className="nav-label">{label}</span>
    </NavLink>
  )
}

export default function ConsoleLayout({ config }) {
  const { t } = useTranslation('nav')
  const { isAdmin, authEnabled, hasFeature } = useAuth()
  const [features, setFeatures] = useState({})
  const location = useLocation()
  // Forward the App-level outlet context (e.g. addToast) — a nested bare
  // <Outlet/> would otherwise shadow it with undefined and crash pages.
  const outletContext = useOutletContext()
  // True only when entering this console fresh; false on item-to-item nav.
  const [entering] = useState(() => {
    const fresh = lastConsoleId !== config.id
    lastConsoleId = config.id
    return fresh
  })

  useEffect(() => {
    fetch(apiUrl('/api/features')).then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

  const auth = { isAdmin, authEnabled, hasFeature, features }

  return (
    <div className="console-layout">
      <nav className={`console-rail${entering ? ' console-rail--enter' : ''}`} aria-label={t(config.titleKey)}>
        <div className="console-rail-header">
          <i className={config.icon} aria-hidden="true" />
          <span>{t(config.titleKey)}</span>
        </div>
        {config.groups.map((group, gi) => {
          const items = group.items.filter(item => isConsoleItemVisible(item, auth))
          if (items.length === 0) return null
          return (
            <div key={group.titleKey || gi} className="console-group">
              {group.titleKey && <div className="console-group-title">{t(group.titleKey)}</div>}
              {items.map(item => (
                <RailItem key={item.path || item.href} item={item} label={t(item.labelKey)} />
              ))}
            </div>
          )
        })}
      </nav>
      <div className="console-body" key={location.pathname}>
        {/* Own Suspense so a lazy page shows the loader in the body while the
            rail stays put (instead of bubbling to App's boundary). */}
        <Suspense fallback={<RouteFallback />}>
          <Outlet context={outletContext} />
        </Suspense>
      </div>
    </div>
  )
}
