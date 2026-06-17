import { useState, useEffect } from 'react'
import { NavLink, Outlet, useLocation, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { apiUrl } from '../../utils/basePath'
import { preloadRoute } from '../../router'

// The admin console groups every operator destination by the job it serves.
// Item gating mirrors the sidebar's: adminOnly / authOnly / feature flags +
// the global /api/features map. This layout is mounted as a PATHLESS route in
// router.jsx, so the wrapped admin pages keep their existing flat URLs.
const groups = [
  {
    titleKey: 'operate.inference',
    items: [
      { path: '/app/models', icon: 'fas fa-download', labelKey: 'items.installModels', adminOnly: true },
      { path: '/app/backends', icon: 'fas fa-server', labelKey: 'items.backends', adminOnly: true },
    ],
  },
  {
    titleKey: 'operate.cluster',
    items: [
      { path: '/app/nodes', icon: 'fas fa-network-wired', labelKey: 'items.nodes', adminOnly: true, feature: 'distributed' },
      { path: '/app/p2p', icon: 'fas fa-circle-nodes', labelKey: 'items.swarm', adminOnly: true },
    ],
  },
  {
    titleKey: 'operate.observability',
    items: [
      { path: '/app/usage', icon: 'fas fa-chart-bar', labelKey: 'items.usage', adminOnly: true },
      { path: '/app/traces', icon: 'fas fa-chart-line', labelKey: 'items.traces', adminOnly: true },
    ],
  },
  {
    titleKey: 'operate.access',
    items: [
      { path: '/app/users', icon: 'fas fa-users', labelKey: 'items.users', adminOnly: true, authOnly: true },
      { path: '/app/middleware', icon: 'fas fa-shield-halved', labelKey: 'items.security', adminOnly: true },
    ],
  },
  {
    titleKey: 'operate.system',
    items: [
      { path: '/app/manage', icon: 'fas fa-desktop', labelKey: 'items.host', adminOnly: true },
      { path: '/app/settings', icon: 'fas fa-cog', labelKey: 'items.settings', adminOnly: true },
    ],
  },
]

function RailItem({ item, t }) {
  return (
    <NavLink
      to={item.path}
      className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}
      onMouseEnter={() => preloadRoute(item.path)}
      onFocus={() => preloadRoute(item.path)}
    >
      <i className={`${item.icon} nav-icon`} />
      <span className="nav-label">{t(item.labelKey)}</span>
    </NavLink>
  )
}

export default function AdminConsoleLayout() {
  const { t } = useTranslation('nav')
  const { isAdmin, authEnabled, hasFeature } = useAuth()
  const [features, setFeatures] = useState({})
  const location = useLocation()
  // Forward the App-level outlet context (e.g. { addToast }) down to the
  // wrapped admin pages, which read it via useOutletContext(). Without this the
  // nested Outlet would shadow it with `undefined` and break the pages.
  const outletContext = useOutletContext()

  useEffect(() => {
    fetch(apiUrl('/api/features')).then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

  const visible = (item) => {
    if (item.adminOnly && !isAdmin) return false
    if (item.authOnly && !authEnabled) return false
    if (item.feature && features[item.feature] === false) return false
    if (item.feature && !hasFeature(item.feature)) return false
    return true
  }

  return (
    <div className="admin-console">
      <nav className="admin-console-rail" aria-label={t('sections.operate')}>
        {groups.map(group => {
          const items = group.items.filter(visible)
          if (items.length === 0) return null
          return (
            <div key={group.titleKey} className="admin-console-group">
              <div className="admin-console-group-title">{t(group.titleKey)}</div>
              {items.map(item => <RailItem key={item.path} item={item} t={t} />)}
              {group.titleKey === 'operate.system' && (
                <a
                  className="nav-item"
                  href={apiUrl('/swagger/index.html')}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <i className="fas fa-code nav-icon" />
                  <span className="nav-label">{t('items.api')}</span>
                  <i className="fas fa-external-link-alt nav-external" />
                </a>
              )}
            </div>
          )
        })}
      </nav>
      <div className="admin-console-body" key={location.pathname}>
        <Outlet context={outletContext} />
      </div>
    </div>
  )
}
