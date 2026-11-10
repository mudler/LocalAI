import { useEffect, useRef } from 'react'
import { useTheme } from '../../contexts/ThemeContext'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { setPreferredModel } from '../../utils/lastModel'
import { chatStartModel } from '../../utils/defaults'
import { lookAttributes, lookOf, accentCheck, onAccentRole } from '../../utils/look'
import { pageMode, paletteOf } from './contrast'
import './wellKnown.css'
import './look.css'

function systemTheme() {
  try { return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark' } catch { return 'dark' }
}

const ACCENT_VARS = ['--dk-accent', '--dk-accent-text', '--dk-accent-wash', '--dk-on-accent']

// applyAccent sets the accent of ui.json on the page when it keeps the
// contrast of the theme mode, and takes it off otherwise: a faint accent would
// make links and buttons unreadable, so the theme's own accent shows instead.
// The page carries data-accent-ignored then, and the Appearance tab says why.
// It answers the check.
export function applyAccent(accent, mode) {
  const root = document.documentElement
  const ok = typeof accent === 'string' && /^#[0-9a-fA-F]{6}$/.test(accent)
  const palette = paletteOf(mode)
  const check = ok ? accentCheck(accent, palette) : null
  if (check?.ok) {
    root.style.setProperty('--dk-accent', accent)
    root.style.setProperty('--dk-accent-text', `color-mix(in srgb, ${accent} 78%, var(--dk-text))`)
    root.style.setProperty('--dk-accent-wash', `color-mix(in srgb, ${accent} 16%, transparent)`)
    root.style.setProperty('--dk-on-accent', `var(${onAccentRole(accent, palette)})`)
    root.removeAttribute('data-accent-ignored')
  } else {
    ACCENT_VARS.forEach(v => root.style.removeProperty(v))
    if (check) root.setAttribute('data-accent-ignored', accent)
    else root.removeAttribute('data-accent-ignored')
  }
  return check
}

// applyLook puts the look of ui.json on the page as data attributes; the
// tokens of each value are in look.css. A value outside its list is left off.
export function applyLook(ui) {
  const root = document.documentElement
  for (const [attr, value] of Object.entries(lookAttributes(lookOf(ui)))) {
    if (value) root.setAttribute(attr, value)
    else root.removeAttribute(attr)
  }
}

// applyAppearance puts the values of ui.json on the page: the theme, the
// accent colour, the density and the look. Keys that are absent leave the
// page as it is, so a missing file changes nothing.
export function applyAppearance(ui, setTheme) {
  const v = ui || {}
  const root = document.documentElement
  if (v.theme === 'dark' || v.theme === 'light') setTheme?.(v.theme)
  if (v.theme === 'system') setTheme?.(systemTheme())
  applyAccent(v.accent, pageMode(v.theme))
  if (v.density === 'compact') root.setAttribute('data-density', 'compact')
  else root.removeAttribute('data-density')
  applyLook(v)
}

// AppearanceSync applies the user's ui.json when the app starts and whenever
// the file changes, whoever changed it. The accent is checked again when the
// theme changes, since a colour that reads on the dark page may not on the
// light one.
export default function AppearanceSync() {
  const { theme, setTheme } = useTheme()
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  const { doc: defaults } = useWellKnown('defaults', { enabled: on })
  const applied = useRef(null)
  useEffect(() => {
    if (!doc || applied.current === doc.version) return
    applied.current = doc.version
    applyAppearance(doc.data, setTheme)
  }, [doc, setTheme])
  // The model a new chat starts with: the chat default of defaults.json, or
  // the older defaultModel of ui.json when that file has none.
  useEffect(() => {
    if (!doc || !defaults) return
    setPreferredModel(chatStartModel(defaults.data, doc.data))
  }, [doc, defaults])
  useEffect(() => {
    if (doc) applyAccent(doc.data?.accent, theme === 'light' ? 'light' : 'dark')
  }, [doc, theme])
  return null
}
