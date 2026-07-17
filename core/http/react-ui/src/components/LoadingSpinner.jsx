export default function LoadingSpinner({ size = 'md', className = '' }) {
  const sizeClass =
    size === 'sm' ? 'spinner-sm'
    : size === 'lg' ? 'spinner-lg'
    : size === 'boot' ? 'spinner-lg'
    : 'spinner-md'
  return (
    <div className={`spinner ${sizeClass} ${className}`} role="status" aria-label="Loading">
      <div className="spinner-ring" />
    </div>
  )
}
