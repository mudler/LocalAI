import { useTranslation } from 'react-i18next'
import { gbLabel, fitStyle } from '../../utils/modelLedger'
import Icon from '../Icon'

// The models disk, as a small strip in the page header. It shows what is free,
// turns amber when the disk is low (under 10 percent or under 20 GB free), and
// opens the cleanup review. The caller hides it when the server reports no disk.
export default function DiskStrip({ disk, onOpen, open }) {
  const { t } = useTranslation('models')
  if (!disk) return null
  const otherFraction = disk.total > 0 ? disk.other / disk.total : 0
  const modelsFraction = disk.total > 0 ? disk.models / disk.total : 0
  return (
    <button
      type="button"
      className="ledger-disk"
      data-low={disk.low ? 'true' : 'false'}
      data-testid="disk-strip"
      aria-haspopup="dialog"
      aria-expanded={!!open}
      aria-label={t('disk.stripAria', { free: gbLabel(disk.free), total: gbLabel(disk.total), low: disk.low ? t('disk.lowShort') : '' })}
      onClick={onOpen}
    >
      <Icon name="hard-drive" className="ledger-disk__icon" />
      <span className="ledger-disk__text">
        <span className="ledger-disk__free">{t('disk.free', { amount: gbLabel(disk.free) })}</span>
        {disk.low && <span className="ledger-disk__low">{t('disk.low')}</span>}
      </span>
      <span className="ledger-disk__bar" aria-hidden="true">
        <span className="ledger-disk__fill ledger-disk__fill--other" style={fitStyle(otherFraction)} />
        <span className="ledger-disk__fill ledger-disk__fill--models" style={fitStyle(modelsFraction)} />
      </span>
      <Icon name="chevron-right" className="ledger-disk__chev" />
    </button>
  )
}
