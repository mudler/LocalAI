import { useState, useEffect, useRef } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Sidebar from './components/Sidebar'
import OperationsBar from './components/OperationsBar'
import { ToastContainer, useToast } from './components/Toast'
import { systemApi } from './utils/api'
import { useTheme } from './contexts/ThemeContext'
import { useBranding } from './contexts/BrandingContext'
import { useAuth } from './context/AuthContext'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'

export default function App() {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return localStorage.getItem(COLLAPSED_KEY) === 'true' } catch (_) { return false }
  })
  const { toasts, addToast, removeToast } = useToast()
  const [version, setVersion] = useState('')
  const location = useLocation()
  const navigate = useNavigate()
  const { theme, toggleTheme } = useTheme()
  const { authEnabled, user } = useAuth()
  const branding = useBranding()
  const { t } = useTranslation('nav')
  const hamburgerRef = useRef(null)
  const isChatRoute = location.pathname.match(/\/chat(\/|$)/) || location.pathname.match(/\/agents\/[^/]+\/chat/)

  useEffect(() => {
    systemApi.version()
      .then(data => setVersion(typeof data === 'string' ? data : (data?.version || '')))
      .catch(() => {})
  }, [])

  useEffect(() => {
    const handler = (e) => setSidebarCollapsed(e.detail.collapsed)
    window.addEventListener('sidebar-collapse', handler)
    return () => window.removeEventListener('sidebar-collapse', handler)
  }, [])

  // Scroll to top on route change
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [location.pathname])

  // Drawer polish: lock body scroll, close on Escape, return focus to the
  // hamburger when the drawer closes. Only engages when the drawer is open;
  // desktop and tablet rail mode are unaffected.
  useEffect(() => {
    if (!sidebarOpen) return
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (e) => { if (e.key === 'Escape') setSidebarOpen(false) }
    window.addEventListener('keydown', onKey)
    return () => {
      document.body.style.overflow = prevOverflow
      window.removeEventListener('keydown', onKey)
      // Restore focus to the trigger so keyboard users land back where
      // they invoked the drawer from.
      hamburgerRef.current?.focus()
    }
  }, [sidebarOpen])

  const layoutClasses = [
    'app-layout',
    isChatRoute ? 'app-layout-chat' : '',
    sidebarCollapsed ? 'sidebar-is-collapsed' : '',
  ].filter(Boolean).join(' ')

  const showAvatar = authEnabled && user
  const accountLabel = user?.name || user?.email || t('account')
  const themeToggleLabel = theme === 'dark' ? t('switchToLightMode') : t('switchToDarkMode')

  return (
    <div className={layoutClasses}>
      <Sidebar isOpen={sidebarOpen} onClose={() => setSidebarOpen(false)} />
      <main className="main-content" {...(sidebarOpen ? { 'aria-hidden': 'true', inert: '' } : {})}>
        <OperationsBar />
        {/* Mobile header — primary actions reachable without opening the
            drawer. Hamburger is the only way to expand the nav on phones;
            theme toggle and account avatar are mirrored from the sidebar
            footer so they remain one tap away. */}
        <header className="mobile-header">
          <button
            ref={hamburgerRef}
            className="hamburger-btn"
            onClick={() => setSidebarOpen(true)}
            aria-label={t('openMenu')}
            aria-expanded={sidebarOpen}
            aria-controls="app-sidebar"
          >
            <i className="fas fa-bars" aria-hidden="true" />
          </button>
          <span className="mobile-title">{branding.instanceName}</span>
          <div className="mobile-header-actions">
            <button
              type="button"
              className="mobile-header-btn"
              onClick={toggleTheme}
              aria-label={themeToggleLabel}
              title={themeToggleLabel}
            >
              <i className={`fas ${theme === 'dark' ? 'fa-sun' : 'fa-moon'}`} aria-hidden="true" />
            </button>
            {showAvatar && (
              <button
                type="button"
                className="mobile-header-btn mobile-header-avatar"
                onClick={() => navigate('/app/account')}
                aria-label={t('accountFor', { name: accountLabel })}
                title={accountLabel}
              >
                {user.avatarUrl ? (
                  <img src={user.avatarUrl} alt="" />
                ) : (
                  <i className="fas fa-user-circle" aria-hidden="true" />
                )}
              </button>
            )}
          </div>
        </header>
        <div className="main-content-inner">
          <div className="page-transition" key={location.pathname}>
            <Outlet context={{ addToast }} />
          </div>
        </div>
        {!isChatRoute && (
          <footer className="app-footer">
            <div className="app-footer-inner">
              {version && (
                <span className="app-footer-version">
                  {branding.instanceName} <span style={{ fontWeight: 500 }}>{version}</span>
                </span>
              )}
              <div className="app-footer-links">
                <a href="https://github.com/mudler/LocalAI" target="_blank" rel="noopener noreferrer">
                  <i className="fab fa-github" /> {t('footer.github')}
                </a>
                <a href="https://localai.io" target="_blank" rel="noopener noreferrer">
                  <i className="fas fa-book" /> {t('footer.documentation')}
                </a>
                <a href="https://mudler.pm" target="_blank" rel="noopener noreferrer">
                  <i className="fas fa-user" /> {t('footer.author')}
                </a>
              </div>
              <span className="app-footer-copyright">
                &copy; 2023-2026 <a href="https://mudler.pm" target="_blank" rel="noopener noreferrer">Ettore Di Giacinto</a>
              </span>
            </div>
          </footer>
        )}
      </main>
      <ToastContainer toasts={toasts} removeToast={removeToast} />
    </div>
  )
}
