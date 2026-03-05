export default function LoadingSpinner({ size = 'md', className = '' }) {
  const sizeClass = size === 'sm' ? 'spinner-sm' : size === 'lg' ? 'spinner-lg' : 'spinner-md'
  return (
    <div className={`spinner ${sizeClass} ${className}`}>
      <div className="spinner-ring" />
    </div>
  )
}
