// ResourceActions groups row-level buttons into a lifecycle cluster (start,
// stop, pin, reinstall, upgrade) and a destructive cluster (delete) with a
// thin divider between them, so a destructive intent visually separates from
// a routine one. Replaces the old 4px-gap row of buttons in the Manage page
// where Stop / Pin / Delete sat shoulder-to-shoulder with no visual cue
// telling apart "click to fiddle" from "click to throw away".
//
// `lifecycle` and `destructive` accept any ReactNode — typically one or more
// <button>s. The wrapping div stops click propagation so action clicks don't
// also expand the row.
export default function ResourceActions({ lifecycle, destructive }) {
  const hasLifecycle = !!lifecycle
  const hasDestructive = !!destructive
  if (!hasLifecycle && !hasDestructive) return null

  return (
    <div className="resource-actions" onClick={e => e.stopPropagation()}>
      {hasLifecycle && (
        <div className="resource-actions__group">{lifecycle}</div>
      )}
      {hasLifecycle && hasDestructive && (
        <span className="resource-actions__divider" aria-hidden="true" />
      )}
      {hasDestructive && (
        <div className="resource-actions__group">{destructive}</div>
      )}
    </div>
  )
}
