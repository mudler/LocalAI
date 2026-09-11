import { createContext, useContext, useState, useEffect } from 'react'

const ThemeContext = createContext()

// Dark is the identity, not a preference: localai.io ships one theme and it is
// this one, so an install should look like LocalAI before anyone has chosen
// anything. The OS setting no longer picks light on first load — the toggle
// does, and once used it is remembered and wins forever after.
function getInitialTheme() {
  const stored = localStorage.getItem('localai-theme')
  if (stored) return stored
  return 'dark'
}

export function ThemeProvider({ children }) {
  const [theme, setTheme] = useState(getInitialTheme)

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem('localai-theme', theme)
  }, [theme])

  const toggleTheme = () => {
    setTheme(prev => prev === 'dark' ? 'light' : 'dark')
  }

  return (
    <ThemeContext.Provider value={{ theme, toggleTheme }}>
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
