import { useCallback, useEffect, useRef, useState } from 'react'
import { chatChangesApi } from '../utils/api'
import { CHANGES_EVENT } from '../utils/changeProposal'

const POLL_MS = 60_000

// The change proposals that wait for the person: how many, for the badge of the
// inbox and the Proposals page. It reads again when the tab is shown, every
// minute, when any card answers one (CHANGES_EVENT) and on refresh(). A server
// that keeps no proposals (files off, chats off) answers 404 or 409: the count
// then stays zero and the hook stops asking.
export function usePendingChanges() {
  const [items, setItems] = useState([])
  const [status, setStatus] = useState('loading')
  const [error, setError] = useState(null)
  const offRef = useRef(false)

  const refresh = useCallback(async () => {
    if (offRef.current) return
    try {
      const list = await chatChangesApi.list('pending')
      setItems(Array.isArray(list) ? list : [])
      setStatus('ready')
      setError(null)
    } catch (e) {
      if (e?.status === 404 || e?.status === 409) { offRef.current = true; setItems([]); setStatus('ready'); return }
      setError(e)
      setStatus('error')
    }
  }, [])

  useEffect(() => {
    refresh()
    const timer = setInterval(refresh, POLL_MS)
    const onShow = () => { if (document.visibilityState === 'visible') refresh() }
    window.addEventListener(CHANGES_EVENT, refresh)
    document.addEventListener('visibilitychange', onShow)
    return () => {
      clearInterval(timer)
      window.removeEventListener(CHANGES_EVENT, refresh)
      document.removeEventListener('visibilitychange', onShow)
    }
  }, [refresh])

  return { items, count: items.length, status, error, refresh }
}
