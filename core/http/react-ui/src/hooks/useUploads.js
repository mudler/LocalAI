import { useEffect, useSyncExternalStore } from 'react'
import { filesApi } from '../utils/api'
import { createUploadQueue } from '../utils/uploadQueue'
import { invalidate } from './useWorkspaceFeed'

// The one upload queue of the app. It outlives the Files page, so a folder
// change or a visit to another page does not stop an upload.
export const uploads = createUploadQueue({
  send: (item, { onProgress, signal }) => filesApi.uploadWithProgress(item.path, item.file, {
    replace: item.mode === 'replace', onProgress, signal,
  }),
})

// The page reads the folder again itself; the feed's echo of an upload it
// made is not needed.
uploads.onSettled((item) => {
  if (item.status === 'done' && item.result?.path) invalidate(item.result.path, item.result.rev)
})

// useUploads returns { items, active } and keeps the tab from closing while
// a file is still on its way.
export function useUploads() {
  const snap = useSyncExternalStore(uploads.subscribe, uploads.getSnapshot)
  const busy = snap.active > 0
  useEffect(() => {
    if (!busy) return undefined
    const warn = (e) => { e.preventDefault(); e.returnValue = '' }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [busy])
  return snap
}
