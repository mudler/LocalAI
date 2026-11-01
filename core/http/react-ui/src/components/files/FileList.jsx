/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import Icon from '../Icon'
import ActionMenu from '../ActionMenu'
import { filesApi } from '../../utils/api'
import { fileSize } from '../../utils/fileInfo'
import { KIND_ICON, extOf, findByPrefix, hasThumb, kindOf, nameProblem, nextIndex, relativeDate } from '../../utils/fileBrowser'

// The picture of an entry: a thumbnail for an image (loaded lazily, the icon
// if it fails), else the icon of its kind.
export function EntryIcon({ entry, big = false }) {
  const [broken, setBroken] = useState(false)
  const kind = kindOf(entry)
  if (hasThumb(entry) && !broken) {
    return (
      <span className={`ws-thumb${big ? ' ws-thumb--big' : ''}`} aria-hidden="true">
        <img src={filesApi.contentUrl(entry.path, true)} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} />
      </span>
    )
  }
  return (
    <span className={`ws-glyph ws-glyph--${kind}${big ? ' ws-glyph--big' : ''}`} aria-hidden="true">
      <Icon name={KIND_ICON[kind]} />
      {big && kind !== 'folder' && extOf(entry.name) && <span className="ws-glyph__ext">{extOf(entry.name)}</span>}
    </span>
  )
}

// The name field of a rename or a new entry: Enter keeps it, Escape drops it,
// and a name that cannot be used says why under the field.
export function NameField({ initial, label, taken, current = '', onCommit, onCancel, t }) {
  const [value, setValue] = useState(initial)
  const [problem, setProblem] = useState('')
  const ref = useRef(null)
  const done = useRef(false)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    el.focus({ preventScroll: true })
    // The name without its extension is selected, as on a desktop.
    const ext = extOf(initial)
    el.setSelectionRange(0, ext && initial.length > ext.length + 1 ? initial.length - ext.length - 1 : initial.length)
  }, [initial])
  const commit = (fromBlur) => {
    if (done.current) return
    const name = value.trim()
    if (name === current) { done.current = true; onCancel(); return }
    const why = nameProblem(name, taken, current)
    if (why) {
      if (fromBlur) { done.current = true; onCancel(); return }
      setProblem(why)
      return
    }
    done.current = true
    onCommit(name)
  }
  return (
    <span className="ws-namefield">
      <input
        ref={ref} className="input ws-namefield__input" aria-label={label} value={value}
        aria-invalid={problem ? true : undefined} aria-describedby={problem ? 'ws-name-problem' : undefined}
        onChange={(e) => { setValue(e.target.value); setProblem('') }}
        onBlur={() => commit(true)}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          e.stopPropagation()
          // preventDefault: the Enter must not also press the confirmation
          // that a rename of a managed path opens and focuses.
          if (e.key === 'Enter') { e.preventDefault(); commit(false) }
          if (e.key === 'Escape') { e.preventDefault(); done.current = true; onCancel() }
        }}
      />
      {problem && <span className="ws-namefield__problem" id="ws-name-problem" role="alert">{t(problem)}</span>}
    </span>
  )
}

