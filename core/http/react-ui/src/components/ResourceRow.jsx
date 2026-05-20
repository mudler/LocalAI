import { Fragment } from 'react'

// ResourceRow renders the visible row + its conditional detail row as a pair
// of <tr>s, so the existing .table styling keeps applying and the Manage page
// can re-use the gallery's expand-to-detail interaction without inventing a
// new table system. The consumer owns the cells (which pass through as
// children) — this component only manages the click-to-expand handler, the
// dimmed state for disabled rows, and the colSpan'd detail row beneath.
//
// `onToggleExpand` fires on row click only. Buttons / toggles inside cells
// must call e.stopPropagation() (or be wrapped in an .actions-stop wrapper)
// to avoid double-triggering the expand.
export default function ResourceRow({
  expanded,
  onToggleExpand,
  detail,
  colSpan,
  dimmed,
  className = '',
  children,
}) {
  return (
    <Fragment>
      <tr
        className={`resource-row${dimmed ? ' is-dimmed' : ''}${expanded ? ' is-expanded' : ''} ${className}`.trim()}
        onClick={onToggleExpand}
        style={{ cursor: onToggleExpand ? 'pointer' : 'default' }}
      >
        {children}
      </tr>
      {expanded && detail && (
        <tr className="resource-row__detail-row">
          <td colSpan={colSpan} className="resource-row__detail-cell">
            {detail}
          </td>
        </tr>
      )}
    </Fragment>
  )
}

// ChevronCell is the small rotating chevron used as the leftmost cell of an
// expandable row. Mirrors the Nodes/Models/Backends gallery affordance so
// users see the same "click to expand" cue everywhere.
export function ChevronCell({ expanded }) {
  return (
    <td className="resource-row__chevron-cell">
      <span className={`row-chevron${expanded ? ' is-expanded' : ''}`} aria-hidden="true">
        <i className="fas fa-chevron-right" />
      </span>
    </td>
  )
}

// IconCell renders the 48px brand icon shell — the same one the Install
// gallery uses. `icon` is the image URL (from gallery metadata); when absent
// or broken we fall back to a FontAwesome glyph so custom-imported items
// still get a placeholder instead of an empty square.
export function IconCell({ icon, fallback = 'fa-cube', alt = '' }) {
  return (
    <td className="resource-row__icon-cell">
      <div className="resource-row__icon">
        {icon ? (
          <img src={icon} alt={alt} loading="lazy" />
        ) : (
          <i className={`fas ${fallback}`} />
        )}
      </div>
    </td>
  )
}

// StopPropagationCell wraps cell contents that contain interactive controls
// (Toggle, action buttons) so a click on them doesn't also expand the row.
export function StopPropagationCell({ children, ...props }) {
  return (
    <td {...props} onClick={e => e.stopPropagation()}>
      {children}
    </td>
  )
}
