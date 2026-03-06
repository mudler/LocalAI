import { useState, useEffect } from 'react'
import { NavLink } from 'react-router-dom'
import ThemeToggle from './ThemeToggle'

const mainItems = [
  { path: '/', icon: 'fas fa-home', label: 'Home' },
  { path: '/browse', icon: 'fas fa-download', label: 'Install Models' },
  { path: '/chat', icon: 'fas fa-comments', label: 'Chat' },
  { path: '/image', icon: 'fas fa-image', label: 'Images' },
  { path: '/video', icon: 'fas fa-video', label: 'Video' },
  { path: '/tts', icon: 'fas fa-music', label: 'TTS' },
  { path: '/sound', icon: 'fas fa-volume-high', label: 'Sound' },
  { path: '/talk', icon: 'fas fa-phone', label: 'Talk' },
]

const agentItems = [
  { path: '/agents', icon: 'fas fa-robot', label: 'Agents' },
  { path: '/skills', icon: 'fas fa-wand-magic-sparkles', label: 'Skills' },
  { path: '/collections', icon: 'fas fa-database', label: 'Memory' },
  { path: '/agent-jobs', icon: 'fas fa-tasks', label: 'MCP CI Jobs', feature: 'mcp' },
]

const systemItems = [
  { path: '/backends', icon: 'fas fa-server', label: 'Backends' },
  { path: '/traces', icon: 'fas fa-chart-line', label: 'Traces' },
  { path: '/p2p', icon: 'fas fa-circle-nodes', label: 'Swarm' },
  { path: '/manage', icon: 'fas fa-desktop', label: 'System' },
  { path: '/settings', icon: 'fas fa-cog', label: 'Settings' },
]

function NavItem({ item, onClose }) {
  return (
    <NavLink
      to={item.path}
      end={item.path === '/'}
      className={({ isActive }) =>
        `nav-item ${isActive ? 'active' : ''}`
      }
      onClick={onClose}
    >
      <i className={`${item.icon} nav-icon`} />
      <span className="nav-label">{item.label}</span>
    </NavLink>
  )
}

export default function Sidebar({ isOpen, onClose }) {
  const [features, setFeatures] = useState({})
  useEffect(() => {
    fetch('/api/features').then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

  return (
    <>
      {isOpen && <div className="sidebar-overlay" onClick={onClose} />}

      <aside className={`sidebar ${isOpen ? 'open' : ''}`}>
        {/* Logo */}
        <div className="sidebar-header">
          <a href="./" className="sidebar-logo-link">
            <img src="/static/logo_horizontal.png" alt="LocalAI" className="sidebar-logo-img" />
          </a>
          <button className="sidebar-close-btn" onClick={onClose} aria-label="Close menu">
            <i className="fas fa-times" />
          </button>
        </div>

        {/* Navigation */}
        <nav className="sidebar-nav">
          {/* Main section */}
          <div className="sidebar-section">
            {mainItems.map(item => (
              <NavItem key={item.path} item={item} onClose={onClose} />
            ))}
          </div>

          {/* Agents section */}
          {features.agents !== false && (
            <div className="sidebar-section">
              <div className="sidebar-section-title">Agents</div>
              {agentItems.filter(item => !item.feature || features[item.feature] !== false).map(item => (
                <NavItem key={item.path} item={item} onClose={onClose} />
              ))}
            </div>
          )}

          {/* System section */}
          <div className="sidebar-section">
            <div className="sidebar-section-title">System</div>
            <a
              href="/swagger/index.html"
              target="_blank"
              rel="noopener noreferrer"
              className="nav-item"
            >
              <i className="fas fa-code nav-icon" />
              <span className="nav-label">API</span>
              <i className="fas fa-external-link-alt" style={{ fontSize: '0.6rem', marginLeft: 'auto', opacity: 0.5 }} />
            </a>
            {systemItems.map(item => (
              <NavItem key={item.path} item={item} onClose={onClose} />
            ))}
          </div>
        </nav>

        {/* Footer */}
        <div className="sidebar-footer">
          <ThemeToggle />
        </div>
      </aside>
    </>
  )
}
