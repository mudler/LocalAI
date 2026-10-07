import { useState, useEffect, useRef } from 'react'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ThemeToggle from './ThemeToggle'
import LanguageSwitcher from './LanguageSwitcher'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'
import { preloadRoute } from '../router'
import { hubs, hubEntryPath, hubOwnsPath } from './hub/hubConfig'
import { useOperations } from '../hooks/useOperations'
import Icon from './Icon'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'
const SECTIONS_KEY = 'localai_sidebar_sections'

const topItems = [
  { path: '/app', icon: 'home', labelKey: 'items.home' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
]

// Create stays inline (frequent, one-click creative destinations). Build and
// Operate sit under Workspace as single entries; each opens a hub whose tab
// bar lives in hub/hubConfig.js (shared with HubLayout).
const sections = [
  {
    id: 'create',
    titleKey: 'sections.create',
    items: [
      { path: '/app/chat', icon: 'chat', labelKey: 'items.chat' },
      { path: '/app/studio', icon: 'palette', labelKey: 'items.studio' },
      { path: '/app/talk', icon: 'phone', labelKey: 'items.talk' },
    ],
  },
  // Items come from the hubs (hubConfig.js) and carry their own gating.
  { id: 'workspace', titleKey: 'sections.workspace', hubs: true },
]

function NavItem({ item, onClose, collapsed, active, badge }) {
  const { t } = useTranslation('nav')
  const label = t(item.labelKey)
  // Warm the route's lazy chunk before the user clicks. Touch fires ~150ms
  // before the synthetic click on mobile; mouseenter/focus cover desktop and
  // keyboard. The underlying import() is memoised so multiple triggers are free.
  const preload = () => preloadRoute(item.path)
  return (
    <NavLink
      to={item.path}
      end={item.path === '/app'}
      className={({ isActive }) =>
        `nav-item ${(active ?? isActive) ? 'active' : ''}`
      }
      onClick={onClose}
      onMouseEnter={preload}
      onFocus={preload}
      onTouchStart={preload}
      title={collapsed ? label : undefined}
    >
      <Icon name={item.icon} className="nav-icon" aria-hidden="true" />
      <span className="nav-label">{label}</span>
      {badge}
    </NavLink>
  )
}

function loadSectionState() {
  // Tiers render expanded by default (the redesign favours showing the few
  // intent groups up front); users can still collapse any tier and the choice
  // is persisted. Stored values override the defaults so a saved collapse wins.
  const defaults = Object.fromEntries(sections.map(s => [s.id, true]))
  try {
    const stored = localStorage.getItem(SECTIONS_KEY)
    return stored ? { ...defaults, ...JSON.parse(stored) } : defaults
  } catch (_) {
    return defaults
  }
}

function saveSectionState(state) {
  try { localStorage.setItem(SECTIONS_KEY, JSON.stringify(state)) } catch (_) { /* ignore */ }
}

export default function Sidebar({ isOpen, onClose }) {
  const { t } = useTranslation('nav')
  const [features, setFeatures] = useState({})
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem(COLLAPSED_KEY) === 'true' } catch (_) { return false }
  })
  const [openSections, setOpenSections] = useState(loadSectionState)
  const { isAdmin, authEnabled, user, logout, hasFeature } = useAuth()
  const { operations } = useOperations()
  const branding = useBranding()
  const navigate = useNavigate()
  const location = useLocation()
  const closeBtnRef = useRef(null)

  useEffect(() => {
    fetch(apiUrl('/api/features')).then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

  // Stay in sync with external collapse dispatches (e.g. the chat
  // page's focus mode). The collapse-toggle button still owns the
  // localStorage write — listeners only mirror state, otherwise an
  // outside dispatch would silently overwrite the user's preference.
  useEffect(() => {
    const handler = (e) => {
      const next = !!e.detail?.collapsed
      setCollapsed(prev => (prev === next ? prev : next))
    }
    window.addEventListener('sidebar-collapse', handler)
    return () => window.removeEventListener('sidebar-collapse', handler)
  }, [])

  // Move focus into the drawer when opened on mobile/tablet so keyboard
  // and screen-reader users land inside the dialog. Targeting the close
  // button avoids hijacking the visual focus to a nav item the user may
  // not have meant to activate.
  useEffect(() => {
    if (!isOpen) return
    const id = window.requestAnimationFrame(() => closeBtnRef.current?.focus())
    return () => window.cancelAnimationFrame(id)
  }, [isOpen])

  // Auto-expand section containing the active route
  useEffect(() => {
    for (const section of sections) {
      if (!section.items) continue
      const match = section.items.some(item => location.pathname.startsWith(item.path))
      if (match && !openSections[section.id]) {
        setOpenSections(prev => {
          const next = { ...prev, [section.id]: true }
          saveSectionState(next)
          return next
        })
      }
    }
  }, [location.pathname])

  const toggleCollapse = () => {
    // Side effects (persist + broadcast) live in the handler body, never inside
    // the setState updater: StrictMode double-invokes updaters in dev, and the
    // synchronous sidebar-collapse dispatch re-entered setState from the
    // listeners mid-update, so the toggle silently no-op'd in dev builds.
    const next = !collapsed
    try { localStorage.setItem(COLLAPSED_KEY, String(next)) } catch (_) { /* ignore */ }
    setCollapsed(next)
    window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: next } }))
  }

  const toggleSection = (id) => {
    setOpenSections(prev => {
      const next = { ...prev, [id]: !prev[id] }
      saveSectionState(next)
      return next
    })
  }

  const filterItem = (item) => {
    if (item.adminOnly && !isAdmin) return false
    if (item.authOnly && !authEnabled) return false
    if (item.feature && features[item.feature] === false) return false
    if (item.feature && !hasFeature(item.feature)) return false
    return true
  }

  const visibleTopItems = topItems.filter(filterItem)
  // Shared shape for the hub gating helpers (hubConfig.js).
  const auth = { isAdmin, authEnabled, hasFeature, features }

  // One badge, on the always-visible sidebar entry. The Operate tab bar only
  // exists while the user is on an Operate route, so badging a tab instead
  // would let the count disappear entirely.
  const failedOps = operations.filter((op) => op.error).length
  const activeOps = operations.length

  // Create carries no gating beyond filterItem. Workspace lists one entry per
  // hub the viewer can use; its target is the hub's overview.
  const getVisibleSectionItems = (section) => {
    if (!section.hubs) return section.items.filter(filterItem)
    return hubs.flatMap(hub => {
      const path = hubEntryPath(hub, auth)
      if (!path) return []
      return [{ path, icon: hub.icon, labelKey: hub.titleKey, hub }]
    })
  }

  return (
    <>
      {isOpen && <div className="sidebar-overlay" onClick={onClose} />}

      <aside
        id="app-sidebar"
        className={`sidebar ${isOpen ? 'open' : ''} ${collapsed ? 'collapsed' : ''}`}
        aria-label={t('primaryNavigation')}
      >
        {/* Logo */}
        <div className="sidebar-header">
          <a href="./" className="sidebar-logo-link">
            <img src={apiUrl(branding.logoHorizontalUrl)} alt={branding.instanceName} className="sidebar-logo-img" />
          </a>
          <a href="./" className="sidebar-logo-icon" title={branding.instanceName}>
            <img src={apiUrl(branding.logoUrl)} alt={branding.instanceName} className="sidebar-logo-icon-img" />
          </a>
          <button
            ref={closeBtnRef}
            className="sidebar-close-btn"
            onClick={onClose}
            aria-label={t('closeMenu')}
          >
            <Icon name="close" />
          </button>
        </div>

        {/* Navigation */}
        <nav className="sidebar-nav">
          {/* Top-level items */}
          <div className="sidebar-section">
            {visibleTopItems.map(item => (
              <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
            ))}
          </div>

          {/* Collapsible sections */}
          {sections.map(section => {
            const visibleItems = getVisibleSectionItems(section)
            if (visibleItems.length === 0) return null

            const isSectionOpen = openSections[section.id]
            const showItems = isSectionOpen || collapsed
            const sectionTitle = t(section.titleKey)

            return (
              <div key={section.id} className="sidebar-section">
                <button
                  className={`sidebar-section-title sidebar-section-toggle ${isSectionOpen ? 'open' : ''}`}
                  onClick={() => toggleSection(section.id)}
                  title={collapsed ? sectionTitle : undefined}
                >
                  <span>{sectionTitle}</span>
                  <Icon name="chevron-right" className="sidebar-section-chevron" />
                </button>
                {showItems && (
                  <div className="sidebar-section-items">
                    {visibleItems.map(item => (
                      <NavItem
                        key={item.path}
                        item={item}
                        onClose={onClose}
                        collapsed={collapsed}
                        active={item.hub ? hubOwnsPath(item.hub, location.pathname) : undefined}
                        badge={item.hub?.id === 'operate' && activeOps > 0 ? (
                          <span className={`nav-badge${failedOps > 0 ? ' nav-badge--error' : ''}`}>
                            {failedOps > 0 ? failedOps : activeOps}
                          </span>
                        ) : null}
                      />
                    ))}
                  </div>
                )}
              </div>
            )
          })}

        </nav>

        {/* Footer */}
        <div className="sidebar-footer">
          {authEnabled && user && (
            <div className="sidebar-user" title={collapsed ? (user.name || user.email) : undefined}>
              <button
                className="sidebar-user-link"
                onClick={() => { navigate('/app/account'); onClose?.() }}
                onMouseEnter={() => preloadRoute('/app/account')}
                onFocus={() => preloadRoute('/app/account')}
                onTouchStart={() => preloadRoute('/app/account')}
                title={t('accountSettings')}
              >
                {user.avatarUrl ? (
                  <img src={user.avatarUrl} alt="" className="sidebar-user-avatar" />
                ) : (
                  <Icon name="user" className="sidebar-user-avatar-icon" />
                )}
                <span className="nav-label sidebar-user-name">{user.name || user.email}</span>
              </button>
              <button className="sidebar-logout-btn" onClick={logout} title={t('logout')}>
                <Icon name="log-out" />
              </button>
            </div>
          )}
          <LanguageSwitcher />
          <ThemeToggle />
          <button
            className="sidebar-collapse-btn"
            onClick={toggleCollapse}
            title={collapsed ? t('expandSidebar') : t('collapseSidebar')}
          >
            <Icon name={`chevron-${collapsed ? 'right' : 'left'}`} />
          </button>
        </div>
      </aside>
    </>
  )
}
