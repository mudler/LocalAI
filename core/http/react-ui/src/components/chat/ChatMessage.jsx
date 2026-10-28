import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown } from '../../utils/markdown'
import { renderMarkdownWithArtifacts } from '../../utils/artifacts'
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

// A turn that failed: what the server said and a Retry that sends the last
// question again.
export function ErrorCard({ message, kept, onRetry, canRetry }) {
  const { t } = useTranslation('chat')
  return (
    <div className="cx-error" role="alert" data-testid="chat-error">
      <Icon name="alert-circle" className="cx-error__icon" />
      <div className="cx-error__main">
        <h3>{t('error.title')}</h3>
        <p>{message}{kept ? ` ${t('error.kept')}` : ''}</p>
        <div className="cx-error__acts">
          <button type="button" className="cx-btn cx-btn--primary" onClick={onRetry} disabled={!canRetry}>
            {t('error.retry')}
          </button>
          <a href="/app/traces?tab=backend" className="chat-error-trace-link">
            <Icon name="waveform" /> {t('errors.viewTraces')}
          </a>
        </div>
        <details className="cx-error__detail">
          <summary>{t('error.detail')}</summary>
          <pre>{message}</pre>
        </details>
      </div>
    </div>
  )
}

// One message. The reader's messages are raised blocks on the right, the
// model's are plain prose with its name above. Actions show on hover, on focus
// and on the last message; the message itself takes focus so the arrow keys and
// C, E and R work from the keyboard. The server keeps the transcript, so a sent
// message is not changed in place: Edit puts its text back in the composer and
// Regenerate asks the question before the answer again.
function ChatMessage({
  msg, isLast, model, warm, canvasMode, busy, activityItems, actions, conversationId,
}) {
  const { t } = useTranslation('chat')
  const isUser = msg.role === 'user'
  const isAssistant = msg.role === 'assistant'
  const text = msg.text || ''
  const index = msg.seq

  const html = useMemo(() => {
    if (isUser || !text) return ''
    return canvasMode ? renderMarkdownWithArtifacts(text, index) : renderMarkdown(text)
  }, [isUser, text, canvasMode, index])

  const onKeyDown = (e) => {
    if (e.target !== e.currentTarget || e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      actions.focusRelative(index, e.key === 'ArrowDown' ? 1 : -1)
    } else if (e.key === 'c') {
      e.preventDefault()
      actions.copy(text)
    } else if (e.key === 'e' && isUser) {
      e.preventDefault()
      actions.reuse(text)
    } else if (e.key === 'r' && isAssistant && !busy) {
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
      {activityItems && <ActivityGroup items={activityItems} id={`cx-act-${index}`} />}

      {isUser ? (
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
        {isUser && (
          <button type="button" className="cx-icobtn" onClick={() => actions.reuse(text)} title={t('actions.reuse')} aria-label={t('actions.reuse')}>
            <Icon name="pencil" />
          </button>
        )}
        {isAssistant && !busy && (
          <button type="button" className="cx-icobtn" onClick={() => actions.regenerate(index)} title={t('actions.regenerate')} aria-label={t('actions.regenerate')}>
            <Icon name="refresh" />
          </button>
        )}
        {conversationId && <AddonSlot slot="chat.message.actions" context={{ conversationId, messageId: String(index), role: msg.role }} />}
      </div>
    </article>
  )
}

// Tool-less rows that sit between messages: a run of reasoning and tool entries
// that no assistant message follows yet.
export function ActivityRow({ items, id }) {
  return (
    <div className="cx-row">
      <ActivityGroup items={items} id={id} />
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
