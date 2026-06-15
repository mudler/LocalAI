import MODEL_TEMPLATES from '../utils/modelTemplates'

export default function TemplateSelector({ onSelect }) {
  return (
    <div style={{ padding: '0 var(--spacing-lg) var(--spacing-lg)' }}>
      <p style={{ fontSize: '0.875rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-lg)' }}>
        Choose a template to get started. You can add or remove fields in the next step.
      </p>
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
        gap: 'var(--spacing-md)',
      }}>
        {MODEL_TEMPLATES.map(t => (
          <button
            key={t.id}
            className="template-card"
            onClick={() => onSelect(t)}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', width: '100%' }}>
              <i className={`fas ${t.icon}`} style={{ fontSize: '1.25rem', color: 'var(--color-primary)', width: 28, textAlign: 'center' }} />
              <span style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>{t.label}</span>
            </div>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', lineHeight: 1.5, margin: 0 }}>
              {t.description}
            </p>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--spacing-xs)', marginTop: 'var(--spacing-xs)' }}>
              {Object.keys(t.fields).filter(k => k !== 'name').map(k => (
                <span key={k} className="badge" style={{
                  fontSize: '0.6875rem', background: 'var(--color-bg-tertiary)',
                  color: 'var(--color-text-muted)', padding: '2px 6px',
                }}>
                  {k}
                </span>
              ))}
            </div>
          </button>
        ))}
      </div>
      <style>{`
        .template-card {
          display: flex;
          flex-direction: column;
          align-items: flex-start;
          gap: var(--spacing-sm);
          padding: var(--spacing-lg);
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-default);
          border-radius: var(--radius-lg);
          cursor: pointer;
          text-align: left;
          transition: all 150ms;
        }
        .template-card:hover {
          border-color: var(--color-primary);
          background: var(--color-primary-light);
        }
      `}</style>
    </div>
  )
}