function Row({ entry, view, selected, cursor, dropping, previewing, renaming, ctx }) {
  const { t, locale, now, taken, anySelected } = ctx
  const isDir = entry.kind === 'dir'
  const about = isDir && entry.managed ? t(`managed.about.${entry.managed}`, { defaultValue: '' }) : ''
  const when = relativeDate(entry.modified, { now, locale })
  const kind = kindOf(entry)
  const menu = ctx.menuItems(entry)
  const nameEl = renaming ? (
    <NameField
      initial={entry.name} current={entry.name} label={t('rename')} taken={taken} t={t}
      onCommit={(name) => ctx.onRename(entry, name)} onCancel={ctx.onRenameCancel}
    />
  ) : (
    <button
      type="button" className="ws-row__name" tabIndex={-1} title={entry.name}
      onClick={(e) => { if (e.shiftKey || e.metaKey || e.ctrlKey) return; e.stopPropagation(); ctx.onOpen(entry) }}
    >
      {entry.name}
    </button>
  )
  return (
    <li
      role="row" aria-selected={selected} tabIndex={cursor ? 0 : -1} data-path={entry.path}
      className={`ws-row ws-row--${view}${selected ? ' ws-row--selected' : ''}${entry.managed ? ' ws-row--managed' : ''}${dropping ? ' ws-row--drop' : ''}${previewing ? ' ws-row--open' : ''}`}
      data-testid="ws-row" data-managed={entry.managed || undefined} data-kind={kind}
      draggable={!renaming && !ctx.touch}
      onClick={(e) => ctx.onRowClick(entry, e)}
      onDoubleClick={() => ctx.onOpen(entry)}
      onContextMenu={(e) => { e.preventDefault(); ctx.onContextMenu(entry, e) }}
      onFocus={(e) => { if (e.target === e.currentTarget) ctx.onFocusRow(entry) }}
      onDragStart={(e) => ctx.onDragStart(entry, e)}
      onDragEnd={ctx.onDragEnd}
      onDragOver={isDir ? (e) => ctx.onFolderDragOver(entry, e) : undefined}
      onDragLeave={isDir ? (e) => ctx.onFolderDragLeave(entry, e) : undefined}
      onDrop={isDir ? (e) => ctx.onFolderDrop(entry, e) : undefined}
    >
      <span role="gridcell" className="ws-cell ws-cell--check">
        <input
          type="checkbox" className="ws-check" tabIndex={-1} checked={selected} data-visible={anySelected || undefined}
          aria-label={t('select.one', { name: entry.name })}
          onClick={(e) => e.stopPropagation()}
          onChange={(e) => ctx.onToggle(entry, e.nativeEvent?.shiftKey)}
        />
      </span>
      <span role="gridcell" className="ws-cell ws-cell--name">
        <EntryIcon entry={entry} big={view === 'grid'} />
        <span className="ws-row__main">
          <span className="ws-row__line">
            {nameEl}
            {isDir && entry.managed && <span className="ws-managed-tag" data-testid="ws-managed-tag">{t('managed.marker')}</span>}
          </span>
          {about && <span className="ws-row__about">{about}</span>}
          <span className="ws-row__meta">
            {[isDir ? t(`types.${kind}`) : fileSize(entry.size), when.label].filter(Boolean).join(' · ')}
          </span>
        </span>
      </span>
      <span role="gridcell" className="ws-cell ws-cell--modified" title={when.exact}>{when.label}</span>
      <span role="gridcell" className="ws-cell ws-cell--type">{t(`types.${kind}`)}</span>
      <span role="gridcell" className="ws-cell ws-cell--size">{isDir ? '' : fileSize(entry.size)}</span>
      <span role="gridcell" className="ws-cell ws-cell--acts">
        {!isDir && (
          <a
            className="ws-icon-btn ws-row__quick" href={filesApi.contentUrl(entry.path)} download={entry.name} tabIndex={-1}
            aria-label={t('downloadOne', { name: entry.name })} title={t('download')} onClick={(e) => e.stopPropagation()}
          >
            <Icon name="download" />
          </a>
        )}
        <ActionMenu items={menu} ariaLabel={t('menu.label', { name: entry.name })} triggerLabel={t('menu.more', { name: entry.name })} compact />
      </span>
    </li>
  )
}

const SORTS = ['name', 'modified', 'type', 'size']

