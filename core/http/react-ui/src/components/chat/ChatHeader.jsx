import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../ActionMenu'
import Icon from '../Icon'

// The slim bar over the thread: the conversations list (Ctrl K), the chat's
// name (click to rename), the inbox (extra), find,
// settings and the rest.
export default function ChatHeader({
  historyMenu, manageMode, name, onRename, renaming, setRenaming,
  onFind, findOpen, onSettings, settingsOpen, moreItems, extra, canRename = true,
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
          <button type="button" className="cx-ttl__btn" data-testid="chat-title" title={name} disabled={!canRename} onClick={() => setRenaming(true)}>
            {name}
          </button>
        )}
      </div>

      <div className="cx-hdr">
        {extra}
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
