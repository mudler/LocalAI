// The kit switch (.dk-switch, 44 x 26 px): a button with role="switch".
// Space and Enter toggle it, as for any button. It is controlled, and it needs
// a name: `label` (aria-label) or `labelledBy` (the id of a visible label).
//
//   checked        on or off
//   onChange(next) called with the new value
//   label          accessible name
//   labelledBy     id of the element that names it (wins over label)
//   describedBy    id of the element that describes it
//   disabled       cannot be toggled
export default function Switch({ checked, onChange, label, labelledBy, describedBy, disabled = false, id, className = '', testId }) {
  return (
    <button
      type="button"
      role="switch"
      id={id}
      className={`dk-switch ${className}`.trim()}
      aria-checked={!!checked}
      aria-label={labelledBy ? undefined : label}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      disabled={disabled}
      data-testid={testId}
      onClick={() => onChange?.(!checked)}
    />
  )
}
