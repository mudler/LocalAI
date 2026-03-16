export default function Modal({ onClose, children, maxWidth = '600px' }) {
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 1000,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'var(--color-modal-backdrop)', backdropFilter: 'blur(4px)',
    }} onClick={onClose}>
      <div style={{
        background: 'var(--color-bg-secondary)',
        border: '1px solid var(--color-border-subtle)',
        borderRadius: 'var(--radius-lg)',
        maxWidth, width: '90%', maxHeight: '80vh',
        display: 'flex', flexDirection: 'column',
        overflow: 'auto',
      }} onClick={e => e.stopPropagation()}>
        {children}
      </div>
    </div>
  )
}
