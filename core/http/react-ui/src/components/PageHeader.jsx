// Page header: title, supporting line, and a right-aligned meta/actions slot.
// There is no eyebrow or breadcrumb above the title: the sidebar and the hub
// tab bar already say where the page lives, so the title stands alone.
export default function PageHeader({ title, supporting, actions, className = '' }) {
  return (
    <header className={`page-header page-header--editorial ${className}`.trim()}>
      <div className="page-header__lead">
        {title && <h1 className="page-title">{title}</h1>}
        {supporting && <p className="page-header__supporting">{supporting}</p>}
      </div>
      {actions && <div className="page-header__meta">{actions}</div>}
    </header>
  )
}
