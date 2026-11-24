import Icon from '../Icon'

// The empty state of a catalog listing: an icon, a title, one line and an
// optional action. The kit's empty-state markup, so a page keeps its look.
export default function CatalogEmpty({ icon = 'search', title, text, children, role }) {
  return (
    <div className="dk-empty" role={role}>
      <div className="dk-empty-icon"><Icon name={icon} /></div>
      {title && <h2 className="dk-empty-title">{title}</h2>}
      {text && <p className="dk-empty-text">{text}</p>}
      {children}
    </div>
  )
}
