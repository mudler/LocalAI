import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { fileIcon, fileSize } from '../../utils/fileInfo'
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

// The chips of one answer. files: [{ path, name, size, kind }].
export function FileChipRow({ files, onOpen }) {
  const { t } = useTranslation('chat')
  if (!files || files.length === 0) return null
  return (
    <ul className="cx-fchips" aria-label={t('preview.chip.row')} data-testid="chat-file-chips">
      {files.map(f => (
        <li key={f.path}><FileChip {...f} onOpen={onOpen} /></li>
      ))}
    </ul>
  )
}

export default FileChip
