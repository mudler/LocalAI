import { useRef } from 'react'
import { radioKeyTarget, radioTabStop } from './segmentedModel.js'

// A segmented control: one choice of a few, as buttons in a tray.
//
//   mode "filter" (default)  a list filter ("All", "On", "Off"): pressed
//                            buttons (aria-pressed). Use tabs for views.
//   mode "radio"             a value to set, one of 2-4 options: a radio group
//                            (aria-checked). One tab stop; the arrow keys,
//                            Home and End move the choice and the focus.
//
//   options     [{ id, label, count, disabled }]  count is drawn after the label
//   value       the id of the pressed (or checked) option
//   onChange    called with the id of the option pressed (or chosen)
//   label       accessible name of the group
//   labelledBy  radio mode: id of an element that names the group, used
//               instead of label
//   itemTestId  optional prefix: each button gets data-testid prefix + id
//
// Each button carries its id in data-filter, so a spec can press it.
export default function Segmented({ options, value, onChange, label, labelledBy, mode = 'filter', className = '', testId, itemTestId }) {
  const items = useRef([])
  const radio = mode === 'radio'
  const tabStop = radio ? radioTabStop(options, value) : -1

  const onKeyDown = (e, i) => {
    if (e.altKey || e.ctrlKey || e.metaKey) return
    const to = radioKeyTarget(options, i, e.key)
    if (to < 0) return
    e.preventDefault()
    items.current[to]?.focus()
    if (to !== i) onChange?.(options[to].id)
  }

  return (
    <div
      className={`ui-seg ${className}`.trim()}
      role={radio ? 'radiogroup' : 'group'}
      aria-label={radio && labelledBy ? undefined : label}
      aria-labelledby={radio ? labelledBy : undefined}
      data-testid={testId}
    >
      {options.map((o, i) => (
        <button
          key={o.id}
          ref={radio ? (el => { items.current[i] = el }) : undefined}
          type="button"
          className="ui-seg__item"
          role={radio ? 'radio' : undefined}
          aria-pressed={radio ? undefined : value === o.id}
          aria-checked={radio ? value === o.id : undefined}
          tabIndex={radio ? (i === tabStop ? 0 : -1) : undefined}
          disabled={o.disabled}
          data-filter={o.id}
          data-testid={itemTestId ? `${itemTestId}${o.id}` : undefined}
          onClick={() => onChange?.(o.id)}
          onKeyDown={radio ? (e => onKeyDown(e, i)) : undefined}
        >
          {o.label}
          {o.count != null && <span className="ui-seg__count">{o.count}</span>}
        </button>
      ))}
    </div>
  )
}
