import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Segmented, Slider } from '../../ui'
import Icon from '../../Icon'
import { readItem, validate } from '../../../utils/engineSettings'
import { choiceWords, fieldLabel } from './labels'

const shown = (v) => (v === undefined || v === null ? '' : String(v))

// The "Default" choice of a setting, with the server's default when it is a
// value ("auto", "f16", "1"). A default the server describes in words
// ("backend default") is English prose and is left to the hint.
function defaultLabel(t, item) {
  const d = item.default
  if (d === undefined || d === null || d === '') return t('engineSettings.default')
  if (d === true || d === 'true') return t('engineSettings.defaultIs', { value: t('engineSettings.on') })
  if (d === false || d === 'false') return t('engineSettings.defaultIs', { value: t('engineSettings.off') })
  if (/\s/.test(String(d))) return t('engineSettings.default')
  return t('engineSettings.defaultIs', { value: choiceWords(t, String(d)) })
}

// One engine setting: a label, the control its type asks for, the
// command-line flag and the server's description. Typed text that does not
// validate stays in the box, is marked, and is not written.
//
//   item        a setting of the schema (utils/engineSettings normalizeSchema)
//   values      the editor's flat values
//   onWrite     (item, value) => void; undefined unsets the setting
//   onValidity  (id, ok) => void
export default function RuntimeField({ item, values, onWrite, onValidity, readOnly }) {
  const { t } = useTranslation('modelEditor')
  const value = readItem(values, item)
  const id = `rt-${item.id.replace(/\./g, '-')}`
  const label = fieldLabel(t, item)

  const [text, setText] = useState(shown(value))
  const [error, setError] = useState(null)
  const editing = useRef(false)
  useEffect(() => {
    if (!editing.current) { setText(shown(value)); setError(null) }
  }, [value])
  useEffect(() => { onValidity?.(item.id, !error) }, [error, item.id, onValidity])
  useEffect(() => () => onValidity?.(item.id, true), [item.id, onValidity])

  const type = (next) => {
    editing.current = true
    setText(next)
    const got = validate(item, next)
    if (got.ok) {
      setError(null)
      onWrite(item, got.value)
    } else {
      setError(t(`engineSettings.errors.${got.error}`, got.params || {}))
    }
  }
  const settle = () => {
    editing.current = false
    if (!error) setText(shown(readItem(values, item)))
  }

  const hintId = `${id}-hint`
  const errorId = `${id}-error`
  const hint = (
    <p className="dk-hint rt-field__hint" id={hintId}>
      {item.flag && <code className="rt-flag" data-testid={`${id}-flag`}>{item.flag}</code>}
      {item.doc && <span>{item.doc}</span>}
    </p>
  )
  const describedBy = error ? `${hintId} ${errorId}` : hintId

  let control
  if (item.type === 'bool' || (item.type === 'enum' && item.enum.length <= 3)) {
    const options = item.type === 'bool'
      ? [{ id: '', label: defaultLabel(t, item) }, { id: 'true', label: t('engineSettings.on') }, { id: 'false', label: t('engineSettings.off') }]
      : [{ id: '', label: defaultLabel(t, item) }, ...item.enum.map(v => ({ id: v, label: choiceWords(t, v) }))]
    const current = value === undefined ? '' : String(value)
    control = (
      <Segmented
        mode="radio"
        labelledBy={`${id}-label`}
        value={current}
        options={options.map(o => ({ ...o, disabled: readOnly }))}
        onChange={next => {
          if (readOnly) return
          onWrite(item, next === '' ? undefined : item.type === 'bool' ? next === 'true' : next)
        }}
        itemTestId={`${id}-`}
        testId={id}
      />
    )
  } else if (item.type === 'enum') {
    control = (
      <span className="dk-select-wrap">
        <select
          id={id}
          className="dk-select"
          value={value === undefined ? '' : String(value)}
          disabled={readOnly}
          aria-describedby={hintId}
          onChange={e => onWrite(item, e.target.value === '' ? undefined : e.target.value)}
          data-testid={id}
        >
          <option value="">{defaultLabel(t, item)}</option>
          {item.enum.map(v => <option key={v} value={v}>{v}</option>)}
        </select>
      </span>
    )
  } else if (item.type === 'float' && item.min !== undefined && item.max !== undefined) {
    control = (
      <Slider
        id={id}
        range={{ min: item.min, max: item.max, step: item.max - item.min > 1 ? 0.05 : 0.01 }}
        value={text}
        onChange={next => { type(next); editing.current = false }}
        label={label}
        placeholder={defaultLabel(t, item)}
        resetLabel={t('engineSettings.reset', { name: label })}
        invalid={Boolean(error)}
        describedBy={describedBy}
        disabled={readOnly}
        testId={id}
      />
    )
  } else {
    control = (
      <input
        id={id}
        className={`dk-input${item.type === 'string' ? ' dk-input--mono' : ''} rt-field__input`}
        type="text"
        inputMode={item.type === 'int' ? 'numeric' : item.type === 'float' ? 'decimal' : undefined}
        value={text}
        placeholder={defaultLabel(t, item)}
        readOnly={readOnly}
        aria-invalid={error ? 'true' : undefined}
        aria-describedby={describedBy}
        onChange={e => type(e.target.value)}
        onBlur={settle}
        data-testid={id}
      />
    )
  }

  // A radio group is named by aria-labelledby; a label element would point at nothing.
  const grouped = item.type === 'bool' || (item.type === 'enum' && item.enum.length <= 3)
  return (
    <div className="dk-field rt-field" data-testid={`${id}-field`}>
      {grouped
        ? <span className="dk-label" id={`${id}-label`}>{label}</span>
        : <label className="dk-label" id={`${id}-label`} htmlFor={id}>{label}</label>}
      {control}
      {error && <p className="dk-field-error" id={errorId} role="alert" data-testid={`${id}-error`}><Icon name="alert-circle" />{error}</p>}
      {hint}
    </div>
  )
}
