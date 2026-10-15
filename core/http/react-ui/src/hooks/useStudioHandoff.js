import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { readAllMediaHistory } from './useMediaHistory'
import { apiUrl } from '../utils/basePath'
import { collectWork, readHandoff, sourceUrlFor } from '../utils/studioWork'

// What the Studio front page hands to a workspace: a prompt, a model, a size, a
// count, and the id of the result the new one grows from. A workspace calls this
// once and seeds its own form from it; with no query string it returns empty
// values and the workspace behaves as it always has.
export function useStudioHandoff() {
  const [params] = useSearchParams()
  const key = params.toString()
  // The query string is the only input, so its text is the dependency.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useMemo(() => readHandoff(params), [key])
}

// The file a hand-off starts from, fetched from the server's own output URL.
//
//   status  'none'     no source was asked for
//           'loading'  fetching
//           'ready'    `blob` and `item` are set
//           'missing'  the result is no longer in this browser's history
//           'error'    the file could not be fetched (the server may have
//                      cleaned it up)
//
// Only the browser-storage lists are searched. A 3D result has no file URL, and
// nothing accepts a mesh as a source yet.
export function useHandoffSource(handoff, enabled = true) {
  const { from, edge } = handoff
  const [state, setState] = useState({ status: from && enabled ? 'loading' : 'none', blob: null, item: null })

  useEffect(() => {
    if (!from || !enabled) { setState({ status: 'none', blob: null, item: null }); return undefined }
    const item = collectWork(readAllMediaHistory(), []).find(i => i.id === from) || null
    const url = sourceUrlFor(item, edge)
    if (!item || !url) { setState({ status: 'missing', blob: null, item }); return undefined }
    let cancelled = false
    setState({ status: 'loading', blob: null, item })
    fetch(url.startsWith('http') || url.startsWith('data:') ? url : apiUrl(url))
      .then(res => { if (!res.ok) throw new Error(`HTTP ${res.status}`); return res.blob() })
      .then(blob => { if (!cancelled) setState({ status: 'ready', blob, item }) })
      .catch(() => { if (!cancelled) setState({ status: 'error', blob: null, item }) })
    return () => { cancelled = true }
  }, [from, edge, enabled])

  return state
}

// A blob as the { base64, dataUrl, mime } shape the image inputs hold.
export function blobToImageInput(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const dataUrl = String(reader.result)
      resolve({ base64: dataUrl.split(',')[1] || '', dataUrl, mime: blob.type || 'image/png', source: 'upload' })
    }
    reader.onerror = reject
    reader.readAsDataURL(blob)
  })
}

export function blobToFile(blob, name) {
  return new File([blob], name, { type: blob.type || 'application/octet-stream' })
}
