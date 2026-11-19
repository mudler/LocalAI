import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import '../dock/dock.css'

// The Files button of the chat header, next to the Dock button and drawn as it
// is: it shows and hides the files pane of the chat, and the number is how
// many files the chat holds. The pane and the dock share the right side, so
// one of them is open at a time.
export default function FilesButton({ open, count, onToggle }) {
  const { t } = useTranslation('chat')
  return (
    <button
      type="button"
      className="pd-toggle"
      aria-pressed={open}
      aria-controls={open ? 'chat-files-pane' : undefined}
      title={t('files.buttonTitle')}
      onClick={onToggle}
      data-testid="chat-files-toggle"
    >
      <Icon name="paperclip" className="pd-toggle__icon" />
      <span className="pd-toggle__label">{t('files.button')}</span>
      {/* Keyed by the number, so a new count rises into place. */}
      {count > 0 && <span key={count} className="pd-toggle__count" data-testid="chat-files-count">{count}</span>}
    </button>
  )
}
