/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The form controls of the connector pages. They draw the host's kit classes
// (dk-input, dk-select, dk-switch, dk-radio, dk-chip, dk-field), which the host
// style sheet already loads, so a form of an add-on looks like a form of a core
// page. The markup is plain: no browser default control is left.
import { useId, useLayoutEffect, useRef, useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'

// Label, control, hint and error of one field. The control comes from a render
// function so that the label, the hint and the error are tied to it:
//
//   <Field label="Name" hint="...">{(p) => <input className="dk-input" {...p} />}</Field>
export function Field({ label, hint, error, optional, className = '', children }) {
  const id = useId()
  const hintId = hint ? `${id}-hint` : undefined
  const errorId = error ? `${id}-error` : undefined
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined
  return (
    <div className={`dk-field ${className}`.trim()}>
      <label className="dk-label" htmlFor={id} data-optional={optional ? '' : undefined}>{label}</label>
      {children({ id, 'aria-describedby': describedBy, 'aria-invalid': error ? 'true' : undefined })}
      {hint && <Hint id={hintId}>{hint}</Hint>}
      {error && <FieldError id={errorId}>{error}</FieldError>}
    </div>
  )
}

export function FieldError({ id, children, ...rest }) {
  return (
    <p id={id} className="dk-field-error" {...rest}>
      <Icon name="alert-circle" />
      <span>{children}</span>
    </p>
  )
}

// Help text of at most two lines. When the text is longer, a "More" control
// opens the rest, so a long paragraph never pushes the next field down.
export function Hint({ id, children, more = 'More', less = 'Less' }) {
  const ref = useRef(null)
  const [open, setOpen] = useState(false)
  const [clipped, setClipped] = useState(false)
  useLayoutEffect(() => {
    const el = ref.current
    if (el && !open) setClipped(el.scrollHeight > el.clientHeight + 1)
  }, [children, open])
  return (
    <div className="cn-hint">
      <p id={id} ref={ref} className={`dk-hint cn-hint__text${open ? ' is-open' : ''}`}>{children}</p>
      {(clipped || open) && (
        <button type="button" className="cn-hint__more" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? less : more}
        </button>
      )}
    </div>
  )
}

// A text input of the kit. `mono` is for tokens and identifiers.
export function TextInput({ mono, className = '', ...rest }) {
  return <input className={`dk-input${mono ? ' dk-input--mono' : ''} ${className}`.trim()} {...rest} />
}

// A select of the kit: the chevron is drawn by the wrapper, not by the browser.
export function Select({ children, className = '', ...rest }) {
  return (
    <span className="dk-select-wrap">
      <select className={`dk-select ${className}`.trim()} {...rest}>{children}</select>
    </span>
  )
}

// A switch for an on/off setting. `label` names it for assistive technology;
// when `children` is set, it is drawn as the visible label next to the switch.
export function Switch({ checked, onChange, label, children, testId, disabled }) {
  const id = useId()
  const button = (
    <button type="button" role="switch" className="dk-switch" aria-checked={checked ? 'true' : 'false'}
      aria-label={children ? undefined : label} aria-labelledby={children ? id : undefined}
      data-testid={testId} disabled={disabled} onClick={() => onChange(!checked)} />
  )
  if (!children) return button
  return (
    <div className="cn-switchrow">
      <span id={id}>{children}</span>
      {button}
    </div>
  )
}

// One choice out of a few, as cards with a real radio inside: the arrow keys,
// the group name and the form all work as for a native radio.
export function RadioCards({ name, legend, value, options, onChange }) {
  return (
    <div className="cn-cards" role="radiogroup" aria-label={legend}>
      {options.map((o) => (
        <label key={String(o.value)} className={`cn-card${value === o.value ? ' is-on' : ''}`}>
          <input type="radio" className="dk-radio" name={name} checked={value === o.value} onChange={() => onChange(o.value)} />
          <span>{o.label}</span>
        </label>
      ))}
    </div>
  )
}

// A checkbox with its label, in the kit's style.
export function Check({ checked, onChange, children, testId }) {
  return (
    <label className="dk-choice">
      <input type="checkbox" className="dk-check" data-testid={testId} checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span>{children}</span>
    </label>
  )
}

// An inline notice with a tone (the host's callout classes).
export function Notice({ tone = 'info', role, testId, children, className = '' }) {
  const icon = { info: 'info', ok: 'check-circle', warn: 'warning', error: 'alert-circle' }[tone] || 'info'
  return (
    <div className={`ui-callout ui-callout--${tone} ${className}`.trim()} role={role} data-testid={testId}>
      <Icon name={icon} className="ui-callout__icon" />
      <div className="ui-callout__text">{children}</div>
    </div>
  )
}
