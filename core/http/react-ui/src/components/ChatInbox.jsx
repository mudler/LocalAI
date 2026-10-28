/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { disablePush, enablePush, pushState } from '../utils/chatPush'
import { formatNext } from '../utils/chatTrigger'
import Icon from './Icon'
import '../pages/chatauto.css'

// Browser notifications: a button that asks for permission and subscribes the
// browser, or says why it cannot.
function PushSwitch({ t }) {
  const [state, setState] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true
    pushState().then((s) => { if (live) setState(s) })
    return () => { live = false }
  }, [])
  if (!state || state === 'unsupported') return null
  if (state === 'denied') return <p className="cs-muted">{t('inbox.pushDenied')}</p>
  const toggle = async () => {
    setError('')
    try {
      setState(state === 'on' ? await disablePush() : await enablePush())
    } catch (e) {
      setError(e?.status === 409 ? t('inbox.pushUnavailable') : (e?.message || t('errors.send')))
    }
  }
  return (
    <div>
      <button type="button" className="btn btn-secondary btn-sm" onClick={toggle}>
        {state === 'on' ? t('inbox.pushOff') : t('inbox.pushOn')}
      </button>
      {error && <p className="ch-error" role="alert">{error}</p>}
    </div>
  )
}

// The inbox button of the chat header and its panel.
export default function ChatInbox({ inbox }) {
  const { t } = useTranslation('chats')
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const ref = useRef(null)
  // The panel floats over the page: a click outside or Esc closes it.
  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    const onKey = (e) => { if (e.key === 'Escape') { e.stopPropagation(); setOpen(false) } }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])
  if (inbox.off) return null

  // A failed mark or delete shows under the inbox; the list is as the server has it.
  const attempt = async (call) => {
    setError('')
    try { await call() } catch (e) { setError(e?.message || t('errors.send')) }
  }

  const openItem = async (n) => {
    if (!n.read) await inbox.markRead([n.id]).catch(() => {})
    if (n.link) navigate(n.link)
  }

  return (
    <div className="ca-inbox" data-testid="chat-inbox" ref={ref}>
      <div className="ca-inbox__head">
        <button
          type="button"
          className="cx-icobtn cx-icobtn--lg ca-inbox__btn"
          aria-expanded={open}
          aria-label={t('list.inbox')}
          title={t('list.inbox')}
          onClick={() => setOpen(!open)}
        >
          <Icon name="inbox" />
          {inbox.unread > 0 && <span className="ca-count" data-testid="inbox-count" aria-label={t('inbox.unread', { count: inbox.unread })}>{inbox.unread}</span>}
        </button>
      </div>
      {open && (
        <div className="ca-inbox__panel" role="dialog" aria-label={t('list.inbox')}>
          {inbox.unread > 0 && (
            <button type="button" className="ch-link" onClick={() => attempt(() => inbox.markAllRead())}>{t('inbox.markAll')}</button>
          )}
          {error && <p className="ch-error" role="alert">{error}</p>}
          {inbox.items.length === 0 && <p className="cs-muted">{t('inbox.empty')}</p>}
          <ul className="cs-list">
            {inbox.items.map((n) => (
              <li key={n.id} className={`ca-inbox__item${n.read ? '' : ' ca-inbox__item--unread'}`} data-testid="inbox-item">
                <button type="button" className="ch-link ca-inbox__title" onClick={() => openItem(n)}>{n.title}</button>
                {n.body && <span className="cs-muted">{n.body}</span>}
                <span className="ca-inbox__meta">
                  <span>{formatNext(n.created, '')}</span>
                  <button type="button" className="ch-link ch-link--danger" onClick={() => attempt(() => inbox.remove([n.id]))}>{t('inbox.delete')}</button>
                </span>
              </li>
            ))}
          </ul>
          <PushSwitch t={t} />
        </div>
      )}
    </div>
  )
}
