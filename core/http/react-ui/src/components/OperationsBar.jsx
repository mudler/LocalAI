import { useOperations } from '../hooks/useOperations'

export default function OperationsBar() {
  const { operations, cancelOperation } = useOperations()

  if (operations.length === 0) return null

  return (
    <div className="operations-bar">
      {operations.map(op => (
        <div key={op.jobID || op.id} className="operation-item">
          <div className="operation-info">
            {op.isCancelled ? (
              <i className="fas fa-ban" style={{ color: 'var(--color-warning)', marginRight: 'var(--spacing-xs)' }} />
            ) : op.isDeletion ? (
              <i className="fas fa-trash" style={{ color: 'var(--color-error)', marginRight: 'var(--spacing-xs)' }} />
            ) : (
              <div className="operation-spinner" />
            )}
            <span className="operation-text">
              {op.isDeletion ? 'Removing' : 'Installing'}{' '}
              {op.isBackend ? 'backend' : 'model'}: {op.name || op.id}
            </span>
            {op.isQueued && (
              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                (Queued)
              </span>
            )}
            {op.isCancelled && (
              <span style={{ fontSize: '0.75rem', color: 'var(--color-warning)', marginLeft: 'var(--spacing-xs)' }}>
                Cancelling...
              </span>
            )}
            {op.message && !op.isQueued && !op.isCancelled && (
              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                {op.message}
              </span>
            )}
            {op.progress !== undefined && op.progress > 0 && (
              <span className="operation-progress">{Math.round(op.progress)}%</span>
            )}
          </div>
          {op.progress !== undefined && op.progress > 0 && (
            <div className="operation-bar-container">
              <div className="operation-bar" style={{ width: `${op.progress}%` }} />
            </div>
          )}
          {op.cancellable && !op.isCancelled && (
            <button
              className="operation-cancel"
              onClick={() => cancelOperation(op.jobID)}
              title="Cancel"
            >
              <i className="fas fa-xmark" />
            </button>
          )}
        </div>
      ))}
    </div>
  )
}
