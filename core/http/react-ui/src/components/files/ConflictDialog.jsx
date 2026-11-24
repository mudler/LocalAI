/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Button from '../ui/Button'
import Icon from '../Icon'

// Asks what to do with uploads whose names are taken in the folder: replace
// the files there, keep both (the new one gets a new name) or skip them. One
// answer covers every clash of the drop.
//   names     the names that clash
//   folder    the folder they go to, as the user reads it
//   onChoose  'replace' | 'keep' | 'skip'; onCancel drops the whole upload
export default function ConflictDialog({ names, folder, onChoose, onCancel }) {
  const { t } = useTranslation('workspace')
  const shown = names.slice(0, 5)
  return (
    <Dialog
      title={t('conflict.title', { count: names.length })}
      description={t('conflict.body', { count: names.length, folder })}
      onClose={onCancel}
      closeLabel={t('conflict.cancel')}
      labelId="ws-conflict-title"
      testId="ws-conflict"
      foot={(
        <>
          <Button variant="ghost" onClick={() => onChoose('skip')}>{t('conflict.skip', { count: names.length })}</Button>
          <Button onClick={() => onChoose('keep')}>{t('conflict.keep')}</Button>
          <Button variant="primary" onClick={() => onChoose('replace')}>{t('conflict.replace', { count: names.length })}</Button>
        </>
      )}
    >
      <ul className="ws-conflict__list">
        {shown.map(n => (
          <li key={n} className="ws-conflict__name"><Icon name="file" /><span>{n}</span></li>
        ))}
        {names.length > shown.length && <li className="ws-conflict__more">{t('conflict.more', { count: names.length - shown.length })}</li>}
      </ul>
      <p className="ws-conflict__note">{t('conflict.note')}</p>
    </Dialog>
  )
}
