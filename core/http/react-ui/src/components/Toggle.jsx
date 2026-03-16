export default function Toggle({ checked, onChange, disabled }) {
  return (
    <label style={{
      position: 'relative', display: 'inline-block', width: 40, height: 22, cursor: disabled ? 'not-allowed' : 'pointer',
      opacity: disabled ? 0.5 : 1,
    }}>
      <input
        type="checkbox"
        checked={checked || false}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
        style={{ display: 'none' }}
      />
      <span style={{
        position: 'absolute', inset: 0, borderRadius: 22,
        background: checked ? 'var(--color-primary)' : 'var(--color-toggle-off)',
        transition: 'background 200ms',
      }}>
        <span style={{
          position: 'absolute', top: 2, left: checked ? 20 : 2,
          width: 18, height: 18, borderRadius: '50%',
          background: 'var(--color-text-inverse)', transition: 'left 200ms',
          boxShadow: 'var(--shadow-sm)',
        }} />
      </span>
    </label>
  )
}
