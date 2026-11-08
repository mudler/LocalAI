import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useInboxEntry } from '../../contexts/InboxContext'
import Icon from '../Icon'

// What a trigger says to a screen reader: the count, since the badge itself is
// only a picture of it. The words come from nav, which is always loaded.
function useEntryName() {
  const { t } = useTranslation('nav')
  const { unread } = useInboxEntry()
  return unread > 0 ? t('inboxUnread', { count: unread }) : t('items.inbox')
}

// The pinned entry of the sidebar, above Administration. It is a button, not a
// link: it opens the shared panel. onOpen closes the phone drawer first, so the
// inert main area and the sheet do not fight.
export function NavInboxEntry({ collapsed, onOpen }) {
  const { t } = useTranslation('nav')
  const { visible, open, badge, failed, toggle } = useInboxEntry()
  const ref = useRef(null)
  const name = useEntryName()
  if (!visible) return null
  const label = t('items.inbox')
  return (
    <div className="sidebar-nav sidebar-nav--pinned nav-inbox-block">
      <button
        ref={ref}
        type="button"
        className={`nav-item nav-inbox${open ? ' is-open' : ''}`}
        data-testid="nav-inbox"
        data-inbox-trigger=""
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={name}
        title={collapsed ? label : undefined}
        onClick={() => { onOpen?.(); toggle(ref.current) }}
      >
        <Icon name="inbox" className="nav-icon" aria-hidden="true" />
        <span className="nav-label">{label}</span>
        {badge && (
          <span className={`nav-badge${failed ? ' nav-badge--error' : ''}`} data-testid="inbox-count" aria-hidden="true">{badge}</span>
        )}
      </button>
    </div>
  )
}

// The bell of the phone header: the badge stays in view without opening the
// drawer. It opens the same panel, as a sheet.
export function MobileInboxButton() {
  const { t } = useTranslation('nav')
  const { visible, open, badge, failed, toggle } = useInboxEntry()
  const ref = useRef(null)
  const name = useEntryName()
  if (!visible) return null
  return (
    <button
      ref={ref}
      type="button"
      className="mobile-header-btn inbox-bell"
      data-testid="mobile-inbox"
      data-inbox-trigger=""
      aria-haspopup="dialog"
      aria-expanded={open}
      aria-label={name}
      title={t('items.inbox')}
      onClick={() => toggle(ref.current)}
    >
      <Icon name="inbox" />
      {badge && (
        <span key={badge} className={`nav-badge inbox-bell__badge${failed ? ' nav-badge--error' : ''}`} data-testid="mobile-inbox-count" aria-hidden="true">{badge}</span>
      )}
    </button>
  )
}
