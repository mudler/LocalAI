/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// Pairing proves that the owner controls a platform account. The owner starts
// it with Verify in the table of people, gets a code, and sends the code as a
// direct message to the bot from an allowed account. The code is shown once and
// never saved by this page. A paired sender may use the sensitive permissions
// the owner switched on. The screen calls it verifying a person.
import { useEffect, useRef, useState } from 'react'
import { Notice } from './controls'
import { connectorsApi } from './api'
import { pageLocale } from './connector'

export const MAX_PAIRED = 3
const POLL_MS = 3000

export function time(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(pageLocale(), { dateStyle: 'medium', timeStyle: 'short' })
}

/**
 * The pairings of one saved binding and the code of the one being made. The
 * list follows the host: it is read again while a code is live, and a sender
 * that used it is reported to onPaired.
 */
export function usePairing(binding, onPaired) {
  const [paired, setPaired] = useState(binding.paired || [])
  const [code, setCode] = useState(null) // { code, expires }
  const [expired, setExpired] = useState(false)
  const [done, setDone] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const known = useRef(new Set((binding.paired || []).map((p) => p.sender_id)))
  const id = binding.id

  // While a code is live, look for the sender that used it.
  useEffect(() => {
    if (!code || !id) return undefined
    const stop = () => { clearInterval(timer) }
    const timer = setInterval(async () => {
      if (Date.now() > new Date(code.expires).getTime()) { setExpired(true); setCode(null); stop(); return }
      try {
        const r = await connectorsApi.list()
        const mine = (r.connectors || []).find((c) => c.id === id)
        const list = mine?.paired || []
        const fresh = list.find((p) => !known.current.has(p.sender_id))
        if (fresh) {
          known.current = new Set(list.map((p) => p.sender_id))
          setPaired(list)
          setCode(null)
          setDone(fresh.sender_id)
          onPaired?.(list)
          stop()
        }
      } catch (_e) { /* the next tick tries again */ }
    }, POLL_MS)
    return stop
  }, [code, id, onPaired])

  const start = async () => {
    setBusy(true); setError(''); setDone(''); setExpired(false)
    try { setCode(await connectorsApi.pair(id)) } catch (e) { setError(e?.message || '') } finally { setBusy(false) }
  }
  const remove = async (sender) => {
    setError('')
    try {
      const b = await connectorsApi.unpair(id, sender)
      const list = b?.paired || paired.filter((p) => p.sender_id !== sender)
      known.current = new Set(list.map((p) => p.sender_id))
      setPaired(list)
      onPaired?.(list)
    } catch (e) { setError(e?.message || '') }
  }
  return { paired, code, expired, done, error, busy, start, remove, hideCode: () => setCode(null), full: paired.length >= MAX_PAIRED, saved: Boolean(id) }
}

/** What the last verification did: an error, the person who was verified, an expired code, or the code itself. */
export function PairingNotices({ p, s }) {
  return (<>
    {p.error && <Notice tone="error" role="alert">{p.error}</Notice>}
    {p.done && <Notice tone="ok" role="status">{s('pair.done', { sender: p.done })}</Notice>}
    {p.expired && <Notice tone="info" role="status">{s('pair.codeGone')}</Notice>}
    {p.code && (
      <Notice tone="info" role="status" testId="connector-pair-code-box">
        <p className="cn-notice__title">{s('pair.codeTitle')}</p>
        <p className="cn-pairing__code" data-testid="connector-pair-code">{p.code.code}</p>
        <p className="cn-notice__text">{s('pair.codeHelp', { time: time(p.code.expires) })}</p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={p.hideCode}>{s('pair.dismiss')}</button>
      </Notice>
    )}
  </>)
}
