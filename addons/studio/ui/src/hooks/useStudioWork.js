import { useCallback, useEffect, useMemo, useState } from 'react'
import { clearAllMediaHistory, FAVOURITES_KEY, readAllMediaHistory, useMediaHistory } from './useMediaHistory'
import { use3DHistory } from './use3DHistory'
import { collectWork, pruneIds, toggleId } from '../utils/studioWork'

function readFavourites() {
  try {
    const stored = JSON.parse(localStorage.getItem(FAVOURITES_KEY) || '[]')
    return Array.isArray(stored) ? stored.filter(id => typeof id === 'string') : []
  } catch {
    return []
  }
}

function writeFavourites(ids) {
  try {
    if (ids.length === 0) localStorage.removeItem(FAVOURITES_KEY)
    else localStorage.setItem(FAVOURITES_KEY, JSON.stringify(ids))
  } catch { /* quota or private mode: the star simply does not stick */ }
}

// Everything the Studio front page shows as "your work": the entries each
// workspace stored, in one list, with favourites. The browser-storage lists are
// read once on mount, except images: inline upscaling writes through the same
// image history hook and merges its live entries. 3D arrives from IndexedDB.
//
// `ready` is false until 3D has answered, so the page can show a skeleton
// instead of flashing an empty state for work that is a moment away.
export function useStudioWork() {
  const { addEntry: addImageEntry, historyProps: imageHistory } = useMediaHistory('image')
  const clearImages = imageHistory.onClearAll
  const [media, setMedia] = useState(() => readAllMediaHistory())
  const [favourites, setFavourites] = useState(readFavourites)
  const { entries: threeD, clearAll: clearThreeD } = use3DHistory()
  const [ready, setReady] = useState(false)

  // use3DHistory starts with [] and fills from IndexedDB; one tick later it has
  // answered either way, which is all "ready" needs to mean.
  useEffect(() => {
    const id = setTimeout(() => setReady(true), 0)
    return () => clearTimeout(id)
  }, [])
  useEffect(() => { if (threeD.length > 0) setReady(true) }, [threeD])

  const items = useMemo(
    () => collectWork({ ...media, image: imageHistory.entries }, threeD, { favourites }),
    [media, imageHistory.entries, threeD, favourites],
  )

  const toggleFavourite = useCallback((id) => {
    setFavourites(prev => {
      const next = toggleId(pruneIds(prev, items), id)
      writeFavourites(next)
      return next
    })
  }, [items])

  const clearHistory = useCallback(async () => {
    clearImages()
    clearAllMediaHistory()
    setMedia(readAllMediaHistory())
    setFavourites([])
    await clearThreeD()
  }, [clearThreeD, clearImages])

  return { items, ready, addImageEntry, toggleFavourite, clearHistory }
}
