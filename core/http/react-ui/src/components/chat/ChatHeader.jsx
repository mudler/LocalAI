import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { fillStyle } from '../home/memory'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../ActionMenu'
import Icon from '../Icon'

// The slim bar over the thread: the conversations list (Ctrl K), the chat's
// name (click to rename), how full the context is, find, settings and the rest.
export default function ChatHeader({
  historyMenu, manageMode, name, onRename, renaming, setRenaming,
  contextPercent, contextTokens, contextSize,
  onFind, findOpen, onSettings, settingsOpen, moreItems,
}) {
  const { t } = useTranslation('chat')
  const inputRef = useRef(null)
  const cancelledRef = useRef(false)

  // The page owns `renaming`, so the More menu can open the box too.
  useEffect(() => {
    if (!renaming) return
    cancelledRef.current = false
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [renaming])

  // Esc cancels; the box also loses focus as it goes away, and that blur must
  // not save what was typed.
  const finish = (save) => {
    if (!save) cancelledRef.current = true
    else if (cancelledRef.current) { cancelledRef.current = false; return }
    const next = inputRef.current?.value.trim()
    if (save && next && next !== name) onRename(next)
    setRenaming(false)
  }

  const warn = contextPercent !== null && contextPercent > 70
  const hot = contextPercent !== null && contextPercent > 90

  return (
    <header className="cx-hd" data-testid="chat-header">
      {historyMenu}
      {manageMode && (
        <span className="cx-shield" title={t('header.manageModeTooltip')} data-testid="chat-manage-badge">
          <Icon name="user-shield" />
          <span className="dk-sr-only">{t('header.manageModeTooltip')}</span>
        </span>
      )}
      <div className="cx-ttl">
        {renaming ? (
          <input
            ref={inputRef}
            className="cx-ttl__input"
            data-testid="chat-title-input"
            defaultValue={name}
            onBlur={() => finish(true)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); finish(true) }
              if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(false) }
            }}
            aria-label={t('header.chatName')}
          />
        ) : (
          <button type="button" className="cx-ttl__btn" data-testid="chat-title" title={name} onClick={() => setRenaming(true)}>
            {name}
          </button>
        )}
      </div>

      {contextPercent !== null && (
        <div
          className="cx-ctx"
          data-warn={warn || undefined}
          data-hot={hot || undefined}
          title={contextTokens > 0
            ? t('context.labelWithTokens', { percent: Math.round(contextPercent), tokens: contextTokens })
            : t('context.label', { percent: Math.round(contextPercent) })}
          data-testid="chat-context"
        >
          <span className="cx-ctx__label">{t('context.title')}</span>
          <span
            className="cx-ctx__bar"
            role="meter"
            aria-label={t('context.title')}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(contextPercent)}
            aria-valuetext={contextSize ? t('context.ofSize', { percent: Math.round(contextPercent), size: contextSize }) : undefined}
          >
            <i style={fillStyle(contextPercent)} />
          </span>
          <span className="cx-ctx__pct">{Math.round(contextPercent)}%</span>
        </div>
      )}

      <div className="cx-hdr">
        {onFind && (
          <button
            type="button"
            className="cx-icobtn cx-icobtn--lg"
            onClick={onFind}
            aria-pressed={findOpen}
            title={t('header.find')}
            aria-label={t('header.find')}
            data-testid="chat-find-button"
          >
            <Icon name="search" />
          </button>
        )}
        <button
          type="button"
          className="cx-icobtn cx-icobtn--lg"
          onClick={onSettings}
          aria-pressed={settingsOpen}
          title={t('header.chatSettings')}
          aria-label={t('header.chatSettings')}
          data-testid="chat-settings-button"
        >
          <Icon name="sliders" />
        </button>
        <ActionMenu items={moreItems} ariaLabel={t('header.more')} />
      </div>
    </header>
  )
}
