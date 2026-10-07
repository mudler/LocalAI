// eslint-disable-next-line no-unused-vars
import { Fragment, forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useModels } from '../../hooks/useModels'
import { readLastModel, writeLastModel } from '../../utils/lastModel'
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
const HomeModelPicker = forwardRef(function HomeModelPicker(
  {
    value, onChange, capability, loadedIds, disabled = false, placeholder,
    models: givenModels, loading: givenLoading, grouped = false, describe, onOpen, footer,
  },
  ref,
) {
  const { t } = useTranslation('home')
  const own = useModels(capability, { enabled: !givenModels })
  const models = givenModels || own.models
  const loading = givenModels ? !!givenLoading : own.loading
  const names = useMemo(() => models.map(m => m.id), [models])
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
    if (names.length > 0 && (!value || !names.includes(value))) {
      const remembered = readLastModel(capability)
      onChange(remembered && names.includes(remembered) ? remembered : names[0])
    }
  }, [names, value, onChange, capability])

  // Loaded models first when the list is grouped; each group keeps its order.
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    const found = q ? names.filter(n => n.toLowerCase().includes(q)) : names
    if (!grouped) return found
    return [...found.filter(n => loadedIds?.has(n)), ...found.filter(n => !loadedIds?.has(n))]
  }, [names, query, grouped, loadedIds])

  const close = useCallback((restoreFocus = false) => {
    setOpen(false)
    setQuery('')
    if (restoreFocus) chipRef.current?.focus()
  }, [])

  const openMenu = useCallback(() => {
    if (disabled || names.length === 0) return
    setActive(Math.max(0, names.indexOf(value)))
    setQuery('')
    setOpen(true)
    onOpen?.()
  }, [disabled, names, value, onOpen])

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
    writeLastModel(capability, name)
    onChange(name)
    close(true)
  }, [capability, onChange, close])

  const onMenuKey = (e) => {
    const n = shown.length
    if (e.key === 'ArrowDown') { e.preventDefault(); if (n) setActive(a => (a + 1) % n) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); if (n) setActive(a => (a + n - 1) % n) }
    else if (e.key === 'Home' && !query) { e.preventDefault(); setActive(0) }
    else if (e.key === 'End' && !query) { e.preventDefault(); setActive(Math.max(0, n - 1)) }
    else if (e.key === 'Enter') { e.preventDefault(); if (shown[active]) pick(shown[active]) }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(true) }
    else if (e.key === 'Tab') close()
  }

  const isWarm = (name) => loadedIds?.has(name)
  const label = value || placeholder || (loading ? t('picker.loading') : t('picker.none'))
  const activeId = shown[active] ? `home-model-opt-${active}` : undefined

  return (
    <div className="home-pick" ref={rootRef}>
      <button
        ref={chipRef}
        type="button"
        className="home-chip home-chip--model"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls="home-model-menu"
        disabled={disabled}
        title={t('picker.title')}
        data-testid="home-model-chip"
        onClick={() => (open ? close() : openMenu())}
        onKeyDown={(e) => { if (e.key === 'ArrowDown' && !open) { e.preventDefault(); openMenu() } }}
      >
        <Icon name="cube" />
        <span className={`home-dot${value && isWarm(value) ? '' : ' home-dot--cold'}`} aria-hidden="true" />
        <span className="home-chip__text">{label}</span>
        <Icon name="chevron-down" className="home-chip__caret" />
      </button>
      {open && (
        <div className="home-menu home-menu--models" id="home-model-menu" onKeyDown={onMenuKey}>
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
              aria-controls="home-model-list"
              aria-activedescendant={activeId}
            />
          )}
          <ul
            ref={menuRef}
            id="home-model-list"
            className="home-menu__list"
            role="listbox"
            aria-label={t('picker.label')}
            tabIndex={-1}
            aria-activedescendant={activeId}
          >
            {!grouped && <li className="home-menu__head" role="presentation">{t('picker.heading')}</li>}
            {shown.length === 0 && <li className="home-menu__empty" role="presentation">{t('picker.noMatch')}</li>}
            {shown.map((name, i) => {
              const warm = isWarm(name)
              const info = describe ? describe(name, warm) : null
              const first = grouped && (i === 0 || isWarm(shown[i - 1]) !== warm)
              return (
                <Fragment key={name}>
                  {first && (
                    <li className="home-menu__head" role="presentation">
                      {warm ? t('picker.loadedNow') : t('picker.installed')}
                    </li>
                  )}
                  <li
                    id={`home-model-opt-${i}`}
                    role="option"
                    aria-selected={name === value}
                    data-active={i === active}
                    className={`home-menu__item${info ? ' home-menu__item--rich' : ''}`}
                    onMouseMove={() => setActive(i)}
                    onClick={() => pick(name)}
                  >
                    <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />
                    {info ? (
                      <>
                        <span className="home-menu__main">
                          <code>{name}</code>
                          {(info.size || info.vision) && (
                            <small className="home-menu__caps">
                              {info.size && <span>{info.size}</span>}
                              {info.vision && <Icon name="eye" title={t('picker.vision')} />}
                            </small>
                          )}
                        </span>
                        <span className="home-menu__state" data-tone={info.fit?.tone}>
                          <b>{name === value ? <Icon name="check" className="home-menu__check" /> : null}{warm ? t('picker.warm') : t('picker.cold')}</b>
                          {info.fit && <small>{info.fit.text}</small>}
                        </span>
                      </>
                    ) : (
                      <>
                        <code>{name}</code>
                        {name === value
                          ? <Icon name="check" className="home-menu__check" />
                          : <small>{warm ? t('picker.warm') : t('picker.cold')}</small>}
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
