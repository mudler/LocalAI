/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import PreviewBody from './PreviewBody'
import SidePane from '../ui/SidePane'
import Button from '../ui/Button'
import { filesApi } from '../../utils/api'
import { fileSize } from '../../utils/fileInfo'
import { kindOf, relativeDate } from '../../utils/fileBrowser'

// The preview of one file of the Files page, in the app's side pane: docked
// beside the list on a wide screen, over the page below 1280 px, a bottom
// sheet on a phone. The head says what the file is (type, size, when it
// changed); the foot has its actions, the same as the row menu: Download,
// Rename, Move and Move to Trash. Escape closes it.
//
// entry has path, name, size, kind and modified. The actions are optional.
export default function FilePreview({ entry, onClose, onRename, onMove, onDelete }) {
  const { t, i18n } = useTranslation('workspace')
  const when = relativeDate(entry.modified, { locale: i18n.language })
  const meta = [t(`types.${kindOf(entry)}`), entry.kind === 'file' ? fileSize(entry.size) : '', when.label].filter(Boolean)
  return (
    <SidePane
      open title={entry.name} onClose={onClose} wide testId="file-preview" className="ws-preview"
      meta={<span title={when.exact} data-testid="ws-preview-meta">{meta.join(' · ')}</span>}
      footer={(
        <div className="ws-preview__acts" role="group" aria-label={t('preview.actions')}>
          <Button size="sm" variant="primary" icon="download" href={filesApi.contentUrl(entry.path)} download={entry.name}>{t('download')}</Button>
          {onRename && <Button size="sm" variant="ghost" icon="pencil" onClick={() => onRename(entry)}>{t('rename')}</Button>}
          {onMove && <Button size="sm" variant="ghost" icon="arrow-right" onClick={() => onMove(entry)}>{t('moveTo')}</Button>}
          {onDelete && <Button size="sm" variant="danger-ghost" icon="trash" onClick={() => onDelete(entry)} className="ws-preview__delete" aria-label={t('delete')} title={t('delete')} />}
        </div>
      )}
    >
      {entry.kind === 'file' && (
        <PreviewBody key={`${entry.path}:${entry.size}:${entry.version}`} item={{ type: 'file', path: entry.path, name: entry.name, size: entry.size, mime: entry.mime || '' }} />
      )}
    </SidePane>
  )
}
