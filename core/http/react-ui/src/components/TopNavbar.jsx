import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { useDeployment } from '../contexts/DeploymentContext'
import { useTheme } from '../contexts/ThemeContext'
import { launchAssistantChat } from '../utils/launchAssistantChat'
import TokenUsageMeter from './navbar/TokenUsageMeter'

// Desktop top bar. Complementary to the mobile-only header in App.jsx: this is
// hidden on small screens (see .top-navbar CSS) and shows deployment/admin
// affordances on wide screens where the sidebar footer is far from the content.
export default function TopNavbar() {
  const { t } = useTranslation('nav')
  const navigate = useNavigate()
  const { isAdmin, authEnabled, user } = useAuth()
  const { features, distributed, p2pEnabled } = useDeployment()
  const { theme, toggleTheme } = useTheme()

  const modeLabel = distributed
    ? t('topbar.modeDistributed')
    : p2pEnabled
      ? t('topbar.modeSwarm')
      : t('topbar.modeSingle')

  const showAssistantJump = isAdmin && !!features.localai_assistant
  const showAvatar = authEnabled && user
  const themeLabel = theme === 'dark' ? t('switchToLightMode') : t('switchToDarkMode')

  return (
    <div className="top-navbar" role="navigation" aria-label={t('topbar.label')}>
      <div className="top-navbar__left">
        {isAdmin && (
          <span className={`top-navbar__mode ${distributed || p2pEnabled ? 'is-active' : ''}`}>
            <i className="fas fa-circle-nodes" aria-hidden="true" /> {modeLabel}
          </span>
        )}
      </div>
      <div className="top-navbar__right">
        {!isAdmin && (
          <button
            type="button"
            className="top-navbar__btn"
            onClick={() => navigate('/app/chat')}
            title={t('topbar.pickModel')}
          >
            <i className="fas fa-cube" aria-hidden="true" /> {t('topbar.pickModel')}
          </button>
        )}
        {showAssistantJump && (
          <button
            type="button"
            className="top-navbar__btn top-navbar__assistant"
            onClick={() => launchAssistantChat(navigate)}
            title={t('topbar.adminViaChat')}
          >
            <i className="fas fa-user-shield" aria-hidden="true" /> {t('topbar.adminViaChat')}
          </button>
        )}
        {isAdmin && <TokenUsageMeter />}
        {isAdmin && (
          <button
            type="button"
            className="top-navbar__icon"
            onClick={() => navigate('/app/settings')}
            aria-label={t('items.settings')}
            title={t('items.settings')}
          >
            <i className="fas fa-cog" aria-hidden="true" />
          </button>
        )}
        <button
          type="button"
          className="top-navbar__icon"
          onClick={toggleTheme}
          aria-label={themeLabel}
          title={themeLabel}
        >
          <i className={`fas ${theme === 'dark' ? 'fa-sun' : 'fa-moon'}`} aria-hidden="true" />
        </button>
        {showAvatar && (
          <button
            type="button"
            className="top-navbar__icon top-navbar__avatar"
            onClick={() => navigate('/app/account')}
            aria-label={user.name || user.email}
            title={user.name || user.email}
          >
            {user.avatarUrl
              ? <img src={user.avatarUrl} alt="" />
              : <i className="fas fa-user-circle" aria-hidden="true" />}
          </button>
        )}
      </div>
    </div>
  )
}
