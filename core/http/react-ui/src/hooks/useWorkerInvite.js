import { useCallback, useEffect, useRef, useState } from 'react'
import { p2pNetApi } from '../utils/api'

// The P2P side of the Add worker flow: the mode of P2P on this server (an
// invite needs "share"; null until it is read), and the worker invite once it
// is minted. The link is in the answer of the mint only, so it lives here and
// nowhere else. An invite is minted when `active` turns on and the mode is
// "share"; `mint` makes a new one.
export function useWorkerInvite({ active }) {
  const [mode, setMode] = useState(null)
  const [minted, setMinted] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const asked = useRef(false)

  useEffect(() => {
    let live = true
    p2pNetApi.status()
      .then(status => { if (live) setMode(status?.mode || (status?.enabled ? 'share' : 'off')) })
      .catch(() => { if (live) setMode('off') })
    return () => { live = false }
  }, [])

  const mint = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      setMinted(await p2pNetApi.mintInvite({ kind: 'worker' }))
    } catch (err) {
      setError(err.message || '')
    } finally {
      setBusy(false)
    }
  }, [])

  useEffect(() => {
    if (active && mode === 'share' && !asked.current) {
      asked.current = true
      void mint()
    }
  }, [active, mode, mint])

  return { mode, minted, busy, error, mint }
}
