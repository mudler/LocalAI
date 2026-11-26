/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useRouteError } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Mascot } from './ui'
import { reloadOnce, routeErrorKind } from '../utils/chunkReload'
import './route-error.css'

// The error element of the routes, in place of React Router's developer
// screen. A chunk that failed to load means the server was updated under a
// tab that still runs the old build: the screen reloads once on its own (for
// an import that has no recovery of its own) and otherwise says so and offers
// Reload. inline draws it inside the app layout, for a page that failed; the
// sidebar stays usable.
export default function RouteError({ inline = false }) {
  const error = useRouteError()
  const { t } = useTranslation('errors')
  const online = typeof navigator === 'undefined' ? true : navigator.onLine
  const kind = routeErrorKind(error, online)
  const [reloading, setReloading] = useState(false)

  useEffect(() => {
    if (kind === 'updated' && reloadOnce()) setReloading(true)
  }, [kind])

  const reload = () => {
    setReloading(true)
    window.location.reload()
  }
  const message = error?.message || (error?.statusText ? `${error.status} ${error.statusText}` : '')

  return (
    <div className={`route-error${inline ? ' route-error--inline' : ''}`} role="alert" data-testid="route-error" data-kind={kind}>
      <div className="route-error__box">
        <Mascot pose="welcome" size="md" />
        <h1 className="route-error__title">{t(`route.${kind}.title`)}</h1>
        <p className="route-error__text">{t(`route.${kind}.text`)}</p>
        <div className="route-error__acts">
          <Button variant="primary" icon="refresh" busy={reloading} onClick={reload}>
            {reloading ? t('route.reloading') : t('route.reload')}
          </Button>
          {inline && <Button variant="ghost" icon="home" to="/app">{t('route.home')}</Button>}
        </div>
        {kind === 'generic' && message && (
          <details className="route-error__details">
            <summary>{t('route.details')}</summary>
            <pre className="dk-mono">{message}</pre>
          </details>
        )}
      </div>
    </div>
  )
}
