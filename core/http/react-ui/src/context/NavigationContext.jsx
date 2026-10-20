import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useAuth } from './AuthContext'
import { pinsApi } from '../utils/pinsClient'
import { PIN_CAP, togglePin, movePin } from '../utils/addonTiles'

// What the shell needs to draw the Add-ons item, the drawer and the Pinned
// section: the built-in catalog and the caller's pins. Both come from the
// server, so a pin follows the user across browsers.
const EMPTY = Object.freeze({
  status: 'idle', builtin: [], pinIds: [],
  setPins: async () => {}, pin: async () => ({ needsUnpin: false }), unpin: async () => {}, move: async () => {},
})
const NavigationContext = createContext(EMPTY)

// eslint-disable-next-line react-refresh/only-export-components
export function useNavigation() {
  return useContext(NavigationContext)
}

export function NavigationProvider({ children }) {
  const { loading, authEnabled, user } = useAuth()
  const [state, setState] = useState({ status: 'idle', builtin: [], pinIds: [] })
  const pinsRef = useRef([])
  pinsRef.current = state.pinIds
  const signedIn = !loading && (!authEnabled || Boolean(user))

  useEffect(() => {
    if (!signedIn) return undefined
    const ctl = new AbortController()
    setState(s => ({ ...s, status: 'loading' }))
    Promise.all([pinsApi.listBuiltin(ctl.signal), pinsApi.listPins(ctl.signal)])
      .then(([builtin, pinIds]) => setState({ status: 'ready', builtin, pinIds }))
      .catch(() => { if (!ctl.signal.aborted) setState(s => ({ ...s, status: 'error' })) })
    return () => ctl.abort()
  }, [signedIn, user?.id])

  const setPins = useCallback(async (ids) => {
    const previous = pinsRef.current
    setState(s => ({ ...s, pinIds: ids }))
    try {
      const saved = await pinsApi.putPins(ids)
      setState(s => ({ ...s, pinIds: saved }))
    } catch (error) {
      setState(s => ({ ...s, pinIds: previous }))
      throw error
    }
  }, [])

  const pin = useCallback(async (id) => {
    const next = togglePin(pinsRef.current.filter(x => x !== id), id, PIN_CAP)
    if (next.needsUnpin) return { needsUnpin: true }
    await setPins(next.ids)
    return { needsUnpin: false }
  }, [setPins])
  const unpin = useCallback((id) => setPins(pinsRef.current.filter(x => x !== id)), [setPins])
  const move = useCallback((id, delta) => setPins(movePin(pinsRef.current, id, delta)), [setPins])

  const value = useMemo(() => ({ ...state, setPins, pin, unpin, move }), [state, setPins, pin, unpin, move])
  return <NavigationContext.Provider value={value}>{children}</NavigationContext.Provider>
}
