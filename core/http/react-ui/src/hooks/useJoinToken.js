import { useCallback, useEffect, useRef, useState } from 'react'
import { clusterApi } from '../utils/api'
import { mintBody, needsToken } from '../utils/addWorker'

// The join token of the Add worker flow, minted through POST
// /api/cluster/join-tokens. The answer holds the plain token and the commands
// that carry it, once: it lives in this hook and nowhere else. A token is
// minted when the step opens and again when the connections, the uses or the
// lifetime change; the token it replaces is revoked, so a page that is left
// open does not leave live tokens behind.
//
//   status  'idle' | 'minting' | 'ready' | 'error'
//
// `mintAgain` makes a new one with the same settings (for an expired token).
export function useJoinToken({ ways, uses, ttl, enabled }) {
  const [state, setState] = useState({ status: 'idle', minted: null, error: '' })
  const wanted = enabled && needsToken(ways)
  const request = useRef(0)
  const lastId = useRef(null)
  const body = JSON.stringify(mintBody({ ways, uses, ttl }))

  const mint = useCallback(async () => {
    const mine = ++request.current
    setState(current => ({ status: 'minting', minted: current.minted, error: '' }))
    try {
      const minted = await clusterApi.mintJoinToken(JSON.parse(body))
      if (mine !== request.current) {
        // A newer mint started while this one ran: this token is not shown.
        clusterApi.revokeJoinToken(minted.id).catch(() => {})
        return
      }
      const previous = lastId.current
      lastId.current = minted.id
      if (previous) clusterApi.revokeJoinToken(previous).catch(() => {})
      setState({ status: 'ready', minted, error: '' })
    } catch (error) {
      if (mine === request.current) setState({ status: 'error', minted: null, error: error.message || '' })
    }
  }, [body])

  useEffect(() => {
    if (!wanted) return undefined
    void mint()
    return undefined
  }, [wanted, mint])

  // A mint that is still running when the flow closes is not shown.
  useEffect(() => () => {
    request.current += 1
  }, [])

  return { ...state, mintAgain: mint, wanted }
}
