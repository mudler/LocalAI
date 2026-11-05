/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Callout } from '../ui'
import { chatsApi } from '../../utils/api'
import { notApplied, tryLines, tryRequest } from '../../utils/agentTryIt'
import { useSheetFocus } from './useSheetFocus'
import './agentparts.css'

const POLL_MS = 1000
const GIVE_UP_MS = 120_000

// The Try it sheet: send a message to a test chat that uses the settings now
// in the form, without saving the agent. The chat is created on the first
// message and deleted when the sheet closes.
export default function AgentTryIt({ form, tools, models, onClose }) {
  const { t } = useTranslation('agents')
  const { sheetRef, closeRef } = useSheetFocus(onClose)
  const [text, setText] = useState('')
  const [lines, setLines] = useState([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const chatRef = useRef('')
  const liveRef = useRef(true)
  const taRef = useRef(null)
  const request = tryRequest({ form, tools, models })
  const skipped = notApplied(form)

  // Delete the test chat on close, also when the page goes away.
  const cleanup = useCallback(() => {
    const id = chatRef.current
    chatRef.current = ''
    if (id) chatsApi.stop(id).catch(() => {}).then(() => chatsApi.delete(id)).catch(() => {})
  }, [])
  useEffect(() => {
    liveRef.current = true
    const bye = () => cleanup()
    window.addEventListener('pagehide', bye)
    return () => { liveRef.current = false; window.removeEventListener('pagehide', bye); cleanup() }
  }, [cleanup])

  const wait = async (id) => {
    const until = Date.now() + GIVE_UP_MS
    for (;;) {
      await new Promise((r) => setTimeout(r, POLL_MS))
      if (!liveRef.current || chatRef.current !== id) return
      const chat = await chatsApi.get(id)
      const msgs = await chatsApi.messages(id)
      if (!liveRef.current) return
      setLines(tryLines(msgs.messages))
      if (!chat.running) return
      if (Date.now() > until) { setError(t('create.try.slow')); return }
    }
  }

  const send = async (e) => {
    e?.preventDefault?.()
    const msg = text.trim()
    if (!msg || busy || !request) return
    setBusy(true)
    setError('')
    try {
      if (!chatRef.current) {
        const chat = await chatsApi.create(request.model, t('create.try.chatTitle', { name: form.name || t('create.header.unnamed') }), undefined, false, request.switches)
        chatRef.current = chat.id
        if (request.settings.system_prompt) await chatsApi.update(chat.id, { settings: request.settings })
      }
      const id = chatRef.current
      setText('')
      setLines((l) => [...l, { seq: `local-${l.length}`, role: 'user', text: msg }])
      await chatsApi.send(id, msg)
      await wait(id)
    } catch (err) {
      setError(t('create.try.failed', { message: err.message }))
    } finally {
      if (liveRef.current) { setBusy(false); taRef.current?.focus() }
    }
  }

  const stop = () => { if (chatRef.current) chatsApi.stop(chatRef.current).catch(() => {}) }

  return createPortal(
    <div className="dk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div ref={sheetRef} className="dk-sheet dk-sheet--wide" role="dialog" aria-modal="true" aria-labelledby="agent-try-title" data-state="open" data-testid="agent-try" onMouseDown={(e) => e.stopPropagation()}>
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="agent-try-title">{t('create.try.title')}</h2>
            <p className="dk-sheet-desc">{t('create.try.desc')}</p>
          </div>
          <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('create.close')} onClick={onClose}><Icon name="close" /></button>
        </div>
        <div className="dk-sheet-body aed-try">
          {!request && <Callout tone="warn" testId="agent-try-nomodel">{t('create.try.noModel')}</Callout>}
          {request?.usedDefaultModel && <p className="ag-note" data-testid="agent-try-model">{t('create.try.defaultModel', { model: request.model })}</p>}
          {skipped.length > 0 && (
            <p className="ag-note" data-testid="agent-try-skipped">
              {t('create.try.skipped', { what: skipped.map((k) => t(`create.try.skip.${k}`)).join(', ') })}
            </p>
          )}
          <ol className="aed-try__log" aria-live="polite" data-testid="agent-try-log">
            {lines.length === 0 && <li className="aed-try__empty">{t('create.try.empty')}</li>}
            {lines.map((l) => (
              <li key={l.seq} className="aed-try__line" data-role={l.role}>
                <span className="aed-try__who">{t(`create.try.role.${l.role}`)}</span>
                <span className="aed-try__text">{l.role === 'tool' ? t('create.try.toolRan', { name: l.text || '' }) : l.text}</span>
              </li>
            ))}
            {busy && <li className="aed-try__empty" data-testid="agent-try-working">{t('create.try.working')}</li>}
          </ol>
          {error && <Callout tone="error" role="alert" testId="agent-try-error">{error}</Callout>}
          <form className="aed-try__form" onSubmit={send}>
            <label className="sr-only" htmlFor="agent-try-input">{t('create.try.label')}</label>
            <textarea
              id="agent-try-input" ref={taRef} className="textarea" rows={3} value={text} placeholder={t('create.try.placeholder')}
              onChange={(e) => setText(e.target.value)} disabled={!request}
              onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) send(e) }}
              data-testid="agent-try-input"
            />
            <div className="aed-try__acts">
              {busy && <button type="button" className="dk-btn dk-btn--secondary" onClick={stop} data-testid="agent-try-stop"><Icon name="stop" /> {t('create.try.stop')}</button>}
              <button type="submit" className="dk-btn dk-btn--primary" disabled={!request || busy || !text.trim()} data-testid="agent-try-send"><Icon name="send" /> {t('create.try.send')}</button>
            </div>
          </form>
        </div>
        <div className="dk-sheet-foot">
          <p className="ag-note">{t('create.try.deleted')}</p>
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('create.close')}</button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
