/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { useRecentChats } from '../hooks/useRecentChats'
import { allChatsDestination } from '../utils/navigationModel'
import { preloadRoute } from '../router'

// How many chats the sidebar lists. All chats has the rest.
export const RECENT_CHATS = 8

// The chats part of the sidebar, under the primary items: "Recent chats" with
// a New chat button (it opens Home, where a chat starts), the newest chats,
// and All chats. Chats is not a destination of its own: Home starts a chat,
// a row opens its transcript, All chats keeps the history.
//
// In the collapsed rail the titles do not fit, so only the All chats icon
// stays (sidebar.css); the phone drawer shows the whole list.
export default function SidebarChats({ onClose, collapsed }) {
  const { t } = useTranslation('nav')
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const chats = useRecentChats(true, RECENT_CHATS, pathname)
  const allLabel = t(allChatsDestination.labelKey)
  const preload = () => preloadRoute('/app/chats')

  return (
    <div className="sidebar-section sidebar-chats" role="group" aria-labelledby="sidebar-chats-title" data-testid="sidebar-chats">
      <div className="sidebar-chats__head">
        <span id="sidebar-chats-title" className="sidebar-section-title">{t('recentChats')}</span>
        <button
          type="button"
          className="sidebar-chats__new"
          onClick={() => { navigate('/app'); onClose?.() }}
          aria-label={t('newChat')}
          title={t('newChat')}
          data-testid="sidebar-new-chat"
        >
          <Icon name="plus" aria-hidden="true" />
        </button>
      </div>
      {chats.map(c => (
        <NavLink
          key={c.id}
          to={`/app/chats/${encodeURIComponent(c.id)}`}
          onClick={onClose}
          onMouseEnter={preload}
          onFocus={preload}
          onTouchStart={preload}
          className={({ isActive }) => `nav-item nav-item--sub nav-chat${isActive ? ' active' : ''}`}
          data-testid="sidebar-chat"
        >
          <span className="nav-label">{c.name || t('untitledChat')}</span>
          {c.running && <Icon name="spinner" spin className="nav-chat__busy" aria-label={t('chatRunning')} />}
        </NavLink>
      ))}
      <NavLink
        to={allChatsDestination.path}
        end
        onClick={onClose}
        className={({ isActive }) => `nav-item nav-chat-all${isActive ? ' active' : ''}`}
        title={collapsed ? allLabel : undefined}
        data-testid="sidebar-all-chats"
      >
        <Icon name={allChatsDestination.icon} className="nav-icon" aria-hidden="true" />
        <span className="nav-label">{allLabel}</span>
      </NavLink>
    </div>
  )
}
