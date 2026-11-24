import { useContext } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AdminShellContext } from './adminShellContext'

// "Administration / Access / Users". The last crumb is the page and carries
// aria-current; the first leads to the overview. The trail comes from the
// layout (hubConfig.js groups), so a page never spells its own.
export default function AdminCrumbs() {
  const { t } = useTranslation('nav')
  const shell = useContext(AdminShellContext)
  const crumbs = shell?.crumbs || []
  if (crumbs.length === 0) return null
  return (
    <nav className="admin-crumbs" aria-label={t('admin.nav.crumbs')} data-testid="admin-crumbs">
      <ol>
        {crumbs.map((crumb, i) => {
          const last = i === crumbs.length - 1
          return (
            <li key={`${i}-${crumb.label}`}>
              {crumb.to && !last
                ? <Link to={crumb.to}>{crumb.label}</Link>
                : <span aria-current={last ? 'page' : undefined}>{crumb.label}</span>}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
