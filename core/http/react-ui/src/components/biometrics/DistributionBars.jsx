// DistributionBars — one horizontal bar per label, width proportional to value.
// distribution: Record<string, number> (values are probabilities 0..1 or any positive scale).
// dominant: string — highlighted row.
export default function DistributionBars({ title, distribution, dominant, icon }) {
  if (!distribution || Object.keys(distribution).length === 0) return null
  const entries = Object.entries(distribution).sort((a, b) => b[1] - a[1])
  const max = entries.reduce((m, [, v]) => Math.max(m, v), 0) || 1

  return (
    <div className="biometrics-dist card">
      <div className="biometrics-dist__head">
        {icon && <i className={icon} aria-hidden="true" />}
        <h3>{title}</h3>
        {dominant && <span className="biometrics-dist__dominant">{dominant}</span>}
      </div>
      <ul className="biometrics-dist__rows">
        {entries.map(([label, value]) => {
          const pct = (value / max) * 100
          const isDominant = label === dominant
          return (
            <li key={label} className={`biometrics-dist__row ${isDominant ? 'dominant' : ''}`}>
              <span className="biometrics-dist__label">{label}</span>
              <div className="biometrics-dist__bar-wrap" aria-hidden="true">
                <div className="biometrics-dist__bar" style={{ width: `${pct}%` }} />
              </div>
              <span className="biometrics-dist__value">{(value * 100).toFixed(1)}%</span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
