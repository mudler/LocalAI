 
import { useTranslation } from 'react-i18next'
import { WINDOWS } from '../../utils/traffic'
import { useTrafficWindow } from '../../hooks/useTraffic'

// One window for every Traffic page: the choice made here is the one the next
// page opens with. Four windows, because the trace summary stops at 7 days and
// the usage ledger groups by day, week, month or all.
export default function WindowSwitch() {
  const { t } = useTranslation('traffic')
  const { id, setWindow } = useTrafficWindow()
  return (
    <div className="dk-segmented" role="radiogroup" aria-label={t('window.label')} data-testid="window-switch">
      {WINDOWS.map(w => (
        <button
          key={w.id}
          type="button"
          role="radio"
          aria-checked={id === w.id}
          className="dk-seg"
          onClick={() => setWindow(w.id)}
        >
          {t(`window.${w.id}`)}
        </button>
      ))}
    </div>
  )
}
