import { useEffect, useRef } from 'react'
import { useTheme } from '../../contexts/ThemeContext'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { setPreferredModel } from '../../utils/lastModel'
import './wellKnown.css'

function systemTheme() {
  try { return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark' } catch { return 'dark' }
}

// applyAppearance puts the values of ui.json on the page: the theme, the
// accent colour, the density and the default model. Keys that are absent
// leave the page as it is, so a missing file changes nothing.
export function applyAppearance(ui, setTheme) {
  const v = ui || {}
  const root = document.documentElement
  if (v.theme === 'dark' || v.theme === 'light') setTheme?.(v.theme)
  if (v.theme === 'system') setTheme?.(systemTheme())
  if (typeof v.accent === 'string' && /^#[0-9a-fA-F]{6}$/.test(v.accent)) {
    root.style.setProperty('--dk-accent', v.accent)
    root.style.setProperty('--dk-accent-text', `color-mix(in srgb, ${v.accent} 78%, var(--dk-text))`)
    root.style.setProperty('--dk-accent-wash', `color-mix(in srgb, ${v.accent} 16%, transparent)`)
  } else {
    root.style.removeProperty('--dk-accent')
    root.style.removeProperty('--dk-accent-text')
    root.style.removeProperty('--dk-accent-wash')
  }
  if (v.density === 'compact') root.setAttribute('data-density', 'compact')
  else root.removeAttribute('data-density')
  setPreferredModel(v.defaultModel || '')
}

// AppearanceSync applies the user's ui.json when the app starts and whenever
// the file changes, whoever changed it.
export default function AppearanceSync() {
  const { setTheme } = useTheme()
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  const applied = useRef(null)
  useEffect(() => {
    if (!doc || applied.current === doc.version) return
    applied.current = doc.version
    applyAppearance(doc.data, setTheme)
  }, [doc, setTheme])
  return null
}
