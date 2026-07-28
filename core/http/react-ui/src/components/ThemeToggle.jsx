import { useTranslation } from 'react-i18next'
import { useTheme } from '../contexts/ThemeContext'

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
      <i key={theme} className={`fas ${theme === 'dark' ? 'fa-sun' : 'fa-moon'} theme-toggle__icon`} aria-hidden="true" />
    </button>
  )
}
