import { useTheme } from '../contexts/ThemeContext'

export default function ThemeToggle() {
  const { theme, toggleTheme } = useTheme()

  return (
    <button
      onClick={toggleTheme}
      className="theme-toggle"
      title={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`}
    >
      <i className={`fas ${theme === 'dark' ? 'fa-sun' : 'fa-moon'}`} />
    </button>
  )
}
