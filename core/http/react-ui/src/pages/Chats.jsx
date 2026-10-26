/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useParams } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import { CAP_CHAT } from '../utils/capabilities'
import { useServerChat } from '../hooks/useServerChat'
import './chats.css'

// First line of a tool entry: the transcript keeps the summary and a capped output.
const firstLine = (text) => (text || '').split('\n')[0]

function Transcript({ messages, t }) {
  return messages.map((m) => {
    if (m.role === 'user') {
      return <div key={m.seq} className="ch-msg ch-msg--user">{m.text}</div>
    }
    if (m.role === 'assistant') {
      return <div key={m.seq} className="ch-msg">{m.text}</div>
    }
    if (m.role === 'reasoning') {
      return (
        <details key={m.seq} className="ch-thought">
          <summary>{t('reasoning')}{m.dur_ms ? ` ${Math.max(1, Math.round(m.dur_ms / 1000))} s` : ''}</summary>
          <div className="ch-thought__text">{m.text}</div>
        </details>
      )
    }
    if (m.role === 'tool') {
      return (
        <div key={m.seq} className="ch-tools">
          <div className="ch-tool"><span>{firstLine(m.text)}</span></div>
        </div>
      )
    }
    return null // checkpoints are for the sandbox's undo, not for reading
  })
}

function LiveTurn({ live, t }) {
  return (
    <>
      {live.reasoning && (
        <details className="ch-thought">
          <summary>{t('reasoning')}</summary>
          <div className="ch-thought__text">{live.reasoning}</div>
        </details>
      )}
      {live.tools.length > 0 && (
        <div className="ch-tools">
          {live.tools.map((tool, i) => (
            <div key={i} className={`ch-tool${tool.state === 'failed' ? ' ch-tool--failed' : ''}`}>
              <span>{tool.name}{tool.summary ? ` ${tool.summary}` : ''}</span>
              <span className="ch-tool__state">
                {tool.state === 'running' ? t('tool.running') : tool.state === 'failed' ? t('tool.failed') : ''}
              </span>
            </div>
          ))}
        </div>
      )}
      {live.text && <div className="ch-msg">{live.text}</div>}
    </>
  )
}

function PendingList({ chat, t }) {
  const [editing, setEditing] = useState(null) // { id, text }
  const { pending, running, lastTurn, activeId } = chat
  if (pending.length === 0) return null
  const waiting = !running
  const reason = lastTurn === 'stopped' ? t('pending.afterStop') : t('pending.afterFailure')

  return (
    <div className="ch-pending" data-testid="pending-list">
      <div className="ch-pending__head">
        <span>{t('pending.header', { count: pending.length })}{running ? ` · ${t('pending.note')}` : ''}</span>
        {waiting && (
          <button type="button" className="btn btn-primary btn-sm" onClick={() => chat.sendNow(activeId)}>
            {t('composer.sendNow')}
          </button>
        )}
      </div>
      {waiting && lastTurn !== 'done' && lastTurn !== '' && <div className="ch-note">{reason}</div>}
      {pending.map((p) => (
        <div key={p.id} className="ch-pending__row" data-testid="pending-row">
          {editing?.id === p.id ? (
            <>
              <input
                className="input ch-pending__edit"
                aria-label={t('pending.edit')}
                value={editing.text}
                onChange={(e) => setEditing({ id: p.id, text: e.target.value })}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { chat.editPending(activeId, p.id, editing.text); setEditing(null) }
                  if (e.key === 'Escape') setEditing(null)
                }}
                autoFocus
              />
              <span className="ch-pending__actions">
                <button type="button" className="ch-link" onClick={() => { chat.editPending(activeId, p.id, editing.text); setEditing(null) }}>{t('pending.save')}</button>
                <button type="button" className="ch-link" onClick={() => setEditing(null)}>{t('pending.discard')}</button>
              </span>
            </>
          ) : (
            <>
              <span className="ch-pending__text">{p.text}</span>
              <span className="ch-pending__actions">
                <button type="button" className="ch-link" onClick={() => setEditing({ id: p.id, text: p.text })}>{t('pending.edit')}</button>
                <button type="button" className="ch-link ch-link--danger" onClick={() => chat.cancelPending(activeId, p.id)}>{t('pending.cancel')}</button>
              </span>
            </>
          )}
        </div>
      ))}
    </div>
  )
}

