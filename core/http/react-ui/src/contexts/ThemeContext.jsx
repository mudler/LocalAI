import { createContext, useContext, useState, useEffect, useCallback } from 'react'

const ThemeContext = createContext()

function getInitialTheme() {
  const params = new URLSearchParams(window.location.search)
  const urlTheme = params.get('theme')
  if (urlTheme === 'dark' || urlTheme === 'light') return urlTheme
  const stored = localStorage.getItem('localai-theme')
  if (stored) return stored
  if (window.matchMedia?.('(prefers-color-scheme: light)').matches) return 'light'
  return 'dark'
}

export function ThemeProvider({ children }) {
  const [theme, setTheme] = useState(getInitialTheme)

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem('localai-theme', theme)
  }, [theme])

  const handleSyncMessage = useCallback((event) => {
    if (event.data?.type === 'theme-sync') {
      const t = event.data.theme
      if (t === 'dark' || t === 'light') setTheme(t)
    }
  }, [])

  useEffect(() => {
    window.addEventListener('message', handleSyncMessage)
    return () => window.removeEventListener('message', handleSyncMessage)
  }, [handleSyncMessage])

  const toggleTheme = () => {
    setTheme(prev => prev === 'dark' ? 'light' : 'dark')
  }

  return (
    <ThemeContext.Provider value={{ theme, setTheme, toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  )
}

export function useTheme() {
  const context = useContext(ThemeContext)
  if (!context) {
    throw new Error('useTheme must be used within a ThemeProvider')
  }
  return context
}
