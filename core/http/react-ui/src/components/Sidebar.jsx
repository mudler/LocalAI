import { useState, useEffect } from 'react'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import ThemeToggle from './ThemeToggle'
import { useAuth } from '../context/AuthContext'
import { apiUrl } from '../utils/basePath'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'
const SECTIONS_KEY = 'localai_sidebar_sections'

const topItems = [
  { path: '/app', icon: 'fas fa-home', label: 'Home' },
  { path: '/app/models', icon: 'fas fa-download', label: 'Install Models', adminOnly: true },
  { path: '/app/chat', icon: 'fas fa-comments', label: 'Chat' },
  { path: '/app/studio', icon: 'fas fa-palette', label: 'Studio' },
  { path: '/app/talk', icon: 'fas fa-phone', label: 'Talk' },
]

const sections = [
  {
    id: 'tools',
    title: 'Tools',
    items: [
      { path: '/app/fine-tune', icon: 'fas fa-graduation-cap', label: 'Fine-Tune (Experimental)', feature: 'fine_tuning' },
      { path: '/app/quantize', icon: 'fas fa-compress', label: 'Quantize (Experimental)', feature: 'quantization' },
    ],
  },
  {
    id: 'agents',
    title: 'Agents',
    featureMap: {
      '/app/agents': 'agents',
      '/app/skills': 'skills',
      '/app/collections': 'collections',
      '/app/agent-jobs': 'mcp_jobs',
    },
    items: [
      { path: '/app/agents', icon: 'fas fa-robot', label: 'Agents' },
      { path: '/app/skills', icon: 'fas fa-wand-magic-sparkles', label: 'Skills' },
      { path: '/app/collections', icon: 'fas fa-database', label: 'Memory' },
      { path: '/app/agent-jobs', icon: 'fas fa-tasks', label: 'MCP CI Jobs', feature: 'mcp' },
    ],
  },
  {
    id: 'system',
    title: 'System',
    items: [
      { path: '/app/usage', icon: 'fas fa-chart-bar', label: 'Usage', authOnly: true },
      { path: '/app/users', icon: 'fas fa-users', label: 'Users', adminOnly: true, authOnly: true },
      { path: '/app/backends', icon: 'fas fa-server', label: 'Backends', adminOnly: true },
      { path: '/app/traces', icon: 'fas fa-chart-line', label: 'Traces', adminOnly: true },
      { path: '/app/p2p', icon: 'fas fa-circle-nodes', label: 'Swarm', adminOnly: true },
      { path: '/app/manage', icon: 'fas fa-desktop', label: 'System', adminOnly: true },
      { path: '/app/settings', icon: 'fas fa-cog', label: 'Settings', adminOnly: true },
    ],
  },
]

function NavItem({ item, onClose, collapsed }) {
  return (
    <NavLink
      to={item.path}
      end={item.path === '/app'}
      className={({ isActive }) =>
        `nav-item ${isActive ? 'active' : ''}`
      }
      onClick={onClose}
      title={collapsed ? item.label : undefined}
    >
      <i className={`${item.icon} nav-icon`} />
      <span className="nav-label">{item.label}</span>
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
  const [features, setFeatures] = useState({})
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem(COLLAPSED_KEY) === 'true' } catch (_) { return false }
  })
  const [openSections, setOpenSections] = useState(loadSectionState)
  const { isAdmin, authEnabled, user, logout, hasFeature } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()

  useEffect(() => {
    fetch(apiUrl('/api/features')).then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

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

      <aside className={`sidebar ${isOpen ? 'open' : ''} ${collapsed ? 'collapsed' : ''}`}>
        {/* Logo */}
        <div className="sidebar-header">
          <a href="./" className="sidebar-logo-link">
            <img src={apiUrl('/static/logo_horizontal.png')} alt="LocalAI" className="sidebar-logo-img" />
          </a>
          <a href="./" className="sidebar-logo-icon" title="LocalAI">
            <img src={apiUrl('/static/logo.png')} alt="LocalAI" className="sidebar-logo-icon-img" />
          </a>
          <button className="sidebar-close-btn" onClick={onClose} aria-label="Close menu">
            <i className="fas fa-times" />
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

            return (
              <div key={section.id} className="sidebar-section">
                <button
                  className={`sidebar-section-title sidebar-section-toggle ${isSectionOpen ? 'open' : ''}`}
                  onClick={() => toggleSection(section.id)}
                  title={collapsed ? section.title : undefined}
                >
                  <span>{section.title}</span>
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
                        title={collapsed ? 'API' : undefined}
                      >
                        <i className="fas fa-code nav-icon" />
                        <span className="nav-label">API</span>
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
                title="Account settings"
              >
                {user.avatarUrl ? (
                  <img src={user.avatarUrl} alt="" className="sidebar-user-avatar" />
                ) : (
                  <i className="fas fa-user-circle sidebar-user-avatar-icon" />
                )}
                <span className="nav-label sidebar-user-name">{user.name || user.email}</span>
              </button>
              <button className="sidebar-logout-btn" onClick={logout} title="Logout">
                <i className="fas fa-sign-out-alt" />
              </button>
            </div>
          )}
          <ThemeToggle />
          <button
            className="sidebar-collapse-btn"
            onClick={toggleCollapse}
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            <i className={`fas fa-chevron-${collapsed ? 'right' : 'left'}`} />
          </button>
        </div>
      </aside>
    </>
  )
}