export default function Chats() {
  const { t } = useTranslation('chats')
  const { id: routeId } = useParams()
  const navigate = useNavigate()
  const chat = useServerChat()
  const { select, activeId } = chat
  const [model, setModel] = useState('')
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [localError, setLocalError] = useState('')

  // The address names the open chat. Opening one by id selects it, unless it is
  // already open (creating a chat selects it before the address changes).
  useEffect(() => {
    const want = routeId || null
    if (want !== activeId) select(want)
  // activeId is read, not a trigger: only a change of address selects.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [routeId])

  const submit = useCallback(async () => {
    const text = draft.trim()
    // Only the message that starts a chat must go once: it creates the chat. In
    // a chat that exists, every message goes, even while the last one is still
    // on its way, and the hook keeps them in order.
    if (!text || (!activeId && busy)) return
    setLocalError('')
    try {
      let target = activeId
      if (!target) {
        if (!model) return
        setBusy(true)
        target = await chat.createChat(model)
        await select(target)
        navigate(`/app/chats/${target}`)
      }
      setDraft('')
      await chat.send(target, text)
    } catch (e) {
      // The text was cleared to make room for the next message: give it back,
      // unless the user has already typed something else.
      setDraft(d => d || text)
      setLocalError(e.message || t('errors.send'))
    } finally {
      setBusy(false)
    }
  }, [draft, busy, activeId, model, chat, select, navigate, t])

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    }
  }

  const shownError = localError || chat.error
  const dismissError = () => { setLocalError(''); chat.clearError() }
  const activeChat = chat.chats.find((c) => c.id === activeId)
  const title = activeChat?.title || t('list.untitled')

  return (
    <div className="ch-page" data-testid="chats-page">
      <aside className="ch-list" aria-label={t('list.label')}>
        <button type="button" className="btn btn-primary btn-full" onClick={() => navigate('/app/chats')}>
          {t('list.new')}
        </button>
        <div className="ch-list__items">
          {chat.chats.length === 0 && <div className="ch-list__empty">{t('list.empty')}</div>}
          {chat.chats.map((c) => (
            <div key={c.id} className="ch-list__item" aria-current={c.id === activeId ? 'true' : undefined}>
              <button type="button" className="ch-list__open" onClick={() => navigate(`/app/chats/${c.id}`)}>
                <span className="ch-list__title">{c.title || t('list.untitled')}</span>
                {c.running && <span className="ch-dot" role="img" aria-label={t('list.working')} />}
              </button>
              <button type="button" className="ch-link ch-link--danger" aria-label={t('list.delete')} onClick={() => chat.remove(c.id)}>×</button>
            </div>
          ))}
        </div>
      </aside>

      <section className="ch-main">
        <header className="ch-head">
          <h1 className="ch-head__title">{activeId ? title : t('title')}</h1>
          {activeChat?.model && <span className="ch-head__meta">{activeChat.model}</span>}
        </header>

        <div className="ch-scroll">
          <div className="ch-flow" role="log" aria-live="polite" aria-label={t('transcript')}>
            {!activeId && (
              <div className="ch-empty">
                <h2 className="ch-empty__title">{t('empty.title')}</h2>
                <p className="ch-empty__body">{t('empty.body')}</p>
                <label className="ch-empty__body" htmlFor="chats-model">{t('model')}</label>
                <ModelSelector value={model} onChange={setModel} capability={CAP_CHAT} />
              </div>
            )}
            {activeId && <Transcript messages={chat.messages} t={t} />}
            {activeId && <LiveTurn live={chat.live} t={t} />}
            {chat.running && <div className="ch-status">{t('status.working')}</div>}
            {activeId && !chat.running && chat.lastTurn === 'failed' && <div className="ch-status">{t('status.failed')}</div>}
            {activeId && !chat.running && chat.lastTurn === 'interrupted' && <div className="ch-status">{t('status.interrupted')}</div>}
            {chat.notes.map((n, i) => (
              <div key={i} className="ch-note">
                {n.kind === 'compacted' ? t('notes.compacted', { before: n.before, after: n.after }) : t('notes.subAgentDone', { title: n.title })}
              </div>
            ))}
          </div>
        </div>

        <div className="ch-composer-wrap">
          {!chat.persistent && (
            <div className="ch-notice" role="note">{t('memoryNotice')}</div>
          )}
          {shownError && (
            <div className="ch-error" role="alert">
              <span>{shownError}</span>
              <button type="button" className="ch-link" onClick={dismissError}>{t('errors.dismiss')}</button>
            </div>
          )}
          <div className="ch-composer">
            {activeId && <PendingList chat={chat} t={t} />}
            <textarea
              className="ch-input"
              aria-label={t('composer.label')}
              placeholder={t('composer.placeholder')}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={onKeyDown}
              rows={2}
            />
            <div className="ch-actions">
              <span className="ch-head__meta">{!activeId && model ? model : ''}</span>
              <span className="ch-actions__right">
                {chat.running && (
                  <button type="button" className="btn btn-secondary" onClick={() => chat.stop(activeId)}>
                    {t('composer.stop')}
                  </button>
                )}
                <button type="button" className="btn btn-primary" disabled={(!activeId && busy) || !draft.trim() || (!activeId && !model)} onClick={submit}>
                  {t('composer.send')}
                </button>
              </span>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}
