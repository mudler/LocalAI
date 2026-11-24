import { useTranslation } from 'react-i18next'
import KitStatusChip from '../../../packages/ui-kit/src/StatusChip.jsx'

// The kit's StatusChip (packages/ui-kit). The words of the status
// vocabulary come from common:chip.<key>. Props: see the kit file.
export default function StatusChip(props) {
  const { t } = useTranslation('common')
  return <KitStatusChip words={(key) => t(`chip.${key}`)} {...props} />
}
