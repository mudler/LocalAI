import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { fileIcon, fileSize } from '../../utils/fileInfo'
import { filesApi } from '../../utils/api'
import { undrawnImages } from '../../utils/chatMedia'
import './fileChips.css'

// A file that a turn created or changed, as a chip under the answer:
// "Created budget.xlsx · Open preview". The whole chip is one button that
// opens the chat's preview pane on the file.
//
//   path    the file's path in the user's files (the pane finds it by path)
//   name    what the chip says
//   size    the size in bytes, in the button's title
//   kind    'created' or 'changed'
//   onOpen  opens the pane on the file
export function FileChip({ path, name, size, kind = 'created', onOpen }) {
  const { t } = useTranslation('chat')
  const verb = kind === 'changed' ? t('preview.chip.changed') : t('preview.chip.created')
  return (
    <button
      type="button"
      className="cx-fchip"
      data-testid="chat-file-chip"
      data-kind={kind}
      data-path={path}
      onClick={() => onOpen(path)}
      title={size > 0 ? `${name} · ${fileSize(size)}` : name}
      aria-label={t('preview.chip.label', { verb, name })}
    >
      <Icon name={fileIcon(name)} className="cx-fchip__icon" />
      <span className="cx-fchip__verb">{verb}</span>
      <span className="cx-fchip__name">{name}</span>
      <span className="cx-fchip__sep" aria-hidden="true">·</span>
      <span className="cx-fchip__open">{t('preview.open')}</span>
    </button>
  )
}

// An image the turn made, drawn under the answer: a thumbnail that opens the
// pane. When it does not load it draws nothing (the file's chip is in the row),
// never a broken icon.
function ImageTile({ file, onOpen }) {
  const { t } = useTranslation('chat')
  const [broken, setBroken] = useState(false)
  if (broken) return null
  return (
    <li>
    <button
      type="button" className="cx-fimg" data-testid="chat-file-image" data-path={file.path}
      onClick={() => onOpen(file.path)} aria-label={t('preview.chip.label', { verb: file.kind === 'changed' ? t('preview.chip.changed') : t('preview.chip.created'), name: file.name })}
    >
      <img src={filesApi.contentUrl(file.path, true)} alt={file.name} loading="lazy" onError={() => setBroken(true)} />
    </button>
    </li>
  )
}

// The chips of one answer. files: [{ path, name, size, kind }]. An image the
// answer does not draw itself (text is the answer's markdown) also shows as a
// thumbnail above the chips, so an answer that only says "done" still shows
// what it made.
export function FileChipRow({ files, onOpen, text = '' }) {
  const { t } = useTranslation('chat')
  if (!files || files.length === 0) return null
  const tiles = new Set(undrawnImages(files, text).map(f => f.path))
  return (
    <ul className="cx-fchips" aria-label={t('preview.chip.row')} data-testid="chat-file-chips">
      {files.filter(f => tiles.has(f.path)).map(f => (
        <ImageTile key={f.path} file={f} onOpen={onOpen} />
      ))}
      {files.map(f => (
        <li key={`c-${f.path}`}><FileChip {...f} onOpen={onOpen} /></li>
      ))}
    </ul>
  )
}

export default FileChip