// The file list of a folder: a sortable table, or a grid of tiles with
// thumbnails. It draws what the page gives it and reports what the person
// does; the page owns the selection and the actions.
//
// Keyboard (the focus is on a row): arrows move, Shift+arrows extend the
// selection, Space selects, Enter opens, F2 renames, Delete moves to the
// Trash, Ctrl+A selects all, Escape clears the selection, Backspace or
// Alt+Up opens the parent folder, and typing a name jumps to it.
export default function FileList({
  groups, view, sort, onSort, selected, cursor, setCursor, previewPath, renaming, creating, dropPath, ctx, t,
}) {
  const listRef = useRef(null)
  const typed = useRef({ text: '', at: 0 })
  const order = groups.flatMap(g => g.entries)
  const paths = order.map(e => e.path)
  const cursorPath = paths.includes(cursor) ? cursor : paths[0]

  const focusPath = (p) => {
    setCursor(p)
    requestAnimationFrame(() => {
      const el = listRef.current?.querySelector(`[data-path="${CSS.escape(p)}"]`)
      el?.focus({ preventScroll: false })
      el?.scrollIntoView?.({ block: 'nearest' })
    })
  }

  const cols = () => {
    if (view !== 'grid' || !listRef.current) return 1
    const ul = listRef.current.querySelector('.ws-grid')
    if (!ul) return 1
    return Math.max(1, getComputedStyle(ul).gridTemplateColumns.split(' ').filter(Boolean).length)
  }

  const onKeyDown = (e) => {
    if (e.target.closest('input, textarea, [role="menu"]') && e.target.type !== 'checkbox') return
    const i = paths.indexOf(cursorPath)
    const entry = order[i]
    const mod = e.ctrlKey || e.metaKey
    if (['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key) && !(e.altKey && e.key === 'ArrowUp')) {
      const j = nextIndex(e.key, i, paths.length, cols())
      if (j < 0 || j === i) return
      e.preventDefault()
      if (e.shiftKey) ctx.onExtend(paths[j])
      focusPath(paths[j])
      return
    }
    if (mod && e.key.toLowerCase() === 'a') { e.preventDefault(); ctx.onSelectAll(); return }
    if (e.key === 'Escape' && selected.size > 0) { e.preventDefault(); ctx.onClear(); return }
    if (!entry) return
    if (e.key === ' ') { e.preventDefault(); ctx.onToggle(entry); return }
    if (e.key === 'Enter') { e.preventDefault(); ctx.onOpen(entry); return }
    if (e.key === 'F2') { e.preventDefault(); ctx.onStartRename(entry); return }
    if (e.key === 'Delete') { e.preventDefault(); ctx.onDeleteKey(entry); return }
    if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) { e.preventDefault(); ctx.onUp(); return }
    if (e.key.length === 1 && !mod && !e.altKey && /\S/.test(e.key)) {
      const at = Date.now()
      typed.current = { text: at - typed.current.at > 700 ? e.key : typed.current.text + e.key, at }
      const j = findByPrefix(order.map(x => x.name), typed.current.text, typed.current.text.length > 1 ? i : i + 1)
      if (j >= 0) { e.preventDefault(); focusPath(paths[j]) }
    }
  }

  const rowCtx = { ...ctx, anySelected: selected.size > 0 }
  const allSelected = paths.length > 0 && paths.every(p => selected.has(p))
  const someSelected = !allSelected && paths.some(p => selected.has(p))

  const header = view === 'list' && (
    <div role="row" className="ws-head-row">
      <span role="columnheader" className="ws-cell ws-cell--check">
        <input
          type="checkbox" className="ws-check" data-visible={selected.size > 0 || undefined} checked={allSelected}
          ref={(el) => { if (el) el.indeterminate = someSelected }}
          aria-label={t('select.all')} onChange={() => (allSelected ? ctx.onClear() : ctx.onSelectAll())}
        />
      </span>
      {SORTS.map(key => {
        const on = sort.key === key
        return (
          <span
            key={key} role="columnheader" className={`ws-cell ws-cell--${key}`}
            aria-sort={on ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}
          >
            <button type="button" className={`ws-sort${on ? ' ws-sort--on' : ''}`} onClick={() => onSort(key)} data-testid={`ws-sort-${key}`}>
              {t(`columns.${key}`)}
              <Icon name={on && sort.dir === 'desc' ? 'arrow-down' : 'arrow-up'} className="ws-sort__arrow" />
            </button>
          </span>
        )
      })}
      <span role="columnheader" className="ws-cell ws-cell--acts"><span className="sr-only">{t('columns.actions')}</span></span>
    </div>
  )

  const newRow = creating && (
    <li role="row" className={`ws-row ws-row--${view} ws-row--new`} data-testid="ws-new-row">
      <span role="gridcell" className="ws-cell ws-cell--check" />
      <span role="gridcell" className="ws-cell ws-cell--name">
        <EntryIcon entry={{ kind: creating.kind, name: creating.name }} big={view === 'grid'} />
        <span className="ws-row__main">
          <NameField
            initial={creating.name} label={t(creating.kind === 'dir' ? 'folderName' : 'fileName')} taken={ctx.taken} t={t}
            onCommit={ctx.onCreate} onCancel={ctx.onCreateCancel}
          />
        </span>
      </span>
    </li>
  )

  return (
    <div
      ref={listRef} role="grid" aria-multiselectable="true" aria-label={t('tabs.files')} aria-rowcount={order.length}
      className={`ws-table ws-table--${view}`} onKeyDown={onKeyDown} data-testid="ws-list"
    >
      {header}
      {groups.map((g, gi) => (
        <div role="rowgroup" key={g.key} className={g.title ? 'ws-managed' : 'ws-own'} data-testid={g.title ? 'ws-managed' : undefined} aria-labelledby={g.title ? 'ws-managed-title' : undefined}>
          {g.title && <h2 className="ws-managed__title" id="ws-managed-title">{g.title}</h2>}
          <ul className={view === 'grid' ? 'ws-grid' : 'ws-rows'} role="presentation">
            {gi === 0 && newRow}
            {g.entries.map(e => (
              <Row
                key={e.path} entry={e} view={view} selected={selected.has(e.path)} cursor={e.path === cursorPath}
                dropping={dropPath === e.path} previewing={previewPath === e.path} renaming={renaming === e.path} ctx={rowCtx}
              />
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}

