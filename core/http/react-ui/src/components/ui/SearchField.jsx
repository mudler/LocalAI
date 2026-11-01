import Icon from '../Icon'

// A search box for a list toolbar: a magnifier inside a kit input that grows
// to fill the row. It needs a name: `label` is both the aria-label and, unless
// `placeholder` says otherwise, the placeholder.
//
//   value, onChange(text)   controlled text
//   label                   accessible name ("Search tasks")
//   placeholder             the hint inside the box (default: label)
export default function SearchField({ value, onChange, label, placeholder, className = '', testId, ...rest }) {
  return (
    <span className={`ui-search ${className}`.trim()}>
      <Icon name="search" />
      <input
        type="search"
        className="dk-input ui-search__input"
        aria-label={label}
        placeholder={placeholder ?? label}
        value={value}
        onChange={(e) => onChange?.(e.target.value)}
        data-testid={testId}
        {...rest}
      />
    </span>
  )
}
