import { useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { sectionKeyForPath } from '../utils/section'

// Editorial page header: left eyebrow + title + supporting line, right-aligned
// meta/actions slot. The eyebrow defaults to the page's section/console name
// (derived from the route) so headers stay consistent without per-page wiring;
// pass `eyebrow` to override, or `eyebrow={null}` to suppress it.
export default function PageHeader({ eyebrow, title, supporting, actions, className = '' }) {
  const { t } = useTranslation('nav')
  const { pathname } = useLocation()
  const autoKey = sectionKeyForPath(pathname)
  const resolvedEyebrow = eyebrow !== undefined ? eyebrow : (autoKey ? t(autoKey) : null)
  return (
    <header className={`page-header page-header--editorial ${className}`.trim()}>
      <div className="page-header__lead">
        {resolvedEyebrow && <span className="page-header__eyebrow">{resolvedEyebrow}</span>}
        {title && <h1 className="page-title">{title}</h1>}
        {supporting && <p className="page-header__supporting">{supporting}</p>}
      </div>
      {actions && <div className="page-header__meta">{actions}</div>}
    </header>
  )
}
