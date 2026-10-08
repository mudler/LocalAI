/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useHubAuth } from '../components/hub/useHubAuth'
import { destinationsFor } from '../utils/destinations'
import Icon from '../components/Icon'
import './notfound.css'

// The page for an address that is not a page. It says what was asked for and
// lists the places this viewer can really go (the same destinations as the
// sidebar, with the same gates). It has no search: the app has none to offer.
export default function NotFound() {
  const { t } = useTranslation('auth')
  const { t: tn } = useTranslation('nav')
  const { pathname } = useLocation()
  const auth = useHubAuth()
  const places = destinationsFor(auth)

  return (
    <div className="page page--narrow nf" data-testid="not-found">
      <p className="nf-code" aria-hidden="true">404</p>
      <h1 className="nf-title">{t('notFound.title')}</h1>
      <p className="nf-text">{t('notFound.text')}</p>
      <p className="nf-path"><span className="dk-mono">{pathname}</span></p>
      <div className="nf-acts">
        <Link className="dk-btn dk-btn--primary" to="/app"><Icon name="home" /> {t('notFound.goHome')}</Link>
      </div>
      {places.length > 0 && (
        <nav className="nf-places" aria-label={t('notFound.where')}>
          <h2 className="nf-h2">{t('notFound.where')}</h2>
          <ul>
            {places.map(p => (
              <li key={p.path}>
                <Link className="nf-place" to={p.path}><Icon name={p.icon} aria-hidden="true" /> {tn(p.labelKey)}</Link>
              </li>
            ))}
          </ul>
        </nav>
      )}
    </div>
  )
}
