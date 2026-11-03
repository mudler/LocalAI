/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Button from '../ui/Button'
import FolderPicker from './FolderPicker'
import { parentOf } from '../../hooks/useWorkspace'

// Picks the folder that the selection moves to. It opens on the folder of
// the selection and walks the tree one folder at a time; a folder that is
// moving, or is inside one, cannot be the target.
//   items   the entries that move
//   start   the folder to open on
//   onMove(dest)  onCancel()
export default function MoveDialog({ items, start, onMove, onCancel }) {
  const { t } = useTranslation('workspace')
  const [at, setAt] = useState(start || '/')
  const moving = new Set(items.map(e => e.path))
  const blocked = (p) => [...moving].some(m => p === m || p.startsWith(`${m}/`))
  const here = items.every(e => parentOf(e.path) === at)

  const parts = at.split('/').filter(Boolean)
  return (
    <Dialog
      title={t('move.title', { count: items.length })}
      description={items.length === 1 ? items[0].name : t('move.items', { count: items.length })}
      onClose={onCancel}
      closeLabel={t('move.cancel')}
      labelId="ws-move-title"
      testId="ws-move"
      foot={(
        <>
          <Button variant="ghost" onClick={onCancel}>{t('move.cancel')}</Button>
          <Button variant="primary" icon="arrow-right" disabled={here || blocked(at)} onClick={() => onMove(at)} data-testid="ws-move-here">
            {t('move.here', { folder: parts.length ? parts[parts.length - 1] : t('home') })}
          </Button>
        </>
      )}
    >
      <FolderPicker at={at} onAt={setAt} blocked={(f) => blocked(f.path)} emptyText={t('move.noFolders')} />
    </Dialog>
  )
}
