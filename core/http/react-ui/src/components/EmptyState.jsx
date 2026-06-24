// Editorial empty state: optional eyebrow + icon, serif title, lede, actions.
// Wraps the existing .empty-state CSS so legacy callers keep working.
export default function EmptyState({ icon, eyebrow, title, body, actions, className = '' }) {
  return (
    <div className={`empty-state ${className}`.trim()}>
      {eyebrow && <span className="empty-state__eyebrow">{eyebrow}</span>}
      {icon && <i className={`empty-state-icon fas ${icon}`} aria-hidden="true" />}
      {title && <h2 className="empty-state-title">{title}</h2>}
      {body && <p className="empty-state-text">{body}</p>}
      {actions && <div className="empty-state__actions">{actions}</div>}
    </div>
  )
}
