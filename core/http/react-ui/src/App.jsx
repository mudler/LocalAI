import { useState, useEffect } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './components/Sidebar'
import OperationsBar from './components/OperationsBar'
import { ToastContainer, useToast } from './components/Toast'
import { systemApi } from './utils/api'

export default function App() {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const { toasts, addToast, removeToast } = useToast()
  const [version, setVersion] = useState('')

  useEffect(() => {
    systemApi.version()
      .then(data => setVersion(typeof data === 'string' ? data : (data?.version || '')))
      .catch(() => {})
  }, [])

  return (
    <div className="app-layout">
      <Sidebar isOpen={sidebarOpen} onClose={() => setSidebarOpen(false)} />
      <main className="main-content">
        <OperationsBar />
        {/* Mobile header */}
        <header className="mobile-header">
          <button
            className="hamburger-btn"
            onClick={() => setSidebarOpen(true)}
          >
            <i className="fas fa-bars" />
          </button>
          <span className="mobile-title">LocalAI</span>
        </header>
        <div className="main-content-inner">
          <Outlet context={{ addToast }} />
        </div>
        <footer className="app-footer">
          <div className="app-footer-inner">
            {version && (
              <span className="app-footer-version">
                LocalAI <span style={{ color: 'var(--color-primary)', fontWeight: 500 }}>{version}</span>
              </span>
            )}
            <div className="app-footer-links">
              <a href="https://github.com/mudler/LocalAI" target="_blank" rel="noopener noreferrer">
                <i className="fab fa-github" /> GitHub
              </a>
              <a href="https://localai.io" target="_blank" rel="noopener noreferrer">
                <i className="fas fa-book" /> Documentation
              </a>
              <a href="https://mudler.pm" target="_blank" rel="noopener noreferrer">
                <i className="fas fa-user" /> Author
              </a>
            </div>
            <span className="app-footer-copyright">
              &copy; 2023-2026 <a href="https://mudler.pm" target="_blank" rel="noopener noreferrer">Ettore Di Giacinto</a>
            </span>
          </div>
        </footer>
      </main>
      <ToastContainer toasts={toasts} removeToast={removeToast} />
    </div>
  )
}
