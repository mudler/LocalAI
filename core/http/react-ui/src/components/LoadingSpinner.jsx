// A ring that turns while something loads, for a wait with no shape to show
// (a button, a short action). For content that is coming, prefer a skeleton
// in its shape (Skeleton, SkeletonRows). The label is plain text because the
// boot screen draws this before any translation has loaded.
//
//   size    sm (16) | md (24, default) | lg (40); boot is lg
//   label   the accessible name, default "Loading"
export default function LoadingSpinner({ size = 'md', className = '', label = 'Loading' }) {
  const sizeClass =
    size === 'sm' ? 'spinner-sm'
    : size === 'lg' ? 'spinner-lg'
    : size === 'boot' ? 'spinner-lg'
    : 'spinner-md'
  return (
    <div className={`spinner ${sizeClass} ${className}`.trim()} role="status" aria-label={label}>
      <div className="spinner-ring" />
    </div>
  )
}
