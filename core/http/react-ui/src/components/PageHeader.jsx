// Editorial page header: left eyebrow + serif title + supporting line,
// right-aligned meta/actions slot. Asymmetric, left-aligned.
export default function PageHeader({ eyebrow, title, supporting, actions, className = '' }) {
  return (
    <header className={`page-header page-header--editorial ${className}`.trim()}>
      <div className="page-header__lead">
        {eyebrow && <span className="page-header__eyebrow">{eyebrow}</span>}
        {title && <h1 className="page-title">{title}</h1>}
        {supporting && <p className="page-header__supporting">{supporting}</p>}
      </div>
      {actions && <div className="page-header__meta">{actions}</div>}
    </header>
  )
}
