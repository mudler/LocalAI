import { useEffect, useState } from 'react'
import { addonsApi } from '../utils/addonsClient'
import { apiUrl } from '../utils/basePath'

// The icon URL of every add-on the person may see, by add-on id, for a page
// that draws a connection or an app by the add-on behind it. Best effort: a
// failed read, or a host without add-ons, gives an empty map and the tile is
// drawn without a file (its glyph, or its letter).
export function useAddonIcons(enabled = true) {
  const [icons, setIcons] = useState({})
  useEffect(() => {
    if (!enabled) return undefined
    const ctl = new AbortController()
    addonsApi.apps.list(ctl.signal)
      .then((apps) => { if (!ctl.signal.aborted) setIcons(Object.fromEntries((apps || []).filter((a) => a.icon).map((a) => [a.id, apiUrl(a.icon)]))) })
      .catch(() => {})
    return () => ctl.abort()
  }, [enabled])
  return icons
}
