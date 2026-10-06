import { useState, useEffect, useRef, useCallback, useImperativeHandle, forwardRef } from 'react'
import { useTranslation } from 'react-i18next'
import { relativeTime } from '../utils/format'
import Icon from './Icon'

function getLastMessagePreview(chat) {
  if (!chat?.history || chat.history.length === 0) return ''
  for (let i = chat.history.length - 1; i >= 0; i--) {
    const msg = chat.history[i]
    if (msg.role === 'user' || msg.role === 'assistant') {
      const text = typeof msg.content === 'string' ? msg.content : msg.content?.[0]?.text || ''
      return text.slice(0, 60).replace(/\n/g, ' ')
    }
  }
  return ''
}

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
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [editingId, setEditingId] = useState(null)
  const [editName, setEditName] = useState('')
  const [activeIdx, setActiveIdx] = useState(0)
  const containerRef = useRef(null)
  const searchRef = useRef(null)
  const triggerRef = useRef(null)
  const listRef = useRef(null)

  useImperativeHandle(ref, () => ({
    open: () => setOpen(true),
    close: () => setOpen(false),
    toggle: () => setOpen(prev => !prev),
  }), [])

  const filtered = search.trim()
    ? chats.filter(c => {
        const q = search.toLowerCase()
        if ((c.name || '').toLowerCase().includes(q)) return true
        return c.history?.some(m => {
          const t = typeof m.content === 'string' ? m.content : m.content?.[0]?.text || ''
          return t.toLowerCase().includes(q)
        })
      })
    : chats

  // Click-outside to close
  useEffect(() => {
    if (!open) return
    const handleClick = (e) => {
      if (containerRef.current && !containerRef.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [open])

  // Esc closes; focus search on open; reset state on close
  useEffect(() => {
    if (!open) {
      setSearch('')
      setEditingId(null)
      setActiveIdx(0)
      // Restore focus to trigger when closing
      triggerRef.current?.focus()
      return
    }
    setActiveIdx(filtered.findIndex(c => c.id === activeChatId))
    // Focus search shortly after open so the popover animation doesn't fight focus
    const t = setTimeout(() => searchRef.current?.focus(), 20)
    const onKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        setOpen(false)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      clearTimeout(t)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  const handleSelect = useCallback((id) => {
    onSelect?.(id)
    setOpen(false)
  }, [onSelect])

  const handleNew = useCallback(() => {
    onNew?.()
    setOpen(false)
  }, [onNew])

  const startRename = (id, name) => {
    setEditingId(id)
    setEditName(name || '')
  }

  const finishRename = () => {
    if (editingId && editName.trim()) {
      onRename?.(editingId, editName.trim())
    }
    setEditingId(null)
  }

  const handleListKey = (e) => {
    if (filtered.length === 0) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIdx(i => Math.min(i + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIdx(i => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const target = filtered[activeIdx]
      if (target) handleSelect(target.id)
    }
  }

  // Keep highlighted item scrolled into view
  useEffect(() => {
    if (!open) return
    const el = listRef.current?.querySelector(`[data-idx="${activeIdx}"]`)
    el?.scrollIntoView({ block: 'nearest' })
  }, [activeIdx, open])

  return (
    <div className="chats-menu" ref={containerRef}>
      <button
        ref={triggerRef}
        type="button"
        className={`btn btn-secondary btn-sm chats-menu-trigger${open ? ' active' : ''}`}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t('menu.triggerTitle')}
        onClick={() => setOpen(prev => !prev)}
      >
        <Icon name="chat" />
        <span className="chats-menu-trigger-label">{t('menu.trigger')}</span>
        <kbd className="chats-menu-trigger-kbd">⌘K</kbd>
      </button>

      {open && (
        <div className="chats-menu-popover" role="menu" onKeyDown={handleListKey}>
          <div className="chats-menu-search">
            <Icon name="search" className="chats-menu-search-icon" />
            <input
              ref={searchRef}
              className="chats-menu-search-input"
              type="text"
              value={search}
              onChange={(e) => { setSearch(e.target.value); setActiveIdx(0) }}
              placeholder={t('menu.search')}
            />
            {search && (
              <button
                type="button"
                className="chats-menu-search-clear"
                onClick={() => setSearch('')}
                aria-label={t('menu.clearSearch')}
              >
                <Icon name="close" />
              </button>
            )}
          </div>

          <div className="chats-menu-list" ref={listRef}>
            {filtered.length === 0 && (
              <div className="chats-menu-empty">
                {search ? t('menu.noMatch') : t('menu.noConversations')}
              </div>
            )}
            {filtered.map((chat, idx) => (
              <div
                key={chat.id}
                role="menuitem"
                tabIndex={-1}
                data-idx={idx}
                className={`chats-menu-item${chat.id === activeChatId ? ' active' : ''}${idx === activeIdx ? ' highlighted' : ''}`}
                onClick={() => handleSelect(chat.id)}
                onMouseEnter={() => setActiveIdx(idx)}
              >
                <Icon name="chat" className="chats-menu-item-icon" />
                {editingId === chat.id ? (
                  <input
                    className="input chats-menu-item-rename"
                    value={editName}
                    onChange={(e) => setEditName(e.target.value)}
                    onBlur={finishRename}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') { e.preventDefault(); finishRename() }
                      if (e.key === 'Escape') { e.preventDefault(); setEditingId(null) }
                    }}
                    autoFocus
                    onClick={(e) => e.stopPropagation()}
                  />
                ) : (
                  <div className="chats-menu-item-info">
                    <div className="chats-menu-item-top">
                      <span
                        className="chats-menu-item-name"
                        onDoubleClick={(e) => { e.stopPropagation(); startRename(chat.id, chat.name) }}
                      >
                        {streamingChatId === chat.id && (
                          <Icon name="spinner" spin className="chats-menu-item-spin" />
                        )}
                        {chat.name}
                      </span>
                      <span className="chats-menu-item-time">{relativeTime(chat.updatedAt)}</span>
                    </div>
                    <span className="chats-menu-item-preview">
                      {getLastMessagePreview(chat) || t('empty.noMessages')}
                    </span>
                  </div>
                )}
                <div className="chats-menu-item-actions">
                  <button
                    type="button"
                    onClick={(e) => { e.stopPropagation(); startRename(chat.id, chat.name) }}
                    title={t('menu.rename')}
                  >
                    <Icon name="pencil" />
                  </button>
                  {onDuplicate && (
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); onDuplicate(chat); setOpen(false) }}
                      title={t('menu.duplicate')}
                    >
                      <Icon name="copy" />
                    </button>
                  )}
                  {(chat.history?.length || 0) > 0 && onCopyChat && (
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); onCopyChat(chat) }}
                      title={t('menu.copyChat')}
                    >
                      <Icon name="clipboard" />
                    </button>
                  )}
                  {(chat.history?.length || 0) > 0 && onExport && (
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); onExport(chat) }}
                      title={t('menu.exportMarkdown')}
                    >
                      <Icon name="download" />
                    </button>
                  )}
                  {chats.length > 1 && (
                    <button
                      type="button"
                      className="chats-menu-item-delete"
                      onClick={(e) => { e.stopPropagation(); onDelete?.(chat.id) }}
                      title={t('menu.deleteChat')}
                    >
                      <Icon name="trash" />
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>

          <div className="chats-menu-footer">
            <button type="button" className="btn btn-primary btn-sm chats-menu-new" onClick={handleNew}>
              <Icon name="plus" /> {t('menu.newChat')}
            </button>
            {chats.length > 1 && (
              <button
                type="button"
                className="btn btn-secondary btn-sm chats-menu-clear-all"
                onClick={() => { onDeleteAll?.(); setOpen(false) }}
                title={t('menu.deleteAllTitle')}
              >
                <Icon name="trash" /> {t('menu.clearAll')}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  )
})

export default ChatsMenu
