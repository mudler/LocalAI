import LoadingSpinner from './LoadingSpinner'

// Suspense fallback for lazy-loaded routes. Centered in the content area; the
// CSS delays its appearance ~150ms so fast chunk loads don't flash a spinner.
export default function RouteFallback() {
  return (
    <div className="route-fallback" role="status" aria-live="polite">
      <LoadingSpinner size="lg" />
    </div>
  )
}
