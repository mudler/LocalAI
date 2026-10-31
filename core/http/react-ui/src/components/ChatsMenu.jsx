import { useState, useEffect, useRef, useCallback, useImperativeHandle, forwardRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import ChatsList from './ChatsList'
import Icon from './Icon'

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || '')

// The chat history of a transcript, opened on Ctrl or Cmd K: the list of
// ChatsList in a dialog. The All chats page shows the same list on a page.
//
// The page owns what the actions do. `onDelete` is only a request: the page
// hides the row and offers an undo before anything is removed.
const ChatsMenu = forwardRef(function ChatsMenu({
  chats,
  activeChatId,
  onSelect,
  onNew,
  onDelete,
  onDeleteAll,
  onRename,
  onExport,
  onCopyChat,
  onSearch,
}, ref) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const dialogRef = useRef(null)
  const triggerRef = useRef(null)
  const returnFocusRef = useRef(null)

  const close = useCallback(() => setOpen(false), [])
  const openMenu = useCallback(() => {
    returnFocusRef.current = document.activeElement
    setOpen(true)
  }, [])

  useImperativeHandle(ref, () => ({
    open: openMenu,
    close,
    toggle: () => setOpen(prev => {
      if (!prev) returnFocusRef.current = document.activeElement
      return !prev
    }),
  }), [openMenu, close])

  // Focus goes back to where it came from when the dialog closes.
  useEffect(() => {
    if (open) return
    const el = returnFocusRef.current
    if (el && document.contains(el)) el.focus?.()
    returnFocusRef.current = null
  }, [open])

  // Tab stays inside the dialog.
  const onKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      close()
      return
    }
    if (e.key !== 'Tab') return
    const focusable = Array.from(dialogRef.current?.querySelectorAll('button, input, [href]') || []).filter(el => !el.disabled && el.offsetParent !== null)
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
  }

  return (
    <div className="chats-menu">
      <button
        ref={triggerRef}
        type="button"
        className={`cx-hist${open ? ' cx-hist--open' : ''}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={t('menu.triggerTitle')}
        data-testid="chats-trigger"
        onClick={() => (open ? close() : openMenu())}
      >
        <Icon name="history" />
        <span className="cx-hist__label">{t('menu.trigger')}</span>
        <kbd className="dk-kbd cx-hist__kbd">{isMac() ? '⌘K' : 'Ctrl K'}</kbd>
      </button>

      {open && createPortal(
        <>
          <div className="cx-veil" onMouseDown={close} />
          <div
            ref={dialogRef}
            className="cx-menu"
            role="dialog"
            aria-modal="true"
            aria-label={t('menu.title')}
            data-testid="chats-menu"
            onKeyDown={onKeyDown}
          >
            <ChatsList
              chats={chats}
              activeChatId={activeChatId}
              onSelect={(id) => { onSelect?.(id); close() }}
              onNew={() => { onNew?.(); close() }}
              onDelete={onDelete}
              onDeleteAll={onDeleteAll ? () => { onDeleteAll(); close() } : undefined}
              onRename={onRename}
              onExport={onExport}
              onCopyChat={onCopyChat}
              onSearch={onSearch}
              onEscape={close}
              autoFocus
            />
          </div>
        </>,
        document.body,
      )}
    </div>
  )
})

export default ChatsMenu
