export default function SettingRow({ label, description, children }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      padding: 'var(--spacing-sm) 0',
      borderBottom: '1px solid var(--color-border-subtle)',
    }}>
      <div style={{ flex: 1, marginRight: 'var(--spacing-md)' }}>
        <div style={{ fontSize: '0.875rem', fontWeight: 500 }}>{label}</div>
        {description && <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>}
      </div>
      <div style={{ flexShrink: 0 }}>{children}</div>
    </div>
  )
}
