import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import './dock.css'

const isMac = () => typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || '')

// The Dock button of the chat header: it shows and hides the dock, as Ctrl+\
// does. A dock with no page opens on its list of pages.
export default function DockButton({ open, onToggle }) {
  const { t } = useTranslation('pages')
  return (
    <button
      type="button"
      className="pd-toggle"
      aria-pressed={open}
      aria-keyshortcuts="Control+Backslash"
      title={t('dock.buttonTitle')}
      onClick={onToggle}
      data-testid="dock-toggle"
    >
      <Icon name="columns" className="pd-toggle__icon" />
      <span className="pd-toggle__label">{t('dock.button')}</span>
      <kbd className="pd-toggle__kbd">{isMac() ? '⌘\\' : 'Ctrl \\'}</kbd>
    </button>
  )
}
