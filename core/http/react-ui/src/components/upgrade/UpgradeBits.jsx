/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import './upgrade.css'

// The mark that an update exists, with the version when it is known. The same
// words on every page that shows one.
export function UpdateBadge({ version }) {
  const { t } = useTranslation('upgrade')
  return (
    <span className="up-badge" data-testid="update-badge">
      <Icon name="arrow-up" aria-hidden="true" />
      {version ? t('badge.availableVersion', { version }) : t('badge.available')}
    </span>
  )
}

// Why an upgrade cannot start, in plain words, next to the notice that says one
// exists. `text` is already worded by the page. With `onCheck` it offers a
// check, because a new check can change the answer.
export function UpgradeReason({ text, onCheck, checking = false, testId = 'upgrade-reason' }) {
  const { t } = useTranslation('upgrade')
  if (!text) return null
  return (
    <p className="up-reason" role="status" data-testid={testId}>
      <span>{text}</span>
      {onCheck && (
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={checking} onClick={onCheck} data-testid="upgrade-reason-check">
          <Icon name={checking ? 'spinner' : 'refresh'} spin={checking} aria-hidden="true" /> {checking ? t('reason.checking') : t('reason.checkAgain')}
        </button>
      )}
    </p>
  )
}
