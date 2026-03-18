import { useState, useEffect } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import ThemeToggle from './ThemeToggle'
import { useAuth } from '../context/AuthContext'
import { apiUrl } from '../utils/basePath'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'

const mainItems = [
  { path: '/app', icon: 'fas fa-home', label: 'Home' },
  { path: '/app/models', icon: 'fas fa-download', label: 'Install Models', adminOnly: true },
  { path: '/app/chat', icon: 'fas fa-comments', label: 'Chat' },
  { path: '/app/image', icon: 'fas fa-image', label: 'Images' },
  { path: '/app/video', icon: 'fas fa-video', label: 'Video' },
  { path: '/app/tts', icon: 'fas fa-music', label: 'TTS' },
  { path: '/app/sound', icon: 'fas fa-volume-high', label: 'Sound' },
  { path: '/app/talk', icon: 'fas fa-phone', label: 'Talk' },
  { path: '/app/usage', icon: 'fas fa-chart-bar', label: 'Usage', authOnly: true },
]

const agentItems = [
  { path: '/app/agents', icon: 'fas fa-robot', label: 'Agents' },
  { path: '/app/skills', icon: 'fas fa-wand-magic-sparkles', label: 'Skills' },
  { path: '/app/collections', icon: 'fas fa-database', label: 'Memory' },
  { path: '/app/agent-jobs', icon: 'fas fa-tasks', label: 'MCP CI Jobs', feature: 'mcp' },
]

const systemItems = [
  { path: '/app/users', icon: 'fas fa-users', label: 'Users', adminOnly: true, authOnly: true },
  { path: '/app/backends', icon: 'fas fa-server', label: 'Backends', adminOnly: true },
  { path: '/app/traces', icon: 'fas fa-chart-line', label: 'Traces', adminOnly: true },
  { path: '/app/p2p', icon: 'fas fa-circle-nodes', label: 'Swarm', adminOnly: true },
  { path: '/app/manage', icon: 'fas fa-desktop', label: 'System', adminOnly: true },
  { path: '/app/settings', icon: 'fas fa-cog', label: 'Settings', adminOnly: true },
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

export default function Sidebar({ isOpen, onClose }) {
  const [features, setFeatures] = useState({})
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem(COLLAPSED_KEY) === 'true' } catch (_) { return false }
  })
  const { isAdmin, authEnabled, user, logout, hasFeature } = useAuth()
  const navigate = useNavigate()

  useEffect(() => {
    fetch(apiUrl('/api/features')).then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

  const toggleCollapse = () => {
    setCollapsed(prev => {
      const next = !prev
      try { localStorage.setItem(COLLAPSED_KEY, String(next)) } catch (_) { /* ignore */ }
      window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: next } }))
      return next
    })
  }

  const visibleMainItems = mainItems.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.authOnly && !authEnabled) return false
    return true
  })

  const visibleSystemItems = systemItems.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.authOnly && !authEnabled) return false
    return true
  })

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
          {/* Main section */}
          <div className="sidebar-section">
            {visibleMainItems.map(item => (
              <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
            ))}
          </div>

          {/* Agents section (per-feature permissions) */}
          {features.agents !== false && (() => {
            const featureMap = {
              '/app/agents': 'agents',
              '/app/skills': 'skills',
              '/app/collections': 'collections',
              '/app/agent-jobs': 'mcp_jobs',
            }
            const visibleAgentItems = agentItems.filter(item => {
              if (item.feature && features[item.feature] === false) return false
              const featureName = featureMap[item.path]
              return featureName ? hasFeature(featureName) : isAdmin
            })
            if (visibleAgentItems.length === 0) return null
            return (
              <div className="sidebar-section">
                <div className="sidebar-section-title">Agents</div>
                {visibleAgentItems.map(item => (
                  <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
                ))}
              </div>
            )
          })()}

          {/* System section */}
          <div className="sidebar-section">
            {visibleSystemItems.length > 0 && (
              <div className="sidebar-section-title">System</div>
            )}
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
            {visibleSystemItems.map(item => (
              <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
            ))}
          </div>
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
