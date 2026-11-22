import { forwardRef, useCallback, useEffect, useId, useMemo, useRef, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { useModels } from '../hooks/useModels'
import { readLastModel, writeLastModel, readPreferredModel, readChatStart, subscribeChatStart } from '../utils/lastModel'
import { startPick } from '../utils/defaults'
import Icon from './Icon'
// eslint-disable-next-line no-unused-vars
import ListPicker from './ui/ListPicker'
import Tag from './ui/Tag'
import { FILTER_FROM } from './ui/listPickerModel'

// The one model picker: ListPicker with model rows. A model is "warm" when the
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
//   startsChat        the picker of a new chat: it starts as defaults.json says
//                     (startPick): with ask, or a default that is not installed,
//                     it chooses nothing and a note says why
//   models, loading   the list is already held by the caller, so no second fetch
//   grouped, loadedIds, showWarm, describe, onOpen, footer, labels
//   defaultChoice, defaultSelected, onChooseDefault   a first row for "use the default"
//   describe(name, warm) -> { size, vision, caps: [label], fit }   a richer row; caps are small tags
const ModelPicker = forwardRef(function ModelPicker(
  {
    value, onChange, capability, loadedIds, disabled = false, placeholder,
    models: givenModels, loading: givenLoading, grouped = false, describe, onOpen, footer,
    showWarm = true, labels, autoSelect = true, rememberSelection = true, startsChat = false,
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

  // A chooseable model is listed, enabled and, when it is an alias, resolved.
  const usable = useCallback((name) => {
    const m = byId.get(name)
    return !!m && !m.disabled && !m.alias_error
  }, [byId])
  const usableNames = useMemo(() => names.filter(usable), [names, usable])

  // The remembered model if it is still there, otherwise the first one.
  // Auto-select is not a user choice, so it is not written back. A new chat
  // starts as defaults.json says, which is read after the first render: a
  // model chosen here before it came is changed, one the person chose is not.
  const chatStart = useSyncExternalStore(subscribeChatStart, readChatStart)
  const autoPicked = useRef('')
  const plan = useMemo(() => (startsChat ? startPick(chatStart, usableNames, readLastModel(capability)) : null), [startsChat, chatStart, usableNames, capability])
  useEffect(() => {
    if (multiple || allowCustom || !autoSelect || defaultSelected || usableNames.length === 0) return
    const auto = !value || value === autoPicked.current || !usableNames.includes(value)
    if (plan) {
      if (!auto || value === plan.model) return
      autoPicked.current = plan.model
      onChange(plan.model)
      return
    }
    if (value && usableNames.includes(value)) return
    const preferred = readPreferredModel(capability)
    const remembered = preferred && usableNames.includes(preferred) ? preferred : readLastModel(capability)
    onChange(remembered && usableNames.includes(remembered) ? remembered : usableNames[0])
  }, [usableNames, value, onChange, capability, autoSelect, defaultSelected, multiple, allowCustom, plan])
  const startNote = plan && autoSelect && !value && plan.note
    ? (plan.note === 'missing' ? t('picker.defaultMissing', { model: plan.missing }) : t('picker.chooseModel'))
    : ''

  const showFilter = (searchable ?? names.length >= FILTER_FROM) || allowCustom
  const isWarm = useCallback((name) => !!loadedIds?.has(name), [loadedIds])

  // The rows for a query. Loaded models first when the list is grouped; each
  // group keeps its order. A model row has `name`, the typed name has
  // `custom`, and the "use the default" row has `name: null`.
  const rowsFor = (_items, raw) => {
    const q = raw.trim().toLowerCase()
    const matches = (n) => {
      if (!q) return true
      const m = byId.get(n)
      return n.toLowerCase().includes(q) || (m?.alias_of || '').toLowerCase().includes(q)
    }
    const found = names.filter(matches)
    const ordered = grouped ? [...found.filter(n => isWarm(n)), ...found.filter(n => !isWarm(n))] : found
    const rows = ordered.map((name) => {
      const warm = isWarm(name)
      const info = describe ? describe(name, warm) : null
      return {
        key: name, id: name, name, warm, info, rich: !!info,
        group: grouped ? (warm ? 'loaded' : 'installed') : undefined,
        disabled: !usable(name),
        selected: multiple ? picked.includes(name) : !defaultSelected && name === value,
      }
    })
    const typed = raw.trim()
    if (allowCustom && typed && !names.includes(typed)) rows.push({ key: `${instanceId}-custom`, id: typed, custom: typed, selected: false })
    return defaultChoice && (!q || defaultChoice.toLowerCase().includes(q))
      ? [{ key: `${instanceId}-default`, id: null, name: null, selected: defaultSelected, closes: true }, ...rows]
      : rows
  }

  const pick = (row) => {
    if (row.name === null) { onChooseDefault?.(); return }
    const name = row.custom ?? row.name
    if (rememberSelection && !multiple) writeLastModel(capability, name)
    if (multiple) onChange(picked.includes(name) ? picked.filter(n => n !== name) : [...picked, name])
    else onChange(name)
  }

  const current = multiple ? '' : (defaultSelected ? defaultChoice : value)
  const emptyLabel = multiple ? (placeholder || t('picker.add')) : (placeholder || (loading ? t('picker.loading') : (labels?.none || t('picker.none'))))
  const label = current || emptyLabel
  const selectionId = labels?.label ? `${instanceId}-selection` : undefined

  const badges = (name) => {
    const m = byId.get(name)
    if (!m) return null
    return (
      <>
        {m.alias_of && !m.alias_error && <Tag tone="accent">{t('picker.aliasOf', { target: m.alias_of })}</Tag>}
        {m.alias_error && <Tag tone="error" title={m.alias_error}>{t('picker.aliasBroken')}</Tag>}
        {m.disabled && <Tag>{t('picker.disabled')}</Tag>}
        {m.pinned && <Tag>{t('picker.pinned')}</Tag>}
      </>
    )
  }

  const renderRow = (row, { selected: chosen }) => {
    const { name, custom, warm, info } = row
    const blocked = !!row.disabled
    return (
      <>
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
                  {(info.caps || []).map(c => <Tag key={c}>{c}</Tag>)}
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
      </>
    )
  }

  return (
    <ListPicker
      ref={ref}
      id={id}
      variant={variant}
      className={`home-pick${variant === 'field' ? ' home-pick--field' : ''} ${className}`.trim()}
      triggerClassName="home-chip--model"
      menuClassName="home-menu--models"
      items={models}
      filterItems={rowsFor}
      value={value}
      multiple={multiple}
      onPick={pick}
      onOpen={onOpen}
      disabled={disabled}
      openable={names.length > 0 || !!defaultChoice || allowCustom}
      filter={showFilter}
      filterLabel={t('picker.filter')}
      filterPlaceholder={t('picker.filter')}
      label={labels?.label || t('picker.label')}
      heading={!grouped && (labels?.heading || capability === 'FLAG_CHAT') ? (labels?.heading || t('picker.heading')) : null}
      groups={{ loaded: t('picker.loadedNow'), installed: t('picker.installed') }}
      loading={loading}
      loadingText={t('picker.loading')}
      empty={names.length === 0}
      emptyText={t('picker.empty')}
      noMatchText={t('picker.noMatch')}
      clearable={clearable}
      clearLabel={t('picker.clear')}
      showCount={false}
      renderRow={renderRow}
      footer={footer}
      before={multiple && picked.length > 0 && (
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
      triggerProps={{
        'aria-label': labels?.label,
        'aria-describedby': [selectionId, describedBy].filter(Boolean).join(' ') || undefined,
        title: startNote || labels?.title || t('picker.title'),
        'data-note': startNote ? plan.note : undefined,
        'data-testid': 'home-model-chip',
      }}
      triggerContent={(
        <>
          <Icon name="cube" />
          {showWarm && !multiple && !defaultSelected && <span className={`home-dot${value && isWarm(value) ? '' : ' home-dot--cold'}`} aria-hidden="true" />}
          {startNote
            ? <span id={selectionId} className="home-chip__text home-chip__note" data-testid="model-picker-note">{startNote}</span>
            : <span id={selectionId} className="home-chip__text">{label}</span>}
          {!multiple && current && byId.get(current)?.alias_of && !byId.get(current)?.alias_error && (
            <small className="home-chip__alias">{t('picker.aliasOf', { target: byId.get(current).alias_of })}</small>
          )}
        </>
      )}
    />
  )
})

export default ModelPicker
