import { useState, useEffect, useRef } from 'react'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ThemeToggle from './ThemeToggle'
import LanguageSwitcher from './LanguageSwitcher'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'
const SECTIONS_KEY = 'localai_sidebar_sections'

const topItems = [
  { path: '/app', icon: 'fas fa-home', labelKey: 'items.home' },
  { path: '/app/models', icon: 'fas fa-download', labelKey: 'items.installModels', adminOnly: true },
  { path: '/app/chat', icon: 'fas fa-comments', labelKey: 'items.chat' },
  { path: '/app/studio', icon: 'fas fa-palette', labelKey: 'items.studio' },
  { path: '/app/talk', icon: 'fas fa-phone', labelKey: 'items.talk' },
]

const sections = [
  {
    id: 'tools',
    titleKey: 'sections.tools',
    items: [
      { path: '/app/fine-tune', icon: 'fas fa-graduation-cap', labelKey: 'items.fineTune', feature: 'fine_tuning' },
      { path: '/app/quantize', icon: 'fas fa-compress', labelKey: 'items.quantize', feature: 'quantization' },
    ],
  },
  {
    id: 'biometrics',
    titleKey: 'sections.biometrics',
    featureMap: {
      '/app/face': 'face_recognition',
      '/app/voice': 'voice_recognition',
    },
    items: [
      { path: '/app/face', icon: 'fas fa-face-smile', labelKey: 'items.faceRecognition', feature: 'face_recognition' },
      { path: '/app/voice', icon: 'fas fa-microphone-lines', labelKey: 'items.voiceRecognition', feature: 'voice_recognition' },
    ],
  },
  {
    id: 'agents',
    titleKey: 'sections.agents',
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
      { path: '/app/agent-jobs', icon: 'fas fa-tasks', labelKey: 'items.mcpJobs', feature: 'mcp' },
    ],
  },
  {
    id: 'system',
    titleKey: 'sections.system',
    items: [
      { path: '/app/usage', icon: 'fas fa-chart-bar', labelKey: 'items.usage', authOnly: true },
      { path: '/app/users', icon: 'fas fa-users', labelKey: 'items.users', adminOnly: true, authOnly: true },
      { path: '/app/backends', icon: 'fas fa-server', labelKey: 'items.backends', adminOnly: true },
      { path: '/app/traces', icon: 'fas fa-chart-line', labelKey: 'items.traces', adminOnly: true },
      { path: '/app/nodes', icon: 'fas fa-network-wired', labelKey: 'items.nodes', adminOnly: true, feature: 'distributed' },
      { path: '/app/p2p', icon: 'fas fa-circle-nodes', labelKey: 'items.swarm', adminOnly: true },
      { path: '/app/manage', icon: 'fas fa-desktop', labelKey: 'items.system', adminOnly: true },
      { path: '/app/settings', icon: 'fas fa-cog', labelKey: 'items.settings', adminOnly: true },
    ],
  },
]

function NavItem({ item, onClose, collapsed }) {
  const { t } = useTranslation('nav')
  const label = t(item.labelKey)
  return (
    <NavLink
      to={item.path}
      end={item.path === '/app'}
      className={({ isActive }) =>
        `nav-item ${isActive ? 'active' : ''}`
      }
      onClick={onClose}
      title={collapsed ? label : undefined}
    >
      <i className={`${item.icon} nav-icon`} />
      <span className="nav-label">{label}</span>
    </NavLink>
  )
}

function loadSectionState() {
  try {
    const stored = localStorage.getItem(SECTIONS_KEY)
    return stored ? JSON.parse(stored) : {}
  } catch (_) {
    return {}
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
        return featureName ? hasFeature(featureName) : isAdmin
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
            // For agents section, check global feature flag
            if (section.id === 'agents' && features.agents === false) return null

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
                    {section.id === 'system' && (
                      <a
                        href={apiUrl('/swagger/index.html')}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="nav-item"
                        title={collapsed ? t('items.api') : undefined}
                      >
                        <i className="fas fa-code nav-icon" />
                        <span className="nav-label">{t('items.api')}</span>
                        <i className="fas fa-external-link-alt nav-external" />
                      </a>
                    )}
                    {visibleItems.map(item => (
                      <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
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
