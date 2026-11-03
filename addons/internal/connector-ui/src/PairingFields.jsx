/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// Pairing proves that the owner controls a platform account. The owner starts
// it here, gets a code, and sends the code as a direct message to the bot from
// an allowed account. The code is shown once and never saved by this page. A
// paired sender may use the sensitive permissions the owner switched on.
import { useEffect, useRef, useState } from 'react'
import { Notice } from './controls'
import { connectorsApi } from './api'

export const PAIRING_TEXT = {
  'pair.title': 'Paired senders',
  'pair.help': 'A paired sender proved that they control an allowed account. Only a paired sender can use the sensitive permissions. At most {{max}}; a pairing ends after {{days}} days.',
  'pair.start': 'Pair a sender',
  'pair.startHelp': 'Makes a code. Send it to the bot as a direct message from the account to pair, within ten minutes. The code works once.',
  'pair.needSave': 'Save this bot first, then pair a sender.',
  'pair.codeTitle': 'Your pairing code',
  'pair.codeHelp': 'Send this code to the bot as a direct message from the account to pair. It is shown only now, works once and expires at {{time}}.',
  'pair.codeGone': 'The code expired. Make a new one if the sender is not listed yet.',
  'pair.done': 'Paired: {{sender}}.',
  'pair.dismiss': 'Hide the code',
  'pair.none': 'Nobody is paired.',
  'pair.expires': 'ends {{time}}',
  'pair.remove': 'Remove',
  'pair.removeSender': 'Remove the pairing of {{sender}}',
  'pair.full': 'Three senders are paired. Remove one to pair another.',
}

const MAX_PAIRED = 3
const DAYS = 90
const POLL_MS = 3000

function time(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
}

export default function PairingFields({ binding, s, onPaired }) {
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
          onPaired?.()
          stop()
        }
      } catch (_e) { /* the next tick tries again */ }
    }, POLL_MS)
    return stop
  }, [code, id, onPaired])

  if (!id) {
    return (
      <div className="cn-sub" data-testid="connector-pairing">
        <h3 className="cn-sub__title">{s('pair.title')}</h3>
        <p className="cn-muted">{s('pair.needSave')}</p>
      </div>
    )
  }

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
      onPaired?.()
    } catch (e) { setError(e?.message || '') }
  }
  const full = paired.length >= MAX_PAIRED

  return (
    <div className="cn-sub" data-testid="connector-pairing">
      <h3 className="cn-sub__title">{s('pair.title')}</h3>
      <p className="dk-hint">{s('pair.help', { max: MAX_PAIRED, days: DAYS })}</p>
      {error && <Notice tone="error" role="alert">{error}</Notice>}
      {paired.length === 0 ? <p className="cn-muted">{s('pair.none')}</p> : (
        <ul className="cn-runs" aria-label={s('pair.title')} data-testid="connector-paired">
          {paired.map((p) => (
            <li key={p.sender_id}>
              <span className="cn-mono">{p.sender_id}</span>
              <span className="cn-muted">{s('pair.expires', { time: time(p.expires_at) })}</span>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={s('pair.removeSender', { sender: p.sender_id })} onClick={() => remove(p.sender_id)}>{s('pair.remove')}</button>
            </li>
          ))}
        </ul>
      )}
      {done && <Notice tone="ok" role="status" testId="connector-pair-done">{s('pair.done', { sender: done })}</Notice>}
      {expired && <Notice tone="info" role="status">{s('pair.codeGone')}</Notice>}
      {code && (
        <Notice tone="info" role="status" testId="connector-pair-code-box">
          <p className="cn-notice__title">{s('pair.codeTitle')}</p>
          <p className="cn-pairing__code" data-testid="connector-pair-code">{code.code}</p>
          <p className="cn-notice__text">{s('pair.codeHelp', { time: time(code.expires) })}</p>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setCode(null)}>{s('pair.dismiss')}</button>
        </Notice>
      )}
      <div className="cn-switchfield">
        <div>
          <button type="button" className="dk-btn dk-btn--secondary" data-testid="connector-pair-start" disabled={busy || full} onClick={start}>{s('pair.start')}</button>
        </div>
        <p className="dk-hint">{full ? s('pair.full') : s('pair.startHelp')}</p>
      </div>
    </div>
  )
}
