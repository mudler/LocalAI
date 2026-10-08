// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { explainError } from '../../utils/identity'
import Icon from '../Icon'

// A failed call in plain words first, the server's own message second, and a
// way to the traces. `onRetry` adds a button when trying again makes sense.
export default function ErrorNote({ error, kind, onRetry }) {
  const { t } = useTranslation('biometrics')
  const { title, body } = explainError(error, kind)
  return (
    <div className="idn-error" role="alert" data-testid="identity-error">
      <span className="idn-error__icon" aria-hidden="true"><Icon name="alert-circle" /></span>
      <div className="idn-error__body">
        <h3 className="idn-error__title">{title}</h3>
        <p className="idn-error__text">{body}</p>
        {error?.message && <p className="idn-error__raw dk-mono">{error.message}</p>}
        <div className="idn-error__acts">
          {onRetry && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onRetry}><Icon name="refresh" /> {t('error.retry')}</button>}
          <Link className="dk-btn dk-btn--ghost dk-btn--sm" to="/app/traces?tab=backend"><Icon name="waveform" /> {t('error.traces')}</Link>
        </div>
      </div>
    </div>
  )
}
