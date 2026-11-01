import { useTranslation } from 'react-i18next'
import SettingRow from './SettingRow'
import Toggle from './Toggle'
import { declaresVision, withVision } from '../utils/chatVision'

// The cloud-proxy section's "can look at images" switch. LocalAI cannot ask
// the upstream what it accepts, so the person who knows says it here; the
// switch writes "vision" into known_usecases, which the Known Usecases field
// shows too. Translate mode sends the upstream text only, so the editor
// leaves the switch out there.
export default function ProxyVisionSwitch({ usecases, onChange }) {
  const { t } = useTranslation('modelEditor')
  return (
    <div data-testid="proxy-vision-switch">
      <SettingRow label={t('proxyVision.label')} description={t('proxyVision.hint')}>
        <Toggle checked={declaresVision(usecases)} onChange={on => onChange(withVision(usecases, on))} />
      </SettingRow>
    </div>
  )
}
