/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { useAutomatedChats, useRecentChats } from '../hooks/useRecentChats'
import { attention } from '../utils/originChats'
import { allChatsDestination } from '../utils/navigationModel'
import { preloadRoute } from '../router'
import { useUiString } from './wellknown/Identity'

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
  // The chats that no person started are not listed here (All chats has them
  // on a tab of their own); the entry only says when one needs the person.
  const automated = useAutomatedChats(true, pathname)
  const { waiting, running } = attention(automated.chats, automated.needsYou)
  // The user's own text for these entries (ui.json strings).
  const uiString = useUiString()
  const allLabel = uiString('nav.allChats', t(allChatsDestination.labelKey))
  const newChat = uiString('nav.newChat', t('newChat'))
  const badge = waiting > 0
    ? t('automatedWaiting', { count: waiting })
    : running > 0 ? t('automatedRunning', { count: running }) : ''
  const preload = () => preloadRoute('/app/chats')

  return (
    <div className="sidebar-section sidebar-chats" role="group" aria-labelledby="sidebar-chats-title" data-testid="sidebar-chats">
      <div className="sidebar-chats__head">
        <span id="sidebar-chats-title" className="sidebar-section-title">{uiString('nav.recentChats', t('recentChats'))}</span>
        <button
          type="button"
          className="sidebar-chats__new"
          onClick={() => { navigate('/app'); onClose?.() }}
          aria-label={newChat}
          title={newChat}
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
        {waiting > 0 && <span className="nav-chat-all__badge" data-testid="sidebar-automated-badge" title={badge} aria-label={badge}>{waiting > 9 ? '9+' : waiting}</span>}
        {waiting === 0 && running > 0 && <span className="nav-chat-all__dot" data-testid="sidebar-automated-running" title={badge} role="img" aria-label={badge} />}
      </NavLink>
    </div>
  )
}
