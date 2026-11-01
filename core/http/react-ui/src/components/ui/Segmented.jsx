// A segmented filter: one choice of a few, as pressed buttons in a tray. Use it
// for a list filter ("All", "On", "Off"); use tabs for views.
//
//   options     [{ id, label, count }]  count is drawn after the label
//   value       the id of the pressed option
//   onChange    called with the id of the option pressed
//   label       accessible name of the group
//   itemTestId  optional prefix: each button gets data-testid prefix + id
//
// Each button carries its id in data-filter, so a spec can press it.
export default function Segmented({ options, value, onChange, label, className = '', testId, itemTestId }) {
  return (
    <div className={`ui-seg ${className}`.trim()} role="group" aria-label={label} data-testid={testId}>
      {options.map(o => (
        <button
          key={o.id}
          type="button"
          className="ui-seg__item"
          aria-pressed={value === o.id}
          data-filter={o.id}
          data-testid={itemTestId ? `${itemTestId}${o.id}` : undefined}
          onClick={() => onChange?.(o.id)}
        >
          {o.label}
          {o.count != null && <span className="ui-seg__count">{o.count}</span>}
        </button>
      ))}
    </div>
  )
}
