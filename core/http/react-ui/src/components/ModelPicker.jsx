// eslint-disable-next-line no-unused-vars
import { Fragment, forwardRef, useCallback, useEffect, useImperativeHandle, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useModels } from '../hooks/useModels'
import { readLastModel, writeLastModel, readPreferredModel } from '../utils/lastModel'
import Icon from './Icon'
import './home/composer.css'

// How many models before the list gets a filter box, when `searchable` is left
// to decide.
const FILTER_FROM = 8

// The one model picker: a trigger and a listbox. A model is "warm" when the
// server reports it loaded, "cold" otherwise. Every entry the server lists
// shows what it is: an alias says which model it stands for, a disabled model
// cannot be chosen, a pinned one is marked, and an alias that points nowhere is
// listed as broken and cannot be chosen.
//
//   value, onChange   a name; with `multiple`, an array of names
//   capability        only models that have it (also read to remember a choice)
//   multiple          pick several; the list stays open and the picks show as chips
//   allowCustom       a typed name that is not in the list can be used as it is
//   clearable         a button empties the value
//   searchable        true: always a filter box; false: never; omitted: from 8 models
//   variant           "chip" (default, the compact Home chip) or "field" (a form control)
//   autoSelect        choose the remembered or the first model when the value is not listed
//   models, loading   the list is already held by the caller, so no second fetch
//   grouped, loadedIds, showWarm, describe, onOpen, footer, labels
//   defaultChoice, defaultSelected, onChooseDefault   a first row for "use the default"
//   describe(name, warm) -> { size, vision, caps: [label], fit }   a richer row; caps are small tags
const ModelPicker = forwardRef(function ModelPicker(
  {
    value, onChange, capability, loadedIds, disabled = false, placeholder,
    models: givenModels, loading: givenLoading, grouped = false, describe, onOpen, footer,
    showWarm = true, labels, autoSelect = true, rememberSelection = true,
    defaultChoice, defaultSelected = false, onChooseDefault, id, describedBy,
    multiple = false, allowCustom = false, clearable = false, searchable,
    variant = 'chip', className = '',
  },
  ref,
) {
  const { t } = useTranslation('home')
  const own = useModels(capability, { enabled: !givenModels, keepBroken: true })
  const models = givenModels || own.models
  const loading = givenModels ? !!givenLoading : own.loading
  const byId = useMemo(() => new Map(models.map(m => [m.id, m])), [models])
  const names = useMemo(() => models.map(m => m.id), [models])
  const picked = useMemo(() => (multiple ? (Array.isArray(value) ? value : []) : []), [multiple, value])
  const instanceId = useId()
  const menuId = `${instanceId}-menu`
  const listId = `${instanceId}-list`
  // A null row is a non-model action, never an installed model ID.
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const rootRef = useRef(null)
  const chipRef = useRef(null)
  const menuRef = useRef(null)
  const filterRef = useRef(null)

  // A chooseable model is listed, enabled and, when it is an alias, resolved.
  const usable = useCallback((name) => {
    const m = byId.get(name)
    return !!m && !m.disabled && !m.alias_error
  }, [byId])
  const usableNames = useMemo(() => names.filter(usable), [names, usable])

  // The remembered model if it is still there, otherwise the first one.
  // Auto-select is not a user choice, so it is not written back.
  useEffect(() => {
    if (multiple || allowCustom || !autoSelect || defaultSelected || usableNames.length === 0) return
    if (value && usableNames.includes(value)) return
    const preferred = readPreferredModel(capability)
    const remembered = preferred && usableNames.includes(preferred) ? preferred : readLastModel(capability)
    onChange(remembered && usableNames.includes(remembered) ? remembered : usableNames[0])
  }, [usableNames, value, onChange, capability, autoSelect, defaultSelected, multiple, allowCustom])

  const showFilter = searchable ?? names.length >= FILTER_FROM

  // Loaded models first when the list is grouped; each group keeps its order.
  // Rows are { name } for a model and { custom } for the typed name.
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    const matches = (n) => {
      if (!q) return true
      const m = byId.get(n)
      return n.toLowerCase().includes(q) || (m?.alias_of || '').toLowerCase().includes(q)
    }
    const found = names.filter(matches)
    const ordered = grouped ? [...found.filter(n => loadedIds?.has(n)), ...found.filter(n => !loadedIds?.has(n))] : found
    const rows = ordered.map(name => ({ name }))
    const typed = query.trim()
    if (allowCustom && typed && !names.includes(typed)) rows.push({ custom: typed })
    return defaultChoice && (!q || defaultChoice.toLowerCase().includes(q)) ? [{ name: null }, ...rows] : rows
  }, [names, byId, query, grouped, loadedIds, defaultChoice, allowCustom])

  const close = useCallback((restoreFocus = false) => {
    setOpen(false)
    setQuery('')
    if (restoreFocus) chipRef.current?.focus()
  }, [])

  const openMenu = useCallback(() => {
    if (disabled || (names.length === 0 && !defaultChoice && !allowCustom)) return
    setActive(defaultSelected || multiple ? 0 : Math.max(0, names.indexOf(value) + (defaultChoice ? 1 : 0)))
    setQuery('')
    setOpen(true)
    onOpen?.()
  }, [disabled, names, value, onOpen, defaultChoice, defaultSelected, multiple, allowCustom])

  useImperativeHandle(ref, () => ({ open: openMenu, focus: () => chipRef.current?.focus() }), [openMenu])

  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (rootRef.current && !rootRef.current.contains(e.target)) close() }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open, close])

  // Focus moves into the menu so the arrow keys work, and back to the chip when
  // it closes by keyboard.
  useEffect(() => {
    if (!open) return
    const el = filterRef.current || menuRef.current
    el?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    menuRef.current?.querySelector('[data-active="true"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [open, active, shown])

  const pick = useCallback((row) => {
    if (!row) return
    if (row.name === null) { onChooseDefault?.(); close(true); return }
    const name = row.custom ?? row.name
    if (row.custom === undefined && !usable(name)) return
    if (rememberSelection && !multiple) writeLastModel(capability, name)
    if (multiple) {
      onChange(picked.includes(name) ? picked.filter(n => n !== name) : [...picked, name])
      setQuery('')
      return
    }
    onChange(name)
    close(true)
  }, [capability, onChange, close, rememberSelection, onChooseDefault, usable, multiple, picked])

  const onMenuKey = (e) => {
    const n = shown.length
    if (e.key === 'ArrowDown') { e.preventDefault(); if (n) setActive(a => (a + 1) % n) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); if (n) setActive(a => (a + n - 1) % n) }
    else if (e.key === 'Home' && !query) { e.preventDefault(); setActive(0) }
    else if (e.key === 'End' && !query) { e.preventDefault(); setActive(Math.max(0, n - 1)) }
    else if (e.key === 'Enter') { e.preventDefault(); if (active < n) pick(shown[active]) }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(true) }
    else if (e.key === 'Tab') close()
  }

  const isWarm = (name) => loadedIds?.has(name)
  const current = multiple ? '' : (defaultSelected ? defaultChoice : value)
  const emptyLabel = multiple ? (placeholder || t('picker.add')) : (placeholder || (loading ? t('picker.loading') : (labels?.none || t('picker.none'))))
  const label = current || emptyLabel
  const activeId = active < shown.length ? `${instanceId}-opt-${active}` : undefined
  const trigger = variant === 'field' ? 'home-chip home-chip--field' : 'home-chip'

  const badges = (name) => {
    const m = byId.get(name)
    if (!m) return null
    return (
      <>
        {m.alias_of && !m.alias_error && <span className="home-menu__tag" data-kind="alias">{t('picker.aliasOf', { target: m.alias_of })}</span>}
        {m.alias_error && <span className="home-menu__tag" data-kind="broken" title={m.alias_error}>{t('picker.aliasBroken')}</span>}
        {m.disabled && <span className="home-menu__tag" data-kind="disabled">{t('picker.disabled')}</span>}
        {m.pinned && <span className="home-menu__tag" data-kind="pinned">{t('picker.pinned')}</span>}
      </>
    )
  }

  return (
    <div className={`home-pick${variant === 'field' ? ' home-pick--field' : ''} ${className}`.trim()} ref={rootRef}>
      {multiple && picked.length > 0 && (
        <ul className="home-pick__chips" aria-label={labels?.label || t('picker.label')}>
          {picked.map(name => (
            <li key={name} className="home-pick__tag">
              <code>{name}</code>
              <button type="button" className="home-pick__remove" disabled={disabled}
                aria-label={t('picker.remove', { name })}
                onClick={() => onChange(picked.filter(n => n !== name))}>
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="home-pick__row">
        <button
          ref={chipRef}
          id={id}
          aria-label={labels?.label}
          aria-describedby={[labels?.label && `${instanceId}-selection`, describedBy].filter(Boolean).join(' ') || undefined}
          type="button"
          className={`${trigger} home-chip--model`}
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={listId}
          disabled={disabled}
          title={labels?.title || t('picker.title')}
          data-testid="home-model-chip"
          onClick={() => (open ? close() : openMenu())}
          onKeyDown={(e) => { if (e.key === 'ArrowDown' && !open) { e.preventDefault(); openMenu() } }}
        >
          <Icon name="cube" />
          {showWarm && !multiple && !defaultSelected && <span className={`home-dot${value && isWarm(value) ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
          <span id={labels?.label ? `${instanceId}-selection` : undefined} className="home-chip__text">{label}</span>
          {!multiple && current && byId.get(current)?.alias_of && !byId.get(current)?.alias_error && (
            <small className="home-chip__alias">{t('picker.aliasOf', { target: byId.get(current).alias_of })}</small>
          )}
          <Icon name="chevron-down" className="home-chip__caret" />
        </button>
        {clearable && !multiple && value && (
          <button type="button" className="home-pick__remove" disabled={disabled}
            aria-label={t('picker.clear')} onClick={() => onChange('')}>
            <Icon name="close" />
          </button>
        )}
      </div>
      {open && (
        <div className="home-menu home-menu--models" id={menuId} onKeyDown={onMenuKey}>
          {(showFilter || allowCustom) && (
            <input
              ref={filterRef}
              type="search"
              className="home-menu__filter"
              placeholder={t('picker.filter')}
              aria-label={t('picker.filter')}
              value={query}
              onChange={(e) => { setQuery(e.target.value); setActive(0) }}
              role="combobox"
              aria-expanded="true"
              aria-controls={listId}
              aria-activedescendant={activeId}
            />
          )}
          <ul
            ref={menuRef}
            id={listId}
            className="home-menu__list"
            role="listbox"
            aria-multiselectable={multiple || undefined}
            aria-label={labels?.label || t('picker.label')}
            tabIndex={-1}
            aria-activedescendant={activeId}
          >
            {!grouped && (labels?.heading || capability === 'FLAG_CHAT') && <li className="home-menu__head" role="presentation">{labels?.heading || t('picker.heading')}</li>}
            {loading && <li className="home-menu__empty" role="presentation">{t('picker.loading')}</li>}
            {!loading && shown.length === 0 && (
              <li className="home-menu__empty" role="presentation">{names.length === 0 ? t('picker.empty') : t('picker.noMatch')}</li>
            )}
            {shown.map((row, i) => {
              const { name, custom } = row
              const warm = name != null && isWarm(name)
              const info = name != null && describe ? describe(name, warm) : null
              const chosen = custom !== undefined ? false
                : name === null ? defaultSelected
                  : multiple ? picked.includes(name) : !defaultSelected && name === value
              const blocked = name != null && !usable(name)
              const prev = shown[i - 1]
              const first = grouped && name != null && (i === 0 || prev?.name == null || isWarm(prev.name) !== warm)
              return (
                <Fragment key={custom !== undefined ? `${instanceId}-custom` : name === null ? `${instanceId}-default` : name}>
                  {first && (
                    <li className="home-menu__head" role="presentation">
                      {warm ? t('picker.loadedNow') : t('picker.installed')}
                    </li>
                  )}
                  <li
                    id={`${instanceId}-opt-${i}`}
                    role="option"
                    aria-selected={chosen}
                    aria-disabled={blocked || undefined}
                    data-active={i === active}
                    data-blocked={blocked || undefined}
                    className={`home-menu__item${info ? ' home-menu__item--rich' : ''}`}
                    onMouseMove={() => setActive(i)}
                    onClick={() => pick(row)}
                  >
                    {showWarm && name != null && <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
                    {custom !== undefined ? (
                      <code>{t('picker.useTyped', { name: custom })}</code>
                    ) : info ? (
                      <>
                        <span className="home-menu__main">
                          <code>{name === null ? defaultChoice : name}</code>
                          {badges(name)}
                          {(info.size || info.vision || info.caps?.length > 0) && (
                            <small className="home-menu__caps">
                              {info.size && <span>{info.size}</span>}
                              {info.vision && <Icon name="eye" title={t('picker.vision')} />}
                              {(info.caps || []).map(c => <span key={c} className="home-menu__tag" data-kind="cap">{c}</span>)}
                            </small>
                          )}
                        </span>
                        <span className="home-menu__state" data-tone={info.fit?.tone}>
                          <b>{chosen ? <Icon name="check" className="home-menu__check" /> : null}{warm ? t('picker.warm') : t('picker.cold')}</b>
                          {info.fit && <small>{info.fit.text}</small>}
                        </span>
                      </>
                    ) : (
                      <>
                        <code>{name === null ? defaultChoice : name}</code>
                        {name != null && badges(name)}
                        {chosen
                          ? <Icon name="check" className="home-menu__check" />
                          : (showWarm && name != null && !blocked && <small>{warm ? t('picker.warm') : t('picker.cold')}</small>)}
                      </>
                    )}
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

export default ModelPicker
