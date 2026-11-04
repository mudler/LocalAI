import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars -- used only inside JSX
import ConfirmDialog from './ConfirmDialog'

// Asks before an agent, trigger, connector or skill is deleted. `item` is the
// row to delete (null when closed) and `kind` picks the warning. The page
// deletes in onConfirm and shows a failure in its own error area.
export default function ChatDeleteConfirm({ item, kind, onConfirm, onCancel }) {
  const { t } = useTranslation('chats')
  return (
    <ConfirmDialog
      open={Boolean(item)}
      title={t('confirmDelete.title', { name: item?.name || '' })}
      message={t(`confirmDelete.${kind}`)}
      confirmLabel={t('confirmDelete.confirm')}
      danger
      onConfirm={() => onConfirm(item)}
      onCancel={onCancel}
    />
  )
}
