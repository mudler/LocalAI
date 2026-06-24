export default function AttentionCallout({ nodes, onApprove }) {
  const pending = nodes.filter(n => n.status === 'pending')
  const unhealthy = nodes.filter(n => n.status === 'unhealthy' || n.status === 'offline')
  if (pending.length === 0 && unhealthy.length === 0) return null

  if (pending.length > 0) {
    const first = pending[0]
    const extra = pending.length - 1
    return (
      <div className="attention-callout attention-callout--warn">
        <span>
          <i className="fas fa-exclamation-circle" />{' '}
          <strong>{pending.length} node{pending.length > 1 ? 's' : ''} awaiting approval</strong>
          {' - '}{first.name}{extra > 0 ? ` +${extra} more` : ''}
        </span>
        <button className="btn btn-primary btn-sm" onClick={() => onApprove(first.id)}>
          <i className="fas fa-check" /> Approve {first.name}
        </button>
      </div>
    )
  }
  return (
    <div className="attention-callout attention-callout--error">
      <span>
        <i className="fas fa-exclamation-triangle" />{' '}
        <strong>{unhealthy.length} node{unhealthy.length > 1 ? 's' : ''} unhealthy</strong>
        {' - '}{unhealthy.map(n => n.name).slice(0, 3).join(', ')}
      </span>
    </div>
  )
}
