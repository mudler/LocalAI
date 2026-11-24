import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'

// The remote answered and gave nothing to use: the refresh ended with an error
// text, or no model is listed any more. The edit sheet shows it too.
export default function NoUsable({ remote, onEdit }) {
  const { t } = useTranslation('remotes')
  return (
    <Callout
      tone="warn"
      role="status"
      title={t('card.noUsableTitle')}
      testId="remote-no-usable"
      actions={onEdit && <Button size="sm" icon="edit" onClick={() => onEdit(remote)} data-testid="remote-no-usable-edit">{t('card.editRemote')}</Button>}
    >
      {remote.last_error || t('card.noUsableText')}
    </Callout>
  )
}
