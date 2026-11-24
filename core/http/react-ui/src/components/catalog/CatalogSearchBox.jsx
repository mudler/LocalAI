import Icon from '../Icon'

// The search field of a catalog listing: the kit's icon input with an accessible
// name. Presentational only; the caller owns the value and what searching means.
//
//   value, onChange   controlled text and its handler (receives the string)
//   label             accessible name; also the placeholder unless one is given
//   className         extra class so a page keeps its own layout rules
export default function CatalogSearchBox({ value, onChange, label, placeholder, className = '', inputId, type = 'text' }) {
  return (
    <div className={`dk-input-icon ${className}`.trim()}>
      <Icon name="search" className="dk-icon" />
      <input
        id={inputId}
        className="dk-input"
        type={type}
        aria-label={label}
        placeholder={placeholder ?? label}
        value={value}
        onChange={e => onChange(e.target.value)}
      />
    </div>
  )
}
