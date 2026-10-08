import Icon from './Icon'
export default function ErrorWithTraceLink({ message, style }) {
  return (
    <div style={{ textAlign: 'center', color: 'var(--color-error)', ...style }}>
      <Icon name="alert-circle" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.6 }} />
      <p>Error: {message}</p>
      <a href="/app/traces?tab=backend" className="chat-error-trace-link" style={{ justifyContent: 'center' }}>
        <Icon name="waveform" /> View traces for details
      </a>
    </div>
  )
}
