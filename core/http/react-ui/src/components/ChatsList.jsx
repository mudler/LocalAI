import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { groupConversations } from '../utils/homeConversations'
import Icon from './Icon'

// The chats come from the server list (see menuChats in utils/chatThread.js):
// a name, the model and the times. The names are matched here at once; with
// onSearch the server also finds the chats whose messages hold the text
// (GET /api/chats/search), and the line under a name shows where it matched.
function matches(chat, query) {
  return (chat.name || '').toLowerCase().includes(query)
}

const SEARCH_DELAY_MS = 250

// The chat history: a search box, one block per day, the model that answered
// and the time, with rename, copy, export and delete on each row. It reads
// like the Home "Jump back in" list. Two frames show it: the Ctrl or Cmd K
// dialog of a transcript (ChatsMenu) and the All chats page.
//
// The frame owns what the actions do. `onDelete` is only a request: the frame
// hides the row and offers an undo before anything is removed. `onEscape`
// runs on an Escape that the list does not use itself (the dialog closes).
export default function ChatsList({
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
  onEscape,
  autoFocus = false,
  idPrefix = 'chats',
}) {
  const { t, i18n } = useTranslation('chat')
  const [search, setSearch] = useState('')
  const [editingId, setEditingId] = useState(null)
  const [editName, setEditName] = useState('')
  const [activeIdx, setActiveIdx] = useState(0)
  const searchRef = useRef(null)
  const listRef = useRef(null)

  const query = search.trim().toLowerCase()
  // What the server found for the query: chat id -> snippet ('' for a title hit).
  const [hits, setHits] = useState({ query: '', snippets: {} })
  useEffect(() => {
    if (!onSearch || query.length < 2) return undefined
    let live = true
    const timer = setTimeout(() => {
      onSearch(query)
        .then((res) => {
          if (!live) return
          const snippets = {}
          for (const h of res?.hits || []) if (h?.chat?.id) snippets[h.chat.id] = h.in_title ? '' : (h.snippet || '')
          setHits({ query, snippets })
        })
        .catch(() => { if (live) setHits({ query, snippets: {} }) })
    }, SEARCH_DELAY_MS)
    return () => { live = false; clearTimeout(timer) }
  }, [onSearch, query])
  const snippets = useMemo(() => (hits.query === query ? hits.snippets : {}), [hits, query])

  // Chats that hold a message, and the one you are in. An empty placeholder is
  // not worth a row. Newest first, which is what the day blocks need.
  const items = useMemo(() => {
    const found = query ? chats.filter(c => matches(c, query) || c.id in snippets) : chats
    return [...found].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0))
  }, [chats, query, snippets])

  const groups = useMemo(() => groupConversations(items), [items])
  const flat = useMemo(() => groups.flatMap(g => g.items), [groups])

  const fmtTime = useMemo(() => new Intl.DateTimeFormat(i18n.language, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }), [i18n.language])
  const fmtDay = useMemo(() => new Intl.DateTimeFormat(i18n.language, { weekday: 'short' }), [i18n.language])
  const fmtDate = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: 'numeric', month: 'short' }), [i18n.language])
  const when = (group, ts) => (group === 'older' ? fmtDate.format(ts) : group === 'week' ? fmtDay.format(ts) : fmtTime.format(ts))

  // On mount: start on the current chat and, in the dialog, put the cursor in
  // the search box.
  useEffect(() => {
    const at = items.findIndex(c => c.id === activeChatId)
    setActiveIdx(at < 0 ? 0 : at)
    if (!autoFocus) return undefined
    const timer = setTimeout(() => searchRef.current?.focus(), 20)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => { setActiveIdx(0) }, [query])

  useEffect(() => {
    listRef.current?.querySelector(`[data-idx="${activeIdx}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [activeIdx])

  // Keep the cursor inside the list when a row goes away.
  useEffect(() => {
    if (activeIdx > flat.length - 1) setActiveIdx(Math.max(0, flat.length - 1))
  }, [flat.length, activeIdx])

  const choose = useCallback((id) => { onSelect?.(id) }, [onSelect])

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

  const canDelete = chats.length > 0
  const current = flat[activeIdx]

  const onKeyDown = (e) => {
    if (e.key === 'Escape') {
      if (editingId) {
        e.preventDefault()
        e.stopPropagation()
        cancelRename()
      } else if (onEscape) {
        e.preventDefault()
        e.stopPropagation()
        onEscape()
      } else if (search) {
        e.preventDefault()
        setSearch('')
      }
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

  const listId = `${idPrefix}-menu-list`

  return (
    <div className="cx-menu__body" onKeyDown={onKeyDown}>
      <div className="cx-menu__search">
        <Icon name="search" />
        <input
          ref={searchRef}
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls={listId}
          aria-activedescendant={current ? `${idPrefix}-opt-${current.id}` : undefined}
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
        {onNew && (
          <button type="button" className="cx-btn" onClick={() => onNew()} data-testid="chats-new">
            <Icon name="plus" /> {t('menu.newChat')}
          </button>
        )}
      </div>

      <div className="cx-menu__list" ref={listRef} id={listId} role="listbox" aria-label={t('menu.title')}>
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
                const preview = snippets[chat.id] || (chat.origin ? t(`menu.origin.${chat.origin}`, chat.origin) : '')
                return (
                  <div
                    key={chat.id}
                    id={`${idPrefix}-opt-${chat.id}`}
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
                      {chat.running && <Icon name="spinner" spin />}
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
                          {chat.name || t('menu.untitled')}
                        </span>
                      )}
                    </div>
                    {preview && <div className="cx-row-item__preview">{preview}</div>}
                    <span className="cx-row-item__model">{chat.model || t('menu.noModel')}</span>
                    <span className="cx-row-item__time">{when(g.key, chat.updatedAt || chat.createdAt || 0)}</span>
                    <div className="cx-row-item__acts">
                      <button type="button" className="cx-icobtn" onClick={(e) => { e.stopPropagation(); startRename(chat) }} title={t('menu.rename')} aria-label={t('menu.rename')}>
                        <Icon name="pencil" />
                      </button>
                      {onCopyChat && (
                        <button type="button" className="cx-icobtn" onClick={(e) => { e.stopPropagation(); onCopyChat(chat) }} title={t('menu.copyChat')} aria-label={t('menu.copyChat')}>
                          <Icon name="clipboard" />
                        </button>
                      )}
                      {onExport && (
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
        {onDeleteAll && chats.length > 1 && (
          <button
            type="button"
            className="cx-btn cx-btn--ghost cx-menu__clear"
            onClick={() => onDeleteAll?.()}
            title={t('menu.deleteAllTitle')}
          >
            {t('menu.clearAll')}
          </button>
        )}
      </div>
    </div>
  )
}
