import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { groupConversations } from '../utils/homeConversations'
import Icon from './Icon'
import { knownKind, resolveOrigin } from '../utils/originChats'
import { menuChats } from '../utils/chatThread'
import { chatAgentsApi } from '../utils/api'
import { useOriginContext } from '../hooks/useOriginContext'
import { filtersActive, filtersKey, hitRows, noFilters, searchParams } from '../utils/chatSearch'
import SearchSnippet from './chat/SearchSnippet'
import SearchFilters from './chat/SearchFilters'

// The chats come from the server list (see menuChats in utils/chatThread.js):
// a name, the model and the times. With onSearch, a search of two characters
// or more leaves the day blocks: the server searches every chat, titles and
// messages (GET /api/chats/search), and the list shows its hits best first,
// each with the words that matched marked in a snippet. A hit opens its chat
// at the message (onSelect gets { seq }). Filters narrow the search by agent,
// origin and date.

const SEARCH_DELAY_MS = 250

// The chat history: a search box, one block per day, the model that answered
// and the time, with rename, copy, export and delete on each row. A chat that
// no person started (a trigger, a connector, a job or a page) carries a badge
// with the name of what started it (originTitle) or the name of its kind. It reads
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
  originTitle,
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

  const query = search.trim()
  const searching = Boolean(onSearch) && query.length >= 2
  const [filters, setFilters] = useState(noFilters)
  const fkey = filtersKey(filters)
  const [tries, setTries] = useState(0)
  // What the server found: the answer to the search named by `key`. The rows
  // of an earlier search stay on screen, dimmed, while the next one runs.
  const [found, setFound] = useState({ key: '', status: 'idle', rows: [], meta: null })
  const searchKey = `${query}|${fkey}`
  const onSearchRef = useRef(onSearch)
  onSearchRef.current = onSearch
  const filtersRef = useRef(filters)
  filtersRef.current = filters
  useEffect(() => {
    if (!searching) return undefined
    let live = true
    setFound((f) => (f.key === searchKey && f.status === 'ready' ? f : { ...f, key: searchKey, status: 'loading' }))
    const timer = setTimeout(() => {
      Promise.resolve(onSearchRef.current(query, searchParams(filtersRef.current)))
        .then((res) => {
          if (!live) return
          setFound({
            key: searchKey, status: 'ready', rows: hitRows(res, (c) => menuChats([c])[0]),
            meta: { indexing: !!res?.indexing, partial: !!res?.partial, searched: res?.searched_chats || 0, total: res?.total_chats || 0 },
          })
        })
        .catch(() => { if (live) setFound({ key: searchKey, status: 'error', rows: [], meta: null }) })
    }, SEARCH_DELAY_MS)
    return () => { live = false; clearTimeout(timer) }
  }, [searching, query, searchKey, tries])
  const searchState = !searching ? 'idle' : found.key === searchKey ? found.status : 'loading'

  // Saved agents for the filter, read once the first search starts.
  const [agents, setAgents] = useState(null)
  useEffect(() => {
    if (!searching || agents) return undefined
    let live = true
    chatAgentsApi.list().then((r) => { if (live) setAgents(r.agents || []) }).catch(() => { if (live) setAgents([]) })
    return () => { live = false }
  }, [searching, agents])
  // What started the chats of the hits: the names of triggers and connections.
  const originCtx = useOriginContext(searching && found.rows.some((r) => r.chat.origin))

  // The chats shown in day blocks, newest first. Rows of a search come as the
  // server ranks them.
  const items = useMemo(() => [...chats].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0)), [chats])
  const groups = useMemo(() => (searching ? [] : groupConversations(items)), [items, searching])
  const flat = useMemo(() => (
    searching
      ? found.rows.map((r) => ({ key: r.key, chat: r.chat, hit: r }))
      : groups.flatMap((g) => g.items.map((c) => ({ key: c.id, chat: c, group: g.key })))
  ), [searching, found.rows, groups])

  const fmtTime = useMemo(() => new Intl.DateTimeFormat(i18n.language, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }), [i18n.language])
  const fmtDay = useMemo(() => new Intl.DateTimeFormat(i18n.language, { weekday: 'short' }), [i18n.language])
  const fmtDate = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: 'numeric', month: 'short' }), [i18n.language])
  const when = (group, ts) => (group === 'older' ? fmtDate.format(ts) : group === 'week' ? fmtDay.format(ts) : fmtTime.format(ts))
  // A hit has no day block, so its time carries the date.
  const fmtHit = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: 'numeric', month: 'short', year: 'numeric' }), [i18n.language])

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

  useEffect(() => { setActiveIdx(0) }, [query, fkey])
  // A new answer starts on its best hit.
  useEffect(() => { if (found.status === 'ready') setActiveIdx(0) }, [found])

  useEffect(() => {
    listRef.current?.querySelector(`[data-idx="${activeIdx}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [activeIdx])

  // Keep the cursor inside the list when a row goes away.
  useEffect(() => {
    if (activeIdx > flat.length - 1) setActiveIdx(Math.max(0, flat.length - 1))
  }, [flat.length, activeIdx])

  const choose = useCallback((id, hit) => { onSelect?.(id, hit?.seq ? { seq: hit.seq } : undefined) }, [onSelect])

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
      } else if (search) {
        // The first Escape clears the search; the next one leaves.
        e.preventDefault()
        e.stopPropagation()
        setSearch('')
        setFilters(noFilters)
      } else if (onEscape) {
        e.preventDefault()
        e.stopPropagation()
        onEscape()
      }
      return
    }
    if (editingId) return
    if (e.key === 'ArrowDown') { e.preventDefault(); setActiveIdx(i => Math.min(i + 1, flat.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActiveIdx(i => Math.max(i - 1, 0)) }
    else if (e.key === 'PageDown' && e.target === searchRef.current) { e.preventDefault(); setActiveIdx(i => Math.min(i + 5, flat.length - 1)) }
    else if (e.key === 'PageUp' && e.target === searchRef.current) { e.preventDefault(); setActiveIdx(i => Math.max(i - 5, 0)) }
    else if (e.key === 'Enter' && e.target === searchRef.current && current) { e.preventDefault(); choose(current.chat.id, current.hit) }
    else if (e.key === 'F2' && current) { e.preventDefault(); startRename(current.chat) }
    else if (e.key === 'Delete' && current && canDelete && (e.target !== searchRef.current || !search)) {
      e.preventDefault()
      onDelete?.(current.chat)
    }
  }

  const listId = `${idPrefix}-menu-list`
  const showFilters = searching || (Boolean(onSearch) && filtersActive(filters) && search.trim().length > 0)
  const meta = searching && found.status === 'ready' ? found.meta : null

  // The words under the list while a search runs or ends.
  const statusLine = (() => {
    if (!searching) return ''
    if (searchState === 'loading') return t('menu.find.searching')
    if (searchState === 'error') return ''
    return flat.length > 0 ? t('menu.find.count', { count: flat.length }) : ''
  })()

  const renderRow = (entry, idx, groupKey) => {
    const { chat, hit } = entry
    const selected = idx === activeIdx
    const editing = editingId === chat.id
    const originKind = chat.origin ? (knownKind(chat.origin)) : ''
    const role = hit?.role === 'user' ? t('menu.find.you') : hit?.role === 'assistant' ? t('menu.find.assistant') : ''
    const stamp = hit ? (hit.at || chat.updatedAt || chat.createdAt || 0) : (chat.updatedAt || chat.createdAt || 0)
    return (
      <div
        key={entry.key}
        id={`${idPrefix}-opt-${entry.key}`}
        role="option"
        aria-selected={selected}
        data-idx={idx}
        data-seq={hit?.seq || undefined}
        data-current={chat.id === activeChatId || undefined}
        data-testid="chats-row"
        className="cx-row-item"
        onClick={() => !editing && choose(chat.id, hit)}
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
          {originKind && (
            <span className="cx-row-item__origin" data-testid="chats-origin-badge" title={t(`menu.origin.${originKind}`)}>
              {(originCtx && resolveOrigin({ origin: chat.origin, originId: chat.originId }, originCtx).title) || originTitle?.(chat) || t(`menu.originBadge.${originKind}`)}
            </span>
          )}
        </div>
        {hit && hit.snippet && (
          <div className="cx-row-item__preview cx-find-snip" data-testid="chats-snippet">
            {role && <span className="cx-find-snip__who">{role}</span>}
            <SearchSnippet text={hit.snippet} ranges={hit.highlights} />
            {hit.more > 0 && <span className="cx-find-snip__more">{t('menu.find.more', { count: hit.more })}</span>}
          </div>
        )}
        {hit && !hit.snippet && hit.inTitle && (
          <div className="cx-row-item__preview cx-find-snip">{t('menu.find.inTitle')}</div>
        )}
        <span className="cx-row-item__model">{chat.model || t('menu.noModel')}</span>
        <span className="cx-row-item__time">{hit ? fmtHit.format(stamp) : when(groupKey, stamp)}</span>
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
  }

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
          aria-activedescendant={current ? `${idPrefix}-opt-${current.key}` : undefined}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('menu.search')}
          aria-label={t('menu.search')}
        />
        {search && (
          <button type="button" className="cx-icobtn" onClick={() => { setSearch(''); setFilters(noFilters); searchRef.current?.focus() }} aria-label={t('menu.clearSearch')}>
            <Icon name="close" />
          </button>
        )}
        {onNew && (
          <button type="button" className="cx-btn" onClick={() => onNew()} data-testid="chats-new">
            <Icon name="plus" /> {t('menu.newChat')}
          </button>
        )}
      </div>

      {showFilters && (
        <SearchFilters
          filters={filters}
          onChange={setFilters}
          agents={agents || []}
          idPrefix={idPrefix}
          active={filtersActive(filters)}
        />
      )}
      {searching && (
        <div className="cx-find-status" role="status" aria-live="polite" data-testid="chats-search-status">
          {statusLine}
          {meta?.indexing && <span className="cx-find-status__note" data-testid="chats-search-indexing">{t('menu.find.indexing')}</span>}
          {meta?.partial && <span className="cx-find-status__note" data-testid="chats-search-partial">{t('menu.find.partial', { searched: meta.searched, total: meta.total })}</span>}
        </div>
      )}

      <div className="cx-menu__list" ref={listRef} id={listId} role="listbox" aria-label={t('menu.title')} aria-busy={searchState === 'loading' || undefined} data-state={searchState}>
        {searchState === 'error' && (
          <div className="cx-menu__empty" role="alert" data-testid="chats-search-error">
            <p>{t('menu.find.failed')}</p>
            <button type="button" className="cx-btn" onClick={() => setTries((n) => n + 1)}>{t('menu.find.retry')}</button>
          </div>
        )}
        {searchState !== 'error' && flat.length === 0 && searchState !== 'loading' && (
          <div className="cx-menu__empty" data-testid="chats-empty">
            {searching
              ? (filtersActive(filters) ? t('menu.find.noMatchFiltered', { query }) : t('menu.find.noMatchFor', { query }))
              : search ? t('menu.noMatch') : t('menu.noConversations')}
          </div>
        )}
        {searchState !== 'error' && searchState === 'loading' && flat.length === 0 && (
          <div className="cx-menu__empty" data-testid="chats-loading">{t('menu.find.searching')}</div>
        )}
        {searching && searchState !== 'error' && (
          <div className="cx-find-list" data-dim={searchState === 'loading' || undefined}>
            {flat.map((entry, idx) => renderRow(entry, idx))}
          </div>
        )}
        {!searching && (() => {
          let n = 0
          return groups.map(g => (
            <div key={g.key} className="cx-menu__group" data-testid={`chats-day-${g.key}`}>
              <h3 className="cx-menu__day">{t(`menu.group.${g.key}`)}</h3>
              {g.items.map(chat => renderRow(flat[n], n++, g.key))}
            </div>
          ))
        })()}
      </div>

      <div className="cx-menu__foot">
        <span><kbd className="dk-kbd">↑</kbd><kbd className="dk-kbd">↓</kbd> {t('menu.keys.move')}</span>
        <span><kbd className="dk-kbd">↵</kbd> {t('menu.keys.open')}</span>
        <span><kbd className="dk-kbd">F2</kbd> {t('menu.keys.rename')}</span>
        <span><kbd className="dk-kbd">Del</kbd> {t('menu.keys.delete')}</span>
        <span><kbd className="dk-kbd">Esc</kbd> {t('menu.keys.clear')}</span>
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
