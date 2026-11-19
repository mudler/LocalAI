import { createContext, useContext, useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { AuthContext } from '../context/authState'
import { wellKnownStore } from '../hooks/useWellKnown'
import { onStored } from '../utils/storage'
import { isTheme, readTheme, writeTheme, saveUiTheme, themeStorageNames } from '../utils/theme'

const ThemeContext = createContext()

// ThemeProvider sits below AuthProvider: the browser copy of the theme is
// kept per account (see utils/theme.js). `theme` is the one on the page;
// `setTheme` changes it for good, `previewTheme` only shows another one (the
// Appearance tab) and `previewTheme(null)` puts the real one back.
export function ThemeProvider({ children }) {
  const auth = useContext(AuthContext)
  const account = auth?.user?.id || ''
  const canUseUi = !!auth && (!auth.hasFeature || auth.hasFeature('chats'))
  const [state, setState] = useState(() => ({ account, theme: readTheme(account) }))
  const [preview, setPreview] = useState(null)
  // The account is known after the first paint: take its own copy, in the
  // same render, so the copy of the other account is never overwritten.
  if (state.account !== account) setState({ account, theme: readTheme(account) })
  const theme = state.account === account ? state.theme : readTheme(account)
  const shown = preview || theme
  const setThemeState = useCallback((next) => setState((s) => (s.theme === next ? s : { ...s, theme: next })), [])

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', shown)
  }, [shown])
  useEffect(() => { writeTheme(state.account, state.theme) }, [state])

  // Another tab changed the choice.
  useEffect(() => {
    const offs = themeStorageNames(account).map((name) => onStored(name, (v) => { if (isTheme(v)) setThemeState(v) }))
    return () => offs.forEach((off) => off())
  }, [account, setThemeState])

  const setTheme = useCallback((next) => { if (isTheme(next)) setThemeState(next) }, [setThemeState])
  // A preview equal to the real theme is no preview: a later toggle must show.
  useEffect(() => { setPreview(null) }, [theme])
  const themeRef = useRef(theme)
  themeRef.current = theme
  const previewTheme = useCallback((next) => setPreview(isTheme(next) && next !== themeRef.current ? next : null), [])

  // Cross-fade the switch where the View Transitions API exists. The
  // attribute changes inside the transition callback, so the browser
  // snapshots the old theme and fades to the new one with no flash. Without
  // the API, or when the person asks for less motion, the switch is instant.
  // The choice goes to ui.json when the person may use it, so a later save of
  // that file (a pin) cannot bring the old theme back.
  const canUseUiRef = useRef(canUseUi)
  canUseUiRef.current = canUseUi
  const toggleTheme = useCallback(() => {
    const next = theme === 'dark' ? 'light' : 'dark'
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (canUseUiRef.current) saveUiTheme(next, wellKnownStore)
    if (typeof document.startViewTransition !== 'function' || reduce) {
      setThemeState(next)
      return
    }
    document.startViewTransition(() => {
      document.documentElement.setAttribute('data-theme', next)
      setThemeState(next)
    })
  }, [theme, setThemeState])

  const value = useMemo(
    () => ({ theme: shown, toggleTheme, setTheme, previewTheme }),
    [shown, toggleTheme, setTheme, previewTheme],
  )
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const context = useContext(ThemeContext)
  if (!context) {
    throw new Error('useTheme must be used within a ThemeProvider')
  }
  return context
}
