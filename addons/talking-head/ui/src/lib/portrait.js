// The portraits of the page: the square crop of an upload and the small
// library kept in this browser. Nothing here goes to the server until a turn
// sends the selected portrait to /video.

// The side of the stored portrait. The model works at 512x512 and the backend
// scales anything else to that size, so a larger image only costs bandwidth.
export const PORTRAIT_SIZE = 512
// How many portraits the browser keeps. A 512 px JPEG is about 40 to 80 kB of
// base64, so this stays far below the storage quota of a site.
export const MAX_PORTRAITS = 6
export const STORAGE_KEY = 'localai.talking-head.portraits'

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v))

// The square to cut from a width x height image. zoom 1 takes the largest
// square; zoom 2 a square half as wide. x and y (0 to 1) place it: 0.5 is the
// centre, 0 the left or top edge. The result is in source pixels.
export function squareCrop(width, height, { zoom = 1, x = 0.5, y = 0.5 } = {}) {
  const side = Math.min(width, height) / clamp(zoom, 1, 8)
  return {
    sx: Math.round((width - side) * clamp(x, 0, 1)),
    sy: Math.round((height - side) * clamp(y, 0, 1)),
    side: Math.round(side),
  }
}

// Draws the crop of `image` on a new square canvas and returns it as a JPEG
// data URL. Browser only.
export function cropToDataUrl(image, crop, size = PORTRAIT_SIZE) {
  const canvas = document.createElement('canvas')
  canvas.width = size
  canvas.height = size
  const g = canvas.getContext('2d')
  g.imageSmoothingQuality = 'high'
  g.drawImage(image, crop.sx, crop.sy, crop.side, crop.side, 0, 0, size, size)
  return canvas.toDataURL('image/jpeg', 0.9)
}

// The base64 payload of a data URL, as /video takes it.
export function base64Of(dataUrl) {
  const text = String(dataUrl || '')
  const comma = text.indexOf(',')
  return comma === -1 ? text : text.slice(comma + 1)
}

// The stored library: [{ id, name, dataUrl }], newest first. Storage may be
// missing or full (a private window, a blocked site), so every access is
// guarded and a failure keeps the library in memory only.
export function loadPortraits(storage = globalThis.localStorage) {
  try {
    const list = JSON.parse(storage?.getItem(STORAGE_KEY) || '[]')
    return Array.isArray(list) ? list.filter((p) => p && typeof p.id === 'string' && /^data:image\//.test(p.dataUrl || '')) : []
  } catch {
    return []
  }
}

// Returns false when the browser refused to keep the list.
export function savePortraits(list, storage = globalThis.localStorage) {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(list.slice(0, MAX_PORTRAITS)))
    return true
  } catch {
    return false
  }
}

// Puts a new portrait first and drops the oldest beyond the limit.
export function addPortrait(list, portrait) {
  return [portrait, ...list.filter((p) => p.id !== portrait.id)].slice(0, MAX_PORTRAITS)
}

export function removePortrait(list, id) {
  return list.filter((p) => p.id !== id)
}
