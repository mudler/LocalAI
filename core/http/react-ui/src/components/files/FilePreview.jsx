/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import Icon from '../Icon'
import PreviewBody from './PreviewBody'
import { filesApi } from '../../utils/api'
import { fileSize } from '../../utils/fileInfo'
import '../../pages/workspace.css'

// A preview of one file of the user's on the Files page: its name and size,
// the view of its type (PreviewBody: an add-on's view when one handles the
// extension), and a download. entry has path (in the user's files), name,
// size and kind. t is the "workspace" namespace. className adds to the frame.
export default function FilePreview({ entry, t, className = '' }) {
  return (
    <aside className={`ws-preview ${className}`.trim()} aria-label={t('preview.label')} data-testid="file-preview">
      <h2 className="ws-preview__name">{entry.name}</h2>
      <p className="ws-muted">{fileSize(entry.size)}</p>
      {entry.kind === 'file' && <PreviewBody item={{ type: 'file', path: entry.path, name: entry.name, size: entry.size, mime: entry.mime || '' }} />}
      <a className="btn btn-secondary btn-sm" href={filesApi.contentUrl(entry.path)} download={entry.name}>
        <Icon name="download" /> {t('download')}
      </a>
    </aside>
  )
}
