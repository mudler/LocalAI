import { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { brandingApi } from '../utils/api'

// Bundled defaults — used when the backend hasn't applied an override (or
// when /api/branding is briefly unreachable on first load).
const DEFAULT_BRANDING = {
  instanceName: 'LocalAI',
  instanceTagline: '',
  logoUrl: '/static/logo.png',
  logoHorizontalUrl: '/static/logo_horizontal.png',
  faviconUrl: '/favicon.svg',
}

const BrandingContext = createContext(null)

// Reads /api/branding (public — works pre-auth so the login screen renders
// the configured branding) and exposes the resolved values plus a refresh()
// callback used by the Settings page after save/upload.
export function BrandingProvider({ children }) {
  const [branding, setBranding] = useState(DEFAULT_BRANDING)
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const data = await brandingApi.get()
      setBranding({
        instanceName: data?.instance_name || DEFAULT_BRANDING.instanceName,
        instanceTagline: data?.instance_tagline || '',
        logoUrl: data?.logo_url || DEFAULT_BRANDING.logoUrl,
        logoHorizontalUrl: data?.logo_horizontal_url || DEFAULT_BRANDING.logoHorizontalUrl,
        faviconUrl: data?.favicon_url || DEFAULT_BRANDING.faviconUrl,
      })
    } catch (_e) {
      // /api/branding should always succeed (it's public and zero-side-effect).
      // If it doesn't, fall through to defaults so the UI still renders.
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // Drive document.title and the favicon link from branding state. Bust the
  // favicon cache by appending a query so changes show up without forcing a
  // hard reload — most browsers respect the URL change.
  useEffect(() => {
    if (!loaded) return
    document.title = branding.instanceName
    const link = document.querySelector("link[rel='icon']") || document.querySelector("link[rel='shortcut icon']")
    if (link) {
      const href = branding.faviconUrl
      link.href = href.includes('?') ? href : `${href}?v=${Date.now()}`
    }
  }, [branding, loaded])

  return (
    <BrandingContext.Provider value={{ ...branding, loaded, refresh }}>
      {children}
    </BrandingContext.Provider>
  )
}

export function useBranding() {
  const ctx = useContext(BrandingContext)
  if (!ctx) throw new Error('useBranding must be used within a BrandingProvider')
  return ctx
}
