// StatCard renders a single cluster/dashboard metric card. The left accent
// bar + icon chip color is driven by `accentVar` (a CSS custom property name,
// e.g. "--color-success") so the card reads as semantic without the caller
// having to reach into colors directly. `onClick` upgrades the card to a
// keyboard-focusable button — used by the Manage page so cards double as
// shortcuts to the relevant tab + filter.
export default function StatCard({ icon, label, value, color, accentVar, onClick }) {
  const accent = color || (accentVar ? `var(${accentVar})` : 'var(--color-text-primary)')
  const interactive = typeof onClick === 'function'

  const handleKeyDown = interactive
    ? (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick(e)
        }
      }
    : undefined

  return (
    <div
      className="stat-card"
      data-clickable={interactive ? 'true' : undefined}
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      onClick={interactive ? onClick : undefined}
      onKeyDown={handleKeyDown}
      style={accentVar ? { ['--stat-accent']: `var(${accentVar})` } : undefined}
    >
      <div className="stat-card__body">
        <div className="stat-card__label">{label}</div>
        <div className="stat-card__value" style={{ color: accent }}>{value}</div>
      </div>
      <div className="stat-card__icon" style={accentVar ? { color: accent } : undefined}>
        <i className={icon} />
      </div>
    </div>
  )
}
