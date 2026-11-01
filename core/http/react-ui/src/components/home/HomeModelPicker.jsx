// eslint-disable-next-line no-unused-vars
import { Fragment, forwardRef, useCallback, useEffect, useImperativeHandle, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useModels } from '../../hooks/useModels'
import { readLastModel, writeLastModel, readPreferredModel } from '../../utils/lastModel'
import Icon from '../Icon'

// How many models before the list gets a filter box.
const FILTER_FROM = 8

// The model chip on the Home command bar and its listbox. A model is "warm"
// when the server reports it loaded (the same /system list the status strip
// reads), "cold" otherwise. Nothing about load time is shown: the API does not
// report it for an installed model.
//
// Chat uses the same chip and adds, all optional:
//   models, loading   the page already holds the list, so no second fetch
//   grouped           "Loaded now" and "Installed" instead of one list
//   describe(name)    { vision, size, fit: { tone, text } } for a row
//   onOpen            called when the list opens (to read memory and sizes)
//   footer            a node under the list (the memory bar)
//   showWarm          false for a list whose names are not loadable models (Talk's pipelines)
//   labels            { title, heading, label, none } to replace the chat wording
const HomeModelPicker = forwardRef(function HomeModelPicker(
  {
    value, onChange, capability, loadedIds, disabled = false, placeholder,
    models: givenModels, loading: givenLoading, grouped = false, describe, onOpen, footer,
    showWarm = true, labels, autoSelect = true, rememberSelection = true,
    defaultChoice, defaultSelected = false, onChooseDefault, id, describedBy,

  },
  ref,
) {
  const { t } = useTranslation('home')
  const own = useModels(capability, { enabled: !givenModels })
  const models = givenModels || own.models
  const loading = givenModels ? !!givenLoading : own.loading
  const names = useMemo(() => models.map(m => m.id), [models])
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

  // Same defaulting as ModelSelector: the remembered model if it is still
  // there, otherwise the first one. Auto-select is not a user choice, so it is
  // not written back.
  useEffect(() => {
    if (autoSelect && !defaultSelected && names.length > 0 && (!value || !names.includes(value))) {
      const preferred = readPreferredModel(capability)
      const remembered = preferred && names.includes(preferred) ? preferred : readLastModel(capability)
      onChange(remembered && names.includes(remembered) ? remembered : names[0])
    }
  }, [names, value, onChange, capability, autoSelect, defaultSelected])

  // Loaded models first when the list is grouped; each group keeps its order.
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    const found = q ? names.filter(n => n.toLowerCase().includes(q)) : names
    const ordered = grouped ? [...found.filter(n => loadedIds?.has(n)), ...found.filter(n => !loadedIds?.has(n))] : found
    return defaultChoice && (!q || defaultChoice.toLowerCase().includes(q)) ? [null, ...ordered] : ordered
  }, [names, query, grouped, loadedIds, defaultChoice])

  const close = useCallback((restoreFocus = false) => {
    setOpen(false)
    setQuery('')
    if (restoreFocus) chipRef.current?.focus()
  }, [])

  const openMenu = useCallback(() => {
    if (disabled || (names.length === 0 && !defaultChoice)) return
    setActive(defaultSelected ? 0 : Math.max(0, names.indexOf(value) + (defaultChoice ? 1 : 0)))
    setQuery('')
    setOpen(true)
    onOpen?.()
  }, [disabled, names, value, onOpen, defaultChoice, defaultSelected])

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

  const pick = useCallback((name) => {
    if (name === null) onChooseDefault?.()
    else {
      if (rememberSelection) writeLastModel(capability, name)
      onChange(name)
    }
    close(true)
  }, [capability, onChange, close, rememberSelection, onChooseDefault])

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
  const label = (defaultSelected ? defaultChoice : value) || placeholder || (loading ? t('picker.loading') : (labels?.none || t('picker.none')))
  const activeId = active < shown.length ? `${instanceId}-opt-${active}` : undefined

  return (
    <div className="home-pick" ref={rootRef}>
      <button
        ref={chipRef}
        id={id}
        aria-label={labels?.label}
        aria-describedby={[labels?.label && `${instanceId}-selection`, describedBy].filter(Boolean).join(' ') || undefined}
        type="button"
        className="home-chip home-chip--model"
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
        {showWarm && !defaultSelected && <span className={`home-dot${value && isWarm(value) ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
        <span id={labels?.label ? `${instanceId}-selection` : undefined} className="home-chip__text">{label}</span>
        <Icon name="chevron-down" className="home-chip__caret" />
      </button>
      {open && (
        <div className="home-menu home-menu--models" id={menuId} onKeyDown={onMenuKey}>
          {names.length >= FILTER_FROM && (
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
            aria-label={labels?.label || t('picker.label')}
            tabIndex={-1}
            aria-activedescendant={activeId}
          >
            {!grouped && <li className="home-menu__head" role="presentation">{labels?.heading || t('picker.heading')}</li>}
            {shown.length === 0 && <li className="home-menu__empty" role="presentation">{t('picker.noMatch')}</li>}
            {shown.map((name, i) => {
              const warm = isWarm(name)
              const info = name !== null && describe ? describe(name, warm) : null
              const selected = name === null ? defaultSelected : !defaultSelected && name === value
              const first = grouped && (i === 0 || isWarm(shown[i - 1]) !== warm)
              return (
                <Fragment key={name === null ? `${instanceId}-default` : name}>
                  {first && (
                    <li className="home-menu__head" role="presentation">
                      {warm ? t('picker.loadedNow') : t('picker.installed')}
                    </li>
                  )}
                  <li
                    id={`${instanceId}-opt-${i}`}
                    role="option"
                    aria-selected={selected}
                    data-active={i === active}
                    className={`home-menu__item${info ? ' home-menu__item--rich' : ''}`}
                    onMouseMove={() => setActive(i)}
                    onClick={() => pick(name)}
                  >
                    {showWarm && name !== null && <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
                    {info ? (
                      <>
                        <span className="home-menu__main">
                          <code>{name === null ? defaultChoice : name}</code>
                          {(info.size || info.vision) && (
                            <small className="home-menu__caps">
                              {info.size && <span>{info.size}</span>}
                              {info.vision && <Icon name="eye" title={t('picker.vision')} />}
                            </small>
                          )}
                        </span>
                        <span className="home-menu__state" data-tone={info.fit?.tone}>
                          <b>{selected ? <Icon name="check" className="home-menu__check" /> : null}{warm ? t('picker.warm') : t('picker.cold')}</b>
                          {info.fit && <small>{info.fit.text}</small>}
                        </span>
                      </>
                    ) : (
                      <>
                        <code>{name === null ? defaultChoice : name}</code>
                        {selected
                          ? <Icon name="check" className="home-menu__check" />
                          : (showWarm && name !== null && <small>{warm ? t('picker.warm') : t('picker.cold')}</small>)}
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

export default HomeModelPicker
