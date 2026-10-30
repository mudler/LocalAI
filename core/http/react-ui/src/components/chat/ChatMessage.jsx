import { memo, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { failureView } from '../../utils/chatFailure'
import { renderMarkdown } from '../../utils/markdown'
import { escapeHtml } from './chatText'
// eslint-disable-next-line no-unused-vars
import { ActivityGroup, StreamingActivity } from './ActivityFold'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import AddonSlot from '../../addons/AddonSlot'

// Assistant prose. The HTML string is memoised, so React leaves the node alone
// while the page re-renders for other reasons (polling, streaming elsewhere):
// a text selection inside an answer survives.
// eslint-disable-next-line no-unused-vars
function Prose({ html, className = '' }) {
  return <div className={`cx-prose ${className}`} dangerouslySetInnerHTML={{ __html: html }} />
}

// A turn that failed: the reason in plain words, what to do about it, a Retry
// that sends the last question again and, apart, the cleaned technical detail.
// failure says why (see utils/chatFailure.js); without it the card still shows
// that the turn failed and offers the Retry.
export function ErrorCard({ failure, isAdmin, kept, onRetry, canRetry }) {
  const { t } = useTranslation('chat')
  const view = failureView(t, failure, { isAdmin })
  // The detail is shown only when it says more than the reason does.
  const detail = view.detail && view.detail !== view.reason ? view.detail : ''
  return (
    <div className="cx-error" role="alert" data-testid="chat-error" data-code={failure?.code || undefined}>
      <Icon name="alert-circle" className="cx-error__icon" />
      <div className="cx-error__main">
        <h3>{t('error.title')}</h3>
        <p data-testid="chat-error-reason">{view.reason}{kept ? ` ${t('error.kept')}` : ''}</p>
        {view.hint && <p className="text-note" data-testid="chat-error-hint">{view.hint}</p>}
        <div className="cx-error__acts">
          <button type="button" className="cx-btn cx-btn--primary" onClick={onRetry} disabled={!canRetry}>
            {t('error.retry')}
          </button>
          {view.backendsLink && (
            <Link to="/app/backends" className="cx-btn" data-testid="chat-error-backends">
              {t('error.openBackends')}
            </Link>
          )}
          <a href="/app/traces?tab=backend" className="chat-error-trace-link">
            <Icon name="waveform" /> {t('errors.viewTraces')}
          </a>
        </div>
        {detail && (
          <details className="cx-error__detail">
            <summary>{t('error.detail')}</summary>
            <pre data-testid="chat-error-detail">{detail}</pre>
          </details>
        )}
      </div>
    </div>
  )
}

// One message. The reader's messages are raised blocks on the right, the
// model's are plain prose with its name above. Actions show on hover, on focus
// and on the last message; the message itself takes focus so the arrow keys and
// C, E, B and R work from the keyboard. The server keeps the transcript: Edit
// replaces your message and runs the chat again from it, Regenerate answers the
// last message again, and Branch copies the chat up to the message into a new one.
function ChatMessage({
  msg, isLast, model, warm, busy, activityItems, actions, conversationId, canRegenerate, proposalTick,
}) {
  const { t } = useTranslation('chat')
  const isUser = msg.role === 'user'
  const isAssistant = msg.role === 'assistant'
  const text = msg.text || ''
  const index = msg.seq
  // The draft of an edit of your message, or null.
  const [draft, setDraft] = useState(null)
  const saveEdit = () => {
    const next = (draft || '').trim()
    setDraft(null)
    if (next && next !== text.trim()) actions.edit(index, next)
  }

  const html = useMemo(() => {
    if (isUser || !text) return ''
    return renderMarkdown(text)
  }, [isUser, text])

  const onKeyDown = (e) => {
    if (e.target !== e.currentTarget || e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      actions.focusRelative(index, e.key === 'ArrowDown' ? 1 : -1)
    } else if (e.key === 'c') {
      e.preventDefault()
      actions.copy(text)
    } else if (e.key === 'e' && isUser && !busy) {
      e.preventDefault()
      setDraft(text)
    } else if (e.key === 'b' && !busy) {
      e.preventDefault()
      actions.fork(index)
    } else if (e.key === 'r' && canRegenerate) {
      e.preventDefault()
      actions.regenerate(index)
    }
  }

  return (
    <article
      className={`cx-msg cx-msg--${isUser ? 'user' : 'model'}`}
      data-testid="chat-message"
      data-role={msg.role}
      data-index={index}
      data-last={isLast || undefined}
      tabIndex={0}
      onKeyDown={onKeyDown}
      aria-label={isUser ? t('message.you') : (model || t('message.assistant'))}
    >
      {isAssistant && (
        <div className="cx-who">
          <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />
          {model && <b>{model}</b>}
        </div>
      )}
      {activityItems && <ActivityGroup items={activityItems} id={`cx-act-${index}`} proposalTick={proposalTick} />}

      {isUser && draft !== null ? (
        <div className="cx-bubble cx-bubble--edit" data-testid="message-edit">
          <textarea
            className="cx-ta"
            aria-label={t('actions.editMessage')}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              e.stopPropagation()
              if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); saveEdit() }
              if (e.key === 'Escape') { e.preventDefault(); setDraft(null) }
            }}
            autoFocus
          />
          <p className="cx-help">{t('actions.editHelp')}</p>
          <span className="cx-pending__acts">
            <button type="button" className="cx-btn cx-btn--primary" onClick={saveEdit} disabled={!draft.trim()}>{t('actions.editSend')}</button>
            <button type="button" className="cx-btn" onClick={() => setDraft(null)}>{t('actions.editCancel')}</button>
          </span>
        </div>
      ) : isUser ? (
        <div className="cx-bubble" data-testid="message-content">
          {text && <div className="cx-text" dangerouslySetInnerHTML={{ __html: escapeHtml(text) }} />}
        </div>
      ) : (
        text && <Prose html={html} />
      )}

      <div className="cx-acts" data-testid="message-actions">
        <button type="button" className="cx-icobtn" onClick={() => actions.copy(text)} title={t('actions.copy')} aria-label={t('actions.copy')}>
          <Icon name="copy" />
        </button>
        {isUser && !busy && draft === null && (
          <button type="button" className="cx-icobtn" onClick={() => setDraft(text)} title={t('actions.editMessage')} aria-label={t('actions.editMessage')}>
            <Icon name="pencil" />
          </button>
        )}
        {canRegenerate && (
          <button type="button" className="cx-icobtn" onClick={() => actions.regenerate(index)} title={t('actions.regenerate')} aria-label={t('actions.regenerate')}>
            <Icon name="refresh" />
          </button>
        )}
        {!busy && (
          <button type="button" className="cx-icobtn" onClick={() => actions.fork(index)} title={t('actions.branch')} aria-label={t('actions.branch')}>
            <Icon name="git-branch" />
          </button>
        )}
        {conversationId && <AddonSlot slot="chat.message.actions" context={{ conversationId, messageId: String(index), role: msg.role }} />}
      </div>
    </article>
  )
}

// Tool-less rows that sit between messages: a run of reasoning and tool entries
// that no assistant message follows yet.
export function ActivityRow({ items, id, proposalTick }) {
  return (
    <div className="cx-row">
      <ActivityGroup items={items} id={id} proposalTick={proposalTick} />
    </div>
  )
}

// The reply while it is still coming: the activity line, the prose so far with a
// caret at its end, and, before anything has arrived, whatever the page says
// the model is waiting for (a load card, or quiet dots).
export function StreamingTurn({ model, warm, content, reasoning, toolCalls, waiting }) {
  const html = useMemo(() => (content ? renderMarkdown(content) : ''), [content])
  const idle = !content && !reasoning && toolCalls.length === 0
  return (
    <article className="cx-msg cx-msg--model" data-testid="chat-streaming" aria-busy="true">
      <div className="cx-who">
        <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />
        {model && <b>{model}</b>}
      </div>
      <StreamingActivity reasoning={reasoning} toolCalls={toolCalls} hasResponse={!!content} />
      {content && <Prose html={html} className="cx-prose--live" />}
      {idle && waiting}
    </article>
  )
}

export default memo(ChatMessage)
