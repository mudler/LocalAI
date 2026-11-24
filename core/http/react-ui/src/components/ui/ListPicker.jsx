// eslint-disable-next-line no-unused-vars
import { Fragment, forwardRef, useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Tag from './Tag'
import {
  FILTER_FROM, NONE_KEY, filterRows, groupHeading, initialActive, isRowSelected,
  moveActive, placeMenu, rowKey, showFilterBox, toggleValue,
} from './listPickerModel'
import './picker.css'

// The one list picker: a trigger, a menu with an optional filter box, and a
// listbox driven by the keyboard. ModelPicker, SearchableSelect and the
// composer pickers draw it. Props and the row shape: README.md, "ListPicker".
const ListPicker = forwardRef(function ListPicker(
  {
    items = [], value, onChange, onPick, multiple = false,
    variant = 'chip', icon, placeholder, ariaLabel, label, labelledBy, describedBy,
    triggerContent, triggerClassName = '', triggerProps,
    filter, filterFrom = FILTER_FROM, filterItems, filterLabel, filterPlaceholder,
    groups, heading, noneLabel, loading = false, empty, emptyText, noMatchText, loadingText,
    clearable = false, clearLabel, showCount = true, openable,
    disabled = false, invalid = false, id, className = '', menuClassName = '', style,
    before, footer, renderRow, onOpen, testId,
  },
  ref,
) {
  const { t } = useTranslation('common')
  const uid = useId()
  const menuId = `${uid}-menu`
  const listId = `${uid}-list`
  const textId = `${uid}-text`
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const rootRef = useRef(null)
  const triggerRef = useRef(null)
  const listRef = useRef(null)
  const filterRef = useRef(null)
  const menuRef = useRef(null)

  const values = useMemo(() => (multiple ? (Array.isArray(value) ? value : []) : []), [multiple, value])
  const noneRow = useMemo(() => (noneLabel ? { key: NONE_KEY, id: '', label: noneLabel, none: true, closes: true } : null), [noneLabel])
  const rowsFor = useCallback((q) => (filterItems ? filterItems(items, q) : filterRows(items, q)), [filterItems, items])
  // The none row stays on top whatever the filter says: it is how to undo.
  const matched = useMemo(() => rowsFor(query), [rowsFor, query])
  const rows = useMemo(() => (noneRow ? [noneRow, ...matched] : matched), [noneRow, matched])
  const showFilter = showFilterBox(filter, items.length, filterFrom)
  const canOpen = !disabled && (openable ?? (items.length > 0 || !!noneRow))

  const close = useCallback((restoreFocus = false) => {
    setOpen(false)
    setQuery('')
    if (restoreFocus) triggerRef.current?.focus()
  }, [])

  const openMenu = useCallback(() => {
    if (!canOpen) return
    const all = rowsFor('')
    setActive(initialActive(noneRow ? [noneRow, ...all] : all, value, multiple))
    setQuery('')
    setOpen(true)
    onOpen?.()
  }, [canOpen, rowsFor, noneRow, value, multiple, onOpen])

  useImperativeHandle(ref, () => ({ open: openMenu, close, focus: () => triggerRef.current?.focus() }), [openMenu, close])

  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (rootRef.current && !rootRef.current.contains(e.target)) close() }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open, close])

  // The menu is placed inside the room its trigger has: the window, cut down to
  // the nearest ancestor that clips (a side sheet's body). A menu that would
  // leave it hangs at the trigger's right edge or is pushed back in, and one
  // wider than the room is narrowed. The result goes to the menu as custom
  // properties that picker.css reads; the natural width is measured first, so
  // a filter that narrows the list narrows the menu too.
  useLayoutEffect(() => {
    const menu = menuRef.current
    const root = rootRef.current
    if (!open || !menu || !root || typeof window === 'undefined') return
    for (const name of ['--menu-left', '--menu-min', '--menu-max']) menu.style.removeProperty(name)
    let boundsLeft = 0
    let boundsRight = window.innerWidth
    for (let el = root.parentElement; el && el !== document.body; el = el.parentElement) {
      if (getComputedStyle(el).overflowX === 'visible') continue
      const r = el.getBoundingClientRect()
      boundsLeft = Math.max(boundsLeft, r.left)
      boundsRight = Math.min(boundsRight, r.right)
    }
    const rect = root.getBoundingClientRect()
    const { left, width } = placeMenu({ rootLeft: rect.left, rootRight: rect.right, menuWidth: menu.offsetWidth, boundsLeft, boundsRight })
    if (left !== 0) menu.style.setProperty('--menu-left', `${left}px`)
    if (width !== null) {
      menu.style.setProperty('--menu-min', `${width}px`)
      menu.style.setProperty('--menu-max', `${width}px`)
    }
  }, [open, query, rows.length])

  // Focus moves into the menu so the arrow keys work, and back to the trigger
  // when it closes by keyboard or by a pick.
  useEffect(() => {
    if (!open) return
    const el = filterRef.current || listRef.current
    el?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    listRef.current?.querySelector('[data-active="true"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [open, active, rows])

  const pick = useCallback((row) => {
    if (!row || row.disabled) return
    if (onPick) onPick(row)
    else if (multiple) onChange?.(row.none ? [] : toggleValue(values, row.id))
    else onChange?.(row.id)
    if (multiple && !row.closes) { setQuery(''); return }
    close(true)
  }, [onPick, onChange, multiple, values, close])

  const onMenuKey = (e) => {
    const n = rows.length
    const next = moveActive(e.key, active, n, !!query)
    if (next !== null) { e.preventDefault(); setActive(next) }
    else if (e.key === 'Enter') { e.preventDefault(); if (active < n) pick(rows[active]) }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(true) }
  }

  // Tab closes the menu once the focus leaves it, so a button in the footer
  // can still take the focus. A blur to nothing (a click on the menu's own
  // padding) keeps it open; a click outside closes it through mousedown.
  const onMenuBlur = (e) => {
    const next = e.relatedTarget
    if (next && menuRef.current && !menuRef.current.contains(next)) close()
  }

  const current = !multiple && value != null && value !== '' ? items.find(i => i.id === value) : null
  const picked = multiple ? items.filter(i => values.includes(i.id)) : []
  const shownText = multiple
    ? (picked.length ? picked.map(i => i.label).join(', ') : placeholder)
    : (current ? current.label : (value != null && value !== '' ? String(value) : (noneRow && !placeholder ? noneRow.label : placeholder)))
  const isPlaceholder = multiple ? picked.length === 0 : !current && (value == null || value === '')
  const activeId = active < rows.length ? `${uid}-opt-${active}` : undefined
  const field = variant === 'field'
  const listName = label || ariaLabel

  return (
    <div className={`list-picker${field ? ' list-picker--field' : ''} ${className}`.trim()} ref={rootRef} style={style}>
      {before}
      <div className="list-picker__row">
        <button
          ref={triggerRef}
          id={id}
          type="button"
          className={`home-chip${field ? ' home-chip--field' : ''} ${triggerClassName}`.trim()}
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={listId}
          aria-label={ariaLabel}
          aria-labelledby={labelledBy}
          aria-describedby={[ariaLabel && !triggerContent && textId, describedBy].filter(Boolean).join(' ') || undefined}
          aria-invalid={invalid || undefined}
          disabled={disabled}
          data-testid={testId}
          onClick={() => (open ? close() : openMenu())}
          onKeyDown={(e) => { if (e.key === 'ArrowDown' && !open) { e.preventDefault(); openMenu() } }}
          {...triggerProps}
        >
          {triggerContent ?? (
            <>
              {icon && <Icon name={icon} />}
              {current?.dot != null && <span className={`home-dot${current.dot ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
              <span
                id={textId}
                className={`home-chip__text${current?.mono ? ' home-chip__text--mono' : ''}${isPlaceholder ? ' home-chip__text--placeholder' : ''}`}
              >
                {shownText}
              </span>
              {multiple && showCount && values.length > 0 && (
                <span className="home-chip__count" data-testid={testId ? `${testId}-count` : undefined}>{values.length}</span>
              )}
            </>
          )}
          <Icon name="chevron-down" className="home-chip__caret" />
        </button>
        {clearable && !multiple && value != null && value !== '' && (
          <button type="button" className="home-pick__remove" disabled={disabled}
            aria-label={clearLabel || t('picker.clear')} onClick={() => onChange?.('')}>
            <Icon name="close" />
          </button>
        )}
      </div>
      {open && (
        <div ref={menuRef} className={`home-menu ${menuClassName}`.trim()} id={menuId} onKeyDown={onMenuKey} onBlur={onMenuBlur} data-testid={testId ? `${testId}-menu` : undefined}>
          {showFilter && (
            <input
              ref={filterRef}
              type="search"
              className="home-menu__filter"
              placeholder={filterPlaceholder || filterLabel || t('picker.filter')}
              aria-label={filterLabel || filterPlaceholder || t('picker.filter')}
              value={query}
              onChange={(e) => { setQuery(e.target.value); setActive(0) }}
              role="combobox"
              aria-expanded="true"
              aria-controls={listId}
              aria-activedescendant={activeId}
            />
          )}
          <ul
            ref={listRef}
            id={listId}
            className="home-menu__list"
            role="listbox"
            aria-multiselectable={multiple || undefined}
            aria-label={listName}
            tabIndex={-1}
            aria-activedescendant={activeId}
          >
            {heading && <li className="home-menu__head" role="presentation">{heading}</li>}
            {loading && <li className="home-menu__empty" role="presentation">{loadingText || t('picker.loading')}</li>}
            {!loading && matched.length === 0 && (noneRow ? query.trim() !== '' : true) && (
              <li className="home-menu__empty" role="presentation">
                {(empty ?? items.length === 0) ? (emptyText || t('picker.empty')) : (noMatchText || t('picker.noMatch'))}
              </li>
            )}
            {rows.map((row, i) => {
              const selected = row.selected ?? isRowSelected(row, value, multiple)
              const head = groupHeading(rows, i, groups)
              return (
                <Fragment key={row.none ? `${uid}-${NONE_KEY}` : rowKey(row)}>
                  {head && <li className="home-menu__head" role="presentation">{head}</li>}
                  <li
                    id={`${uid}-opt-${i}`}
                    role="option"
                    aria-selected={selected}
                    aria-disabled={row.disabled || undefined}
                    data-active={i === active}
                    data-blocked={row.disabled || undefined}
                    className={`home-menu__item${row.rich || row.description ? ' home-menu__item--rich' : ''}`}
                    title={row.title}
                    onMouseMove={() => setActive(i)}
                    onClick={() => pick(row)}
                  >
                    {renderRow ? renderRow(row, { selected, active: i === active, index: i }) : <Row row={row} selected={selected} />}
                  </li>
                </Fragment>
              )
            })}
          </ul>
          {footer}
        </div>
      )}
    </div>
  )
})

// The default row: a dot, an icon, the label (mono when it names an id), tags,
// a second line, a secondary text at the end and the check of a chosen row.
// eslint-disable-next-line no-unused-vars
function Row({ row, selected }) {
  const name = row.mono ? <code>{row.label}</code> : <span className="home-menu__label">{row.label}</span>
  const tags = (row.tags || []).map((tag, i) => {
    const t = typeof tag === 'string' ? { label: tag } : tag
    const tone = t.kind === 'alias' ? 'accent' : t.kind === 'broken' ? 'error' : 'neutral'
    return <Tag key={`${t.label}-${i}`} tone={tone} title={t.title}>{t.label}</Tag>
  })
  return (
    <>
      {row.dot != null && <span className={`home-dot${row.dot ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
      {row.icon && <Icon name={row.icon} className="home-menu__icon" />}
      {row.description ? (
        <span className="home-menu__main">
          <span className="home-menu__line">{name}{tags}</span>
          <span className="home-menu__desc">{row.description}</span>
        </span>
      ) : <>{name}{tags}</>}
      {row.secondary != null && row.secondary !== '' && <small>{row.secondary}</small>}
      {selected && <Icon name="check" className="home-menu__check" />}
    </>
  )
}

export default ListPicker
