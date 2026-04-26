export default function Toggle({ checked, onChange, disabled }) {
  return (
    <label className={`toggle${checked ? ' toggle--on' : ''}${disabled ? ' toggle--disabled' : ''}`}>
      <input
        type="checkbox"
        checked={checked || false}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
      />
      <span className="toggle__track">
        <span className="toggle__thumb" />
      </span>
    </label>
  )
}
