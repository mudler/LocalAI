/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import Icon from '../Icon'
import AddonSlot from '../../addons/AddonSlot'
import { filesApi } from '../../utils/api'
import { TEXT_EXT, IMAGE_EXT, extensionOf, fileSize } from '../../utils/fileInfo'
import '../../pages/workspace.css'

// A preview of one file of the user's: its add-on view, an image or the start
// of a text file, and a download. entry has path (in the user's files), name,
// size and kind. t is the "workspace" namespace. className adds to the frame,
// so a side pane can place it.
export default function FilePreview({ entry, t, className = '' }) {
  const [text, setText] = useState(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    setText(null)
    setFailed(false)
    // A read that ends after another file was chosen must not show its text.
    let current = true
    if (entry.kind === 'file' && TEXT_EXT.test(entry.name) && entry.size <= 5 * 1024 * 1024) {
      filesApi.readText(entry.path).then((v) => { if (current) setText(v) }).catch(() => { if (current) setFailed(true) })
    }
    return () => { current = false }
  }, [entry.path, entry.kind, entry.name, entry.size])
  const download = filesApi.contentUrl(entry.path)
  return (
    <aside className={`ws-preview ${className}`.trim()} aria-label={t('preview.label')} data-testid="file-preview">
      <h2 className="ws-preview__name">{entry.name}</h2>
      <p className="ws-muted">{fileSize(entry.size)}</p>
      {entry.kind === 'file' && extensionOf(entry.name) && (
        <AddonSlot slot="files.preview" context={{ path: entry.path, name: entry.name, extension: extensionOf(entry.name) }} />
      )}
      {IMAGE_EXT.test(entry.name) && (
        <img className="ws-preview__image" src={filesApi.contentUrl(entry.path, true)} alt={entry.name} />
      )}
      {text !== null && <pre className="ws-preview__text">{text}</pre>}
      {failed && <p className="ws-muted">{t('preview.failed')}</p>}
      <a className="btn btn-secondary btn-sm" href={download} download={entry.name}>
        <Icon name="download" /> {t('download')}
      </a>
    </aside>
  )
}
