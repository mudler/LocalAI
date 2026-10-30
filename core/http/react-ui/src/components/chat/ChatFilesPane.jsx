/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import FilePreview from '../files/FilePreview'
import { filesApi } from '../../utils/api'
import { fileIcon, fileSize } from '../../utils/fileInfo'
import { groupChatFiles } from '../../hooks/useChatFiles'
import { wellKnownTag } from '../../utils/changeNotes'
import { DOCS_MAKE_YOURS } from '../../utils/discovery'

const GROUPS = ['uploaded', 'created', 'changed']

function FileRow({ file, selected, onSelect, t, tw }) {
  const tag = wellKnownTag(file.path)
  return (
    <li className={`ws-row cx-files__row${selected ? ' ws-row--selected' : ''}`} data-testid="chat-file-row" data-origin={file.origin}>
      <Icon name={fileIcon(file.name, file.mime)} className="ws-row__icon" />
      <div className="cx-files__main">
        <button
          type="button" className="cx-files__name" onClick={() => onSelect(selected ? null : file)}
          aria-expanded={selected} title={file.guest_path}
        >
          {file.name}
        </button>
        <span className="cx-files__meta">
          <span>{fileSize(file.size)}</span>
          {file.turn > 0 && <span>{t('files.turn', { n: file.turn })}</span>}
          {file.media && <span className="cx-files__tag" title={t('files.mediaTitle')}>{t('files.media')}</span>}
          {file.changed && <span className="cx-files__tag" title={t('files.changedTitle')}>{t('files.changedTag')}</span>}
          {tag && <span className="cx-files__tag" data-testid="chat-file-wellknown">{tw(`discovery.files.tag.${tag}`)}</span>}
        </span>
      </div>
      <a
        className="ws-icon-btn" href={filesApi.contentUrl(file.path)} download={file.name}
        aria-label={t('files.download', { name: file.name })} title={t('files.download', { name: file.name })}
      >
        <Icon name="download" />
      </a>
    </li>
  )
}

// The files pane of a chat: the files the user attached, the files the
// assistant created and the files it changed, with a preview and a download.
// Files dropped on it are added to the message being written.
export default function ChatFilesPane({ state, onClose, onDropFiles }) {
  const { t } = useTranslation('chat')
  const { t: tw } = useTranslation('workspace')
  const [selected, setSelected] = useState(null)
  const [dragging, setDragging] = useState(false)
  const groups = groupChatFiles(state.files)
  // Keep the preview on the same file when the list is read again, and drop
  // it when the file is gone.
  const current = selected && state.files.find(f => f.path === selected.path)
  useEffect(() => { if (selected && !current) setSelected(null) }, [selected, current])

  const onDrop = (e) => {
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer?.files?.length) onDropFiles(Array.from(e.dataTransfer.files))
  }
  return (
    <aside
      className={`canvas-panel cx-files${dragging ? ' cx-files--drop' : ''}`}
      aria-label={t('files.title')}
      id="chat-files-pane"
      data-testid="chat-files-pane"
      onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
      onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) setDragging(false) }}
      onDrop={onDrop}
    >
      <div className="canvas-panel-header">
        <span className="canvas-panel-title">
          {t('files.title')}
          {state.files.length > 0 && <span className="cx-files__count">{state.files.length}</span>}
        </span>
        <div className="canvas-header-actions">
          <button type="button" className="canvas-icobtn" onClick={onClose} title={t('files.close')} aria-label={t('files.close')}>
            <Icon name="close" />
          </button>
        </div>
      </div>
      <div className="cx-files__body">
        {state.error && <p className="ws-error" role="alert">{t('files.error')}</p>}
        {state.folderPath && (
          <p className="cx-files__folder" title={state.folder}>
            <Icon name="folder" /> <span>{state.folderPath}</span>
          </p>
        )}
        {state.loaded && state.files.length === 0 && !state.error && (
          <div className="cx-files__empty" data-testid="chat-files-empty">
            <Icon name="paperclip" />
            <p className="cx-files__empty-title">{t('files.emptyTitle')}</p>
            <p className="ws-muted">{t('files.emptyBody')}</p>
          </div>
        )}
        {GROUPS.filter(g => groups[g].length > 0).map(g => (
          <section key={g} className="cx-files__group" aria-labelledby={`cx-files-${g}`} data-testid={`chat-files-${g}`}>
            <h3 className="cx-files__heading" id={`cx-files-${g}`}>
              {t(`files.groups.${g}`)} <span className="cx-files__count">{groups[g].length}</span>
            </h3>
            <ul className="ws-list">
              {groups[g].map(f => (
                <FileRow key={f.path} file={f} selected={current?.path === f.path} onSelect={setSelected} t={t} tw={tw} />
              ))}
            </ul>
          </section>
        ))}
        {current && <FilePreview entry={{ ...current, kind: 'file' }} t={tw} className="cx-files__preview" />}
        {state.files.some(f => wellKnownTag(f.path)) && (
          <p className="ws-hint cx-files__hint wk-files-note" data-testid="chat-files-wellknown-note">
            <Icon name="info" /> {tw('discovery.files.learn')}{' '}
            <a href={DOCS_MAKE_YOURS} target="_blank" rel="noopener noreferrer">{tw('discovery.ex.learn')}</a>
          </p>
        )}
        {state.files.length > 0 && <p className="ws-hint cx-files__hint">{t('files.dropHint')}</p>}
      </div>
    </aside>
  )
}
