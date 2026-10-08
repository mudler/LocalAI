import { useTranslation } from 'react-i18next'
import { useTheme } from '../contexts/ThemeContext'
import Icon from './Icon'

export default function ThemeToggle() {
  const { theme, toggleTheme } = useTheme()
  const { t } = useTranslation('nav')
  const label = theme === 'dark' ? t('switchToLightMode') : t('switchToDarkMode')

  return (
    <button
      onClick={toggleTheme}
      className="theme-toggle"
      title={label}
      aria-label={label}
    >
      {/* key on theme so the icon remounts and replays the rotate/fade */}
      <Icon name={theme === 'dark' ? 'sun' : 'moon'} className="theme-toggle__icon" key={theme} />
    </button>
  )
}
