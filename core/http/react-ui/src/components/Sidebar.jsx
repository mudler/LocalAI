import { useState, useEffect, useRef } from 'react'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ThemeToggle from './ThemeToggle'
import LanguageSwitcher from './LanguageSwitcher'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'
import { preloadRoute } from '../router'
import { consoles, firstVisiblePath, consolePaths } from './console/consoleConfig'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'
const SECTIONS_KEY = 'localai_sidebar_sections'

const topItems = [
  { path: '/app', icon: 'fas fa-home', labelKey: 'items.home' },
  { path: '/app/models', icon: 'fas fa-download', labelKey: 'items.installModels', adminOnly: true },
]

// Create stays inline (frequent, one-click creative destinations). The Build
// and Operate tiers are single entries that open a secondary console rail —
// their items live in console/consoleConfig.js (shared with ConsoleLayout).
const sections = [
  {
    id: 'create',
    titleKey: 'sections.create',
    items: [
      { path: '/app/chat', icon: 'fas fa-comments', labelKey: 'items.chat' },
      { path: '/app/studio', icon: 'fas fa-palette', labelKey: 'items.studio' },
      { path: '/app/talk', icon: 'fas fa-phone', labelKey: 'items.talk' },
    ],
  },
]

function NavItem({ item, onClose, collapsed }) {
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
        `nav-item ${isActive ? 'active' : ''}`
      }
      onClick={onClose}
      onMouseEnter={preload}
      onFocus={preload}
      onTouchStart={preload}
      title={collapsed ? label : undefined}
    >
      <i className={`${item.icon} nav-icon`} />
      <span className="nav-label">{label}</span>
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
  // Shared shape for the console gating helpers (consoleConfig.js).
  const auth = { isAdmin, authEnabled, hasFeature, features }

  // Inline sections (Create) carry no gating; a plain filterItem pass suffices.
  const getVisibleSectionItems = (section) => section.items.filter(filterItem)

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
            <i className="fas fa-times" aria-hidden="true" />
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
                  <i className="fas fa-chevron-right sidebar-section-chevron" />
                </button>
                {showItems && (
                  <div className="sidebar-section-items">
                    {visibleItems.map(item => (
                      <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
                    ))}
                  </div>
                )}
              </div>
            )
          })}

          {/* Console tiers (Build, Operate): a single entry that opens a
              secondary rail. Hidden when the viewer can see none of its items. */}
          {consoles.map(config => {
            const target = firstVisiblePath(config, auth)
            if (!target) return null
            const active = consolePaths(config).some(p => location.pathname.startsWith(p))
            const label = t(config.titleKey)
            return (
              <div key={config.id} className="sidebar-section">
                <NavLink
                  to={target}
                  className={() => `nav-item ${active ? 'active' : ''}`}
                  onClick={onClose}
                  onMouseEnter={() => preloadRoute(target)}
                  onFocus={() => preloadRoute(target)}
                  onTouchStart={() => preloadRoute(target)}
                  title={collapsed ? label : undefined}
                >
                  <i className={`${config.icon} nav-icon`} />
                  <span className="nav-label">{label}</span>
                </NavLink>
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
                  <i className="fas fa-user-circle sidebar-user-avatar-icon" />
                )}
                <span className="nav-label sidebar-user-name">{user.name || user.email}</span>
              </button>
              <button className="sidebar-logout-btn" onClick={logout} title={t('logout')}>
                <i className="fas fa-sign-out-alt" />
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
            <i className={`fas fa-chevron-${collapsed ? 'right' : 'left'}`} />
          </button>
        </div>
      </aside>
    </>
  )
}
