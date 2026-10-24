import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { describeOutcome } from './errors'

// Where an "Install and turn on" is, drawn inline under the add-on it works
// on. The text says it; the icon only supports it.
export default function FlowStatus({ flow, name, onRetry }) {
  const { t } = useTranslation('addons')
  if (!flow || flow.step === 'done') return null
  if (flow.step === 'install' || flow.step === 'turn-on') {
    return (
      <p className="ad-flow" role="status" data-testid="flow-progress" data-step={flow.step}>
        <Icon name="spinner" spin /> {flow.step === 'install' ? t('flow.installing', { name }) : t('flow.turningOn', { name })}
      </p>
    )
  }
  const reason = describeOutcome(flow.outcome, t, flow.error)
  return (
    <div className="ad-flow ad-flow--failed" role="alert" data-testid="flow-failed" data-step={flow.failedStep}>
      <span>
        {flow.failedStep === 'install' ? t('flow.installFailed', { name }) : t('flow.turnOnFailed', { name })}
        {reason ? ` ${reason}` : ''}
      </span>
      {onRetry && (
        <button type="button" className="dk-btn dk-btn--sm" onClick={onRetry}>
          <Icon name="refresh" /> {t('catalog.retry')}
        </button>
      )}
    </div>
  )
}
