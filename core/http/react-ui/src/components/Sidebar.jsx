import { useState, useEffect, useRef } from 'react'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ThemeToggle from './ThemeToggle'
import LanguageSwitcher from './LanguageSwitcher'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'
import { preloadRoute } from '../router'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'
const SECTIONS_KEY = 'localai_sidebar_sections'

const topItems = [
  { path: '/app', icon: 'fas fa-home', labelKey: 'items.home' },
]

// Single entry into the admin console (Task 3). Lands on the console's default
// page and stays visually active for any admin route via `adminPaths` below.
const operateItem = { path: '/app/models', icon: 'fas fa-sliders', labelKey: 'sections.operate', adminOnly: true }

const adminPaths = [
  '/app/models', '/app/backends', '/app/nodes', '/app/p2p', '/app/usage',
  '/app/traces', '/app/users', '/app/middleware', '/app/manage', '/app/settings',
  '/app/backend-logs', '/app/node-backend-logs',
]

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
  {
    id: 'recognition',
    titleKey: 'sections.recognition',
    featureMap: {
      '/app/face': 'face_recognition',
      '/app/voice': 'voice_recognition',
    },
    items: [
      { path: '/app/face', icon: 'fas fa-face-smile', labelKey: 'items.faces', feature: 'face_recognition' },
      { path: '/app/voice', icon: 'fas fa-microphone-lines', labelKey: 'items.voices', feature: 'voice_recognition' },
    ],
  },
  {
    id: 'build',
    titleKey: 'sections.build',
    // featureMap entries are capability-gated via hasFeature(); items NOT listed
    // here fall back to the isAdmin check in getVisibleSectionItems — this keeps
    // Fine-tune/Quantize admin-gated exactly as the old `tools` section did.
    featureMap: {
      '/app/agents': 'agents',
      '/app/skills': 'skills',
      '/app/collections': 'collections',
      '/app/agent-jobs': 'mcp_jobs',
    },
    items: [
      { path: '/app/agents', icon: 'fas fa-robot', labelKey: 'items.agents' },
      { path: '/app/skills', icon: 'fas fa-wand-magic-sparkles', labelKey: 'items.skills' },
      { path: '/app/collections', icon: 'fas fa-database', labelKey: 'items.memory' },
      { path: '/app/agent-jobs', icon: 'fas fa-tasks', labelKey: 'items.jobs', feature: 'mcp' },
      { path: '/app/fine-tune', icon: 'fas fa-graduation-cap', labelKey: 'items.fineTune', feature: 'fine_tuning' },
      { path: '/app/quantize', icon: 'fas fa-compress', labelKey: 'items.quantize', feature: 'quantization' },
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
    setCollapsed(prev => {
      const next = !prev
      try { localStorage.setItem(COLLAPSED_KEY, String(next)) } catch (_) { /* ignore */ }
      window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: next } }))
      return next
    })
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

  const getVisibleSectionItems = (section) => {
    return section.items.filter(item => {
      if (!filterItem(item)) return false
      if (section.featureMap) {
        const featureName = section.featureMap[item.path]
        if (!featureName) return isAdmin
        // Respect the global capability flag from /api/features (e.g. the agents
        // pool being disabled) in addition to the per-user hasFeature() check.
        // This keeps the old `agents` section's global gate intact now that those
        // items live in the `build` tier alongside ungated tools.
        if (features[featureName] === false) return false
        return hasFeature(featureName)
      }
      return true
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

          {isAdmin && (
            <div className="sidebar-section">
              <NavLink
                to={operateItem.path}
                className={() =>
                  `nav-item ${adminPaths.some(p => location.pathname.startsWith(p)) ? 'active' : ''}`
                }
                onClick={onClose}
                onMouseEnter={() => preloadRoute(operateItem.path)}
                onFocus={() => preloadRoute(operateItem.path)}
                onTouchStart={() => preloadRoute(operateItem.path)}
                title={collapsed ? t(operateItem.labelKey) : undefined}
              >
                <i className={`${operateItem.icon} nav-icon`} />
                <span className="nav-label">{t(operateItem.labelKey)}</span>
              </NavLink>
            </div>
          )}
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
