import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { describeOutcome } from './errors'

// Where an install is, drawn inline under the add-on it works on: the step
// in words and a bar for the two steps (install, then turn on). The text says
// it; the bar and the icon only support it.
export default function FlowStatus({ flow, name, onRetry }) {
  const { t } = useTranslation('addons')
  if (!flow || flow.step === 'done') return null
  if (flow.step === 'install' || flow.step === 'turn-on') {
    return (
      <div className="ad-flow" role="status" data-testid="flow-progress" data-step={flow.step}>
        <span className="ad-flow__label"><Icon name="spinner" spin /> {flow.step === 'install' ? t('flow.installing', { name }) : t('flow.turningOn', { name })}</span>
        <span className="ad-progress" data-step={flow.step} aria-hidden="true"><span className="ad-progress__bar" /></span>
      </div>
    )
  }
  const reason = describeOutcome(flow.outcome, t, flow.error)
  return (
    <Callout
      tone="error"
      role="alert"
      className="ad-flow ad-flow--failed"
      testId="flow-failed"
      data-step={flow.failedStep}
      title={flow.failedStep === 'install' ? t('flow.installFailed', { name }) : t('flow.turnOnFailed', { name })}
      actions={onRetry && <Button size="sm" icon="refresh" onClick={onRetry}>{t('catalog.retry')}</Button>}
    >
      {reason}
    </Callout>
  )
}
