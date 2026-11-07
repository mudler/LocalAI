import { useEffect, useState } from 'react'
import { chatConnectorsApi } from '../utils/api'
import { oauthErrorKey, oauthErrorText, readOAuthReturn, returnPath, startBody } from '../utils/connectorOAuth'

// The state and the actions of the connections that log in with OAuth on the
// Connectors page: the result of a login the service sent the browser back
// with, the login that is starting, the one the person is leaving, and the
// notice after a disconnect. The page passes its translator, its location and
// query setter, the error line of the page (setError), its generic failure
// handler (fail) and its reload (load). A page that wants to know about a typed
// refusal of a start (needsApproval, for one) passes onTypedError(key), which returns true when it showed the
// refusal itself and the error line stays empty.
export function useOAuthConnections({ t, location, setParams, setError, fail, load, onTypedError }) {
  // The result of a login the service sent the browser back with, read once:
  // the query is stripped right away, so a reload does not say it again.
  const [returned, setReturned] = useState(() => readOAuthReturn(location.search))
  const [starting, setStarting] = useState('')
  const [leaving, setLeaving] = useState(null)
  const [disconnecting, setDisconnecting] = useState(false)
  const [notice, setNotice] = useState(null)

  useEffect(() => {
    if (returned) setParams(new URLSearchParams(returned.search), { replace: true })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps -- once, on the page the login came back to

  const oauthFail = (e) => {
    const text = oauthErrorText(t, e, 'connectorsPage.oauth.errors')
    // The page may say it in its own place instead (it returns true).
    if (text && onTypedError?.(oauthErrorKey(e)) === true) return
    if (text) setError(text)
    else fail(e)
  }

  // The browser leaves for the service; the button keeps spinning until it does.
  const connect = async (c, scopes) => {
    setError('')
    setNotice(null)
    setReturned(null)
    setStarting(c.id)
    try {
      const r = await chatConnectorsApi.oauthStart(c.id, startBody(scopes, returnPath(location.pathname, location.search)))
      window.location.assign(r.authorize_url)
    } catch (e) {
      setStarting('')
      oauthFail(e)
    }
  }

  const disconnect = async () => {
    const c = leaving
    setDisconnecting(true)
    setError('')
    setReturned(null)
    try {
      const r = await chatConnectorsApi.oauthDisconnect(c.id)
      setNotice({ tone: r?.revoked ? 'ok' : 'warn', text: t(r?.revoked ? 'connectorsPage.oauth.disconnected' : 'connectorsPage.oauth.disconnectedNotTold', { name: c.name }) })
      await load()
    } catch (e) {
      oauthFail(e)
    } finally {
      setDisconnecting(false)
      setLeaving(null)
    }
  }

  return { returned, setReturned, starting, leaving, setLeaving, disconnecting, notice, setNotice, connect, disconnect }
}
