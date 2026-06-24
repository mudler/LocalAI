export default function ErrorWithTraceLink({ message, style }) {
  return (
    <div style={{ textAlign: 'center', color: 'var(--color-error)', ...style }}>
      <i className="fas fa-circle-exclamation" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.6 }} />
      <p>Error: {message}</p>
      <a href="/app/traces?tab=backend" className="chat-error-trace-link" style={{ justifyContent: 'center' }}>
        <i className="fas fa-wave-square" /> View traces for details
      </a>
    </div>
  )
}
