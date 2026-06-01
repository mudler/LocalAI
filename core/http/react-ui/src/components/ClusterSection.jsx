import { useState } from 'react'

// ClusterSection is a collapsible, titled container for one capability area of
// the Cluster page (Distributed / Swarm). Default expanded.
export default function ClusterSection({ icon, title, subtitle, defaultOpen = true, children }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <section className="card" style={{ marginBottom: 'var(--spacing-lg)' }}>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        style={{
          display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
          width: '100%', padding: 'var(--spacing-md)', background: 'none',
          border: 'none', cursor: 'pointer', textAlign: 'left', color: 'inherit',
        }}
      >
        <i className={`fas fa-chevron-${open ? 'down' : 'right'}`} style={{ width: '1rem', color: 'var(--color-text-muted)' }} />
        {icon && <i className={icon} style={{ color: 'var(--color-primary)' }} />}
        <span style={{ fontWeight: 600 }}>{title}</span>
        {subtitle && <span style={{ marginLeft: 'auto', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>{subtitle}</span>}
      </button>
      {open && <div style={{ padding: '0 var(--spacing-md) var(--spacing-md)' }}>{children}</div>}
    </section>
  )
}
