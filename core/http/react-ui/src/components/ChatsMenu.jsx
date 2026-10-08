import { useState, useEffect, useRef, useCallback, useImperativeHandle, forwardRef, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { groupConversations } from '../utils/homeConversations'
import { messageText } from './chat/chatText'
import Icon from './Icon'

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || '')

function oneLine(text, max = 120) {
  const flat = String(text || '').replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

// The line under a conversation's name: the last thing said, or, while you
// search, the first message that holds what you typed.
function previewOf(chat, query) {
  const history = chat.history || []
  if (query) {
    const hit = history.find(m => messageText(m.content).toLowerCase().includes(query))
    if (hit) {
      const text = messageText(hit.content)
      const at = Math.max(0, text.toLowerCase().indexOf(query) - 30)
      return oneLine(text.slice(at))
    }
  }
  for (let i = history.length - 1; i >= 0; i--) {
    const m = history[i]
    if (m.role === 'user' || m.role === 'assistant') return oneLine(messageText(m.content))
  }
  return ''
}

function matches(chat, query) {
  if ((chat.name || '').toLowerCase().includes(query)) return true
  return (chat.history || []).some(m => messageText(m.content).toLowerCase().includes(query))
}

// The conversations list, opened on Ctrl or Cmd K. It reads like the Home
// "Jump back in" list: one block per day, the model that answered, and the time.
// There is no permanent history pane; this is the way to move between chats.
//
// The page owns what the actions do. `onDelete` is only a request: the page
// hides the row and offers an undo before anything is removed.
const ChatsMenu = forwardRef(function ChatsMenu({
  chats,
  activeChatId,
  streamingChatId,
  onSelect,
  onNew,
  onDelete,
  onDeleteAll,
  onRename,
  onExport,
  onCopyChat,
  onDuplicate,
}, ref) {
  const { t, i18n } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [editingId, setEditingId] = useState(null)
  const [editName, setEditName] = useState('')
  const [activeIdx, setActiveIdx] = useState(0)
  const dialogRef = useRef(null)
  const searchRef = useRef(null)
  const triggerRef = useRef(null)
  const listRef = useRef(null)
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

  const query = search.trim().toLowerCase()

  // Chats that hold a message, and the one you are in. An empty placeholder is
  // not worth a row. Newest first, which is what the day blocks need.
  const items = useMemo(() => {
    const kept = chats.filter(c => (c.history?.length || 0) > 0 || c.id === activeChatId)
    const found = query ? kept.filter(c => matches(c, query)) : kept
    return [...found].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0))
  }, [chats, activeChatId, query])

  const groups = useMemo(() => groupConversations(items), [items])
  const flat = useMemo(() => groups.flatMap(g => g.items), [groups])

  const fmtTime = useMemo(() => new Intl.DateTimeFormat(i18n.language, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }), [i18n.language])
  const fmtDay = useMemo(() => new Intl.DateTimeFormat(i18n.language, { weekday: 'short' }), [i18n.language])
  const fmtDate = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: 'numeric', month: 'short' }), [i18n.language])
  const when = (group, ts) => (group === 'older' ? fmtDate.format(ts) : group === 'week' ? fmtDay.format(ts) : fmtTime.format(ts))

  // On open: start on the current chat and put the cursor in the search box.
  // On close: forget the search and give focus back to where it came from.
  useEffect(() => {
    if (!open) {
      setSearch('')
      setEditingId(null)
      const el = returnFocusRef.current
      if (el && document.contains(el)) el.focus?.()
      returnFocusRef.current = null
      return undefined
    }
    const at = items.findIndex(c => c.id === activeChatId)
    setActiveIdx(at < 0 ? 0 : at)
    const timer = setTimeout(() => searchRef.current?.focus(), 20)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  useEffect(() => { setActiveIdx(0) }, [query])

  useEffect(() => {
    if (!open) return
    listRef.current?.querySelector(`[data-idx="${activeIdx}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [activeIdx, open])

  // Keep the cursor inside the list when a row goes away.
  useEffect(() => {
    if (activeIdx > flat.length - 1) setActiveIdx(Math.max(0, flat.length - 1))
  }, [flat.length, activeIdx])

  const choose = useCallback((id) => { onSelect?.(id); close() }, [onSelect, close])

  const cancelledRef = useRef(false)
  const startRename = (chat) => { cancelledRef.current = false; setEditingId(chat.id); setEditName(chat.name || '') }
  // Esc cancels. The box also loses focus as it goes away, which would save
  // it, so Esc leaves a note for that blur to read.
  const cancelRename = () => { cancelledRef.current = true; setEditingId(null); searchRef.current?.focus() }
  const finishRename = () => {
    if (cancelledRef.current) { cancelledRef.current = false; return }
    if (editingId && editName.trim()) onRename?.(editingId, editName.trim())
    setEditingId(null)
    searchRef.current?.focus()
  }

  const canDelete = chats.length > 1
  const current = flat[activeIdx]

  const onKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      if (editingId) cancelRename(); else close()
      return
    }
    if (e.key === 'Tab') {
      const focusable = Array.from(dialogRef.current?.querySelectorAll('button, input, [href]') || []).filter(el => !el.disabled && el.offsetParent !== null)
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
      return
    }
    if (editingId) return
    if (e.key === 'ArrowDown') { e.preventDefault(); setActiveIdx(i => Math.min(i + 1, flat.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActiveIdx(i => Math.max(i - 1, 0)) }
    else if (e.key === 'Enter' && e.target === searchRef.current && current) { e.preventDefault(); choose(current.id) }
    else if (e.key === 'F2' && current) { e.preventDefault(); startRename(current) }
    else if (e.key === 'Delete' && current && canDelete && (e.target !== searchRef.current || !search)) {
      e.preventDefault()
      onDelete?.(current)
    }
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
            <div className="cx-menu__search">
              <Icon name="search" />
              <input
                ref={searchRef}
                type="text"
                role="combobox"
                aria-expanded="true"
                aria-controls="chats-menu-list"
                aria-activedescendant={current ? `chats-opt-${current.id}` : undefined}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t('menu.search')}
                aria-label={t('menu.search')}
              />
              {search && (
                <button type="button" className="cx-icobtn" onClick={() => { setSearch(''); searchRef.current?.focus() }} aria-label={t('menu.clearSearch')}>
                  <Icon name="close" />
                </button>
              )}
              <button type="button" className="cx-btn" onClick={() => { onNew?.(); close() }} data-testid="chats-new">
                <Icon name="plus" /> {t('menu.newChat')}
              </button>
            </div>

            <div className="cx-menu__list" ref={listRef} id="chats-menu-list" role="listbox" aria-label={t('menu.title')}>
              {flat.length === 0 && (
                <div className="cx-menu__empty">{search ? t('menu.noMatch') : t('menu.noConversations')}</div>
              )}
              {(() => {
                let n = 0
                return groups.map(g => (
                  <div key={g.key} className="cx-menu__group" data-testid={`chats-day-${g.key}`}>
                    <h3 className="cx-menu__day">{t(`menu.group.${g.key}`)}</h3>
                    {g.items.map(chat => {
                      const idx = n++
                      const selected = idx === activeIdx
                      const editing = editingId === chat.id
                      const preview = previewOf(chat, query) || t('empty.noMessages')
                      return (
                        <div
                          key={chat.id}
                          id={`chats-opt-${chat.id}`}
                          role="option"
                          aria-selected={selected}
                          data-idx={idx}
                          data-current={chat.id === activeChatId || undefined}
                          data-testid="chats-row"
                          className="cx-row-item"
                          onClick={() => !editing && choose(chat.id)}
                          onMouseMove={() => { if (!selected) setActiveIdx(idx) }}
                        >
                          <div className="cx-row-item__title">
                            {streamingChatId === chat.id && <Icon name="spinner" spin />}
                            {editing ? (
                              <input
                                className="cx-row-item__rename"
                                value={editName}
                                onChange={(e) => setEditName(e.target.value)}
                                onBlur={finishRename}
                                onKeyDown={(e) => {
                                  e.stopPropagation()
                                  if (e.key === 'Enter') { e.preventDefault(); finishRename() }
                                  if (e.key === 'Escape') { e.preventDefault(); cancelRename() }
                                }}
                                onClick={(e) => e.stopPropagation()}
                                aria-label={t('menu.rename')}
                                autoFocus
                              />
                            ) : (
                              <span
                                className="cx-row-item__name"
                                onDoubleClick={(e) => { e.stopPropagation(); startRename(chat) }}
                              >
                                {chat.name}
                              </span>
                            )}
                          </div>
                          <div className="cx-row-item__preview">{preview}</div>
                          <span className="cx-row-item__model">{chat.model || t('menu.noModel')}</span>
                          <span className="cx-row-item__time">{when(g.key, chat.updatedAt || chat.createdAt || 0)}</span>
                          <div className="cx-row-item__acts">
                            <button type="button" className="cx-icobtn" onClick={(e) => { e.stopPropagation(); startRename(chat) }} title={t('menu.rename')} aria-label={t('menu.rename')}>
                              <Icon name="pencil" />
                            </button>
                            {onDuplicate && (
                              <button type="button" className="cx-icobtn" onClick={(e) => { e.stopPropagation(); onDuplicate(chat); close() }} title={t('menu.duplicate')} aria-label={t('menu.duplicate')}>
                                <Icon name="copy" />
                              </button>
                            )}
                            {(chat.history?.length || 0) > 0 && onCopyChat && (
                              <button type="button" className="cx-icobtn" onClick={(e) => { e.stopPropagation(); onCopyChat(chat) }} title={t('menu.copyChat')} aria-label={t('menu.copyChat')}>
                                <Icon name="clipboard" />
                              </button>
                            )}
                            {(chat.history?.length || 0) > 0 && onExport && (
                              <button type="button" className="cx-icobtn" onClick={(e) => { e.stopPropagation(); onExport(chat) }} title={t('menu.exportMarkdown')} aria-label={t('menu.exportMarkdown')}>
                                <Icon name="download" />
                              </button>
                            )}
                            {canDelete && (
                              <button type="button" className="cx-icobtn cx-icobtn--danger" onClick={(e) => { e.stopPropagation(); onDelete?.(chat) }} title={t('menu.deleteChat')} aria-label={t('menu.deleteChat')}>
                                <Icon name="trash" />
                              </button>
                            )}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                ))
              })()}
            </div>

            <div className="cx-menu__foot">
              <span><kbd className="dk-kbd">↑</kbd><kbd className="dk-kbd">↓</kbd> {t('menu.keys.move')}</span>
              <span><kbd className="dk-kbd">↵</kbd> {t('menu.keys.open')}</span>
              <span><kbd className="dk-kbd">F2</kbd> {t('menu.keys.rename')}</span>
              <span><kbd className="dk-kbd">Del</kbd> {t('menu.keys.delete')}</span>
              {chats.length > 1 && (
                <button
                  type="button"
                  className="cx-btn cx-btn--ghost cx-menu__clear"
                  onClick={() => { onDeleteAll?.(); close() }}
                  title={t('menu.deleteAllTitle')}
                >
                  {t('menu.clearAll')}
                </button>
              )}
            </div>
          </div>
        </>,
        document.body,
      )}
    </div>
  )
})

export default ChatsMenu
