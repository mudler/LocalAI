import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown } from '../../utils/markdown'
import { renderMarkdownWithArtifacts } from '../../utils/artifacts'
import { editableMessageText, escapeHtml, messageText, splitError } from './chatText'
// eslint-disable-next-line no-unused-vars
import { ActivityGroup, StreamingActivity } from './ActivityFold'
import Icon from '../Icon'

// Assistant prose. The HTML string is memoised, so React leaves the node alone
// while the page re-renders for other reasons (polling, streaming elsewhere):
// a text selection inside an answer survives.
// eslint-disable-next-line no-unused-vars
function Prose({ html, className = '' }) {
  return <div className={`cx-prose ${className}`} dangerouslySetInnerHTML={{ __html: html }} />
}

function fileIcon(f) {
  return f.type === 'image' ? 'image' : f.type === 'audio' ? 'headphones' : f.type === 'video' ? 'video' : 'file'
}

// eslint-disable-next-line no-unused-vars
function UserBody({ content, files, onOpenImage }) {
  const { t } = useTranslation('chat')
  const text = messageText(content)
  const images = Array.isArray(content) ? content.filter(c => c.type === 'image_url') : []
  const videos = Array.isArray(content) ? content.filter(c => c.type === 'video_url') : []
  // An image that is in the message shows as a thumbnail; its entry in the file
  // list would only repeat it.
  const chips = (files || []).filter(f => !(f.type === 'image' && images.length > 0))
  return (
    <>
      {(images.length > 0 || videos.length > 0 || chips.length > 0) && (
        <div className="cx-atts">
          {images.map((img, i) => (
            <button
              key={`i${i}`}
              type="button"
              className="cx-thumb"
              onClick={() => onOpenImage?.(images.map((x, n) => ({ url: x.image_url.url, alt: t('message.attachedImage', { n: n + 1 }) })), i)}
              aria-label={t('message.openImage', { n: i + 1 })}
            >
              <img src={img.image_url.url} alt="" />
            </button>
          ))}
          {videos.map((vid, i) => (
            <video key={`v${i}`} src={vid.video_url.url} controls className="cx-video" />
          ))}
          {chips.map((f, i) => (
            <span key={`f${i}`} className="cx-chip">
              <Icon name={fileIcon(f)} />
              <span className="cx-chip__name">{f.name}</span>
            </span>
          ))}
        </div>
      )}
      {text && <div className="cx-text" dangerouslySetInnerHTML={{ __html: escapeHtml(text) }} />}
    </>
  )
}

// eslint-disable-next-line no-unused-vars
function ErrorCard({ message, kept, onRetry, canRetry }) {
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

// One turn. The reader's messages are raised blocks on the right, the model's
// are plain prose with its name above. Actions show on hover, on focus and on
// the last turn; the turn itself takes focus so the arrow keys and C, E, R and
// B work from the keyboard.
function ChatMessage({
  msg, index, isLast, model, warm, canvasMode, busy,
  editing, draft, onDraft, activityItems, actions, onOpenImage, getClientForTool,
}) {
  const { t } = useTranslation('chat')
  const isUser = msg.role === 'user'
  const isAssistant = msg.role === 'assistant'
  const text = typeof msg.content === 'string' ? msg.content : ''
  const failure = isAssistant ? splitError(text) : null
  const shown = failure ? failure.text : text

  const html = useMemo(() => {
    if (isUser || !shown) return ''
    return canvasMode ? renderMarkdownWithArtifacts(shown, index) : renderMarkdown(shown)
  }, [isUser, shown, canvasMode, index])

  const canEdit = (isUser || isAssistant) && !busy && editableMessageText(msg) !== null

  const onKeyDown = (e) => {
    if (e.target !== e.currentTarget || e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      actions.focusRelative(index, e.key === 'ArrowDown' ? 1 : -1)
    } else if (e.key === 'c') actions.copy(msg.content)
    else if (e.key === 'e' && canEdit) actions.startEdit(index, msg)
    else if (e.key === 'r' && isAssistant && !busy) actions.regenerate(index)
    else if (e.key === 'b' && isAssistant && !busy) actions.branch(index)
  }

  return (
    <article
      className={`cx-msg cx-msg--${isUser ? 'user' : 'model'}`}
      data-testid="chat-message"
      data-role={msg.role}
      data-index={index}
      data-last={isLast || undefined}
      data-editing={editing || undefined}
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
      {!isUser && !isAssistant && <div className="cx-who"><b>{t('message.system')}</b></div>}
      {activityItems && (
        <ActivityGroup items={activityItems} getClientForTool={getClientForTool} id={`cx-act-${index}`} />
      )}

      {editing ? (
        <div className="cx-editor">
          <textarea
            autoFocus
            className="cx-editor__input"
            value={draft}
            rows={Math.min(10, Math.max(3, draft.split('\n').length + 1))}
            onChange={(e) => onDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') { e.stopPropagation(); actions.cancelEdit() }
            }}
            aria-label={t('actions.editMessage')}
          />
          <div className="cx-editor__row">
            <button type="button" className="cx-btn cx-btn--ghost" onClick={actions.cancelEdit}>
              {t('actions.cancel')} <kbd className="dk-kbd">esc</kbd>
            </button>
            <button type="button" className="cx-btn cx-btn--primary" onClick={actions.saveEdit} disabled={!draft.trim()}>
              {t('actions.save')}
            </button>
          </div>
        </div>
      ) : isUser ? (
        <div className="cx-bubble" data-testid="message-content">
          <UserBody content={msg.content} files={msg.files} onOpenImage={onOpenImage} />
        </div>
      ) : (
        <>
          {shown && <Prose html={html} className={msg.role === 'system' ? 'cx-prose--system' : ''} />}
          {failure && (
            <ErrorCard
              message={failure.message}
              kept={!!failure.text}
              canRetry={!busy}
              onRetry={() => actions.regenerate(index)}
            />
          )}
        </>
      )}

      {!editing && (
        <div className="cx-acts" data-testid="message-actions">
          <button type="button" className="cx-icobtn" onClick={() => actions.copy(msg.content)} title={t('actions.copy')} aria-label={t('actions.copy')}>
            <Icon name="copy" />
          </button>
          {canEdit && (
            <button type="button" className="cx-icobtn" onClick={() => actions.startEdit(index, msg)} title={t('actions.edit')} aria-label={t('actions.edit')}>
              <Icon name="pencil" />
            </button>
          )}
          {isAssistant && !busy && (
            <button type="button" className="cx-icobtn" onClick={() => actions.regenerate(index)} title={t('actions.regenerate')} aria-label={t('actions.regenerate')}>
              <Icon name="refresh" />
            </button>
          )}
          {isAssistant && !busy && (
            <button type="button" className="cx-icobtn" onClick={() => actions.branch(index)} title={t('actions.branch')} aria-label={t('actions.branch')}>
              <Icon name="git-branch" />
            </button>
          )}
        </div>
      )}
    </article>
  )
}

// Tool-less rows that sit between messages: a run of reasoning and tool entries
// that no assistant message follows yet.
export function ActivityRow({ items, getClientForTool, id }) {
  return (
    <div className="cx-row">
      <ActivityGroup items={items} getClientForTool={getClientForTool} id={id} />
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
