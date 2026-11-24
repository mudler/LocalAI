import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useInboxEntry } from '../../contexts/InboxContext'
import Icon from '../Icon'
import OpsRing from './OpsRing'

// What a trigger says to a screen reader: the count, since the badge itself is
// only a picture of it, and what the operations are doing, since so is the
// ring. The words come from nav, which is always loaded.
function useEntryName() {
  const { t } = useTranslation('nav')
  const { unread, ops, opsOnly } = useInboxEntry()
  const parts = [opsOnly ? t('activityEntry') : unread > 0 ? t('inboxUnread', { count: unread }) : t('items.inbox')]
  if (ops.count > 0) parts.push(t('inboxOps', { count: ops.count }))
  if (ops.failed) parts.push(t('inboxOpsFailed', { count: ops.failedCount }))
  return parts.join(', ')
}

// The icon of a trigger. While operations run or one failed it sits in a
// progress ring (red, with a dot, after a failure); otherwise it is the plain
// icon, exactly as before. The ring is a picture: the entry's name carries the
// words.
function EntryIcon({ ops, testId, navIcon = false }) {
  const icon = <Icon name="inbox" className={navIcon ? 'nav-icon' : undefined} aria-hidden="true" />
  if (!ops.active && !ops.failed) return icon
  return (
    <span className={`ops-iconwrap${ops.failed ? ' is-failed' : ''}`} data-testid={testId} data-failed={ops.failed ? 'true' : undefined} data-progress={ops.value ?? undefined}>
      <OpsRing value={ops.active ? ops.value : 100} failed={ops.failed} className="ops-ring--entry" />
      {icon}
      {ops.failed && <span className="ops-iconwrap__dot" data-testid={`${testId}-dot`} />}
    </span>
  )
}

// The pinned entry of the sidebar, above Administration. It is a button, not a
// link: it opens the shared panel. onOpen closes the phone drawer first, so the
// inert main area and the sheet do not fight.
export function NavInboxEntry({ collapsed, onOpen }) {
  const { t } = useTranslation('nav')
  const { visible, open, badge, failed, ops, opsOnly, toggle } = useInboxEntry()
  const ref = useRef(null)
  const name = useEntryName()
  if (!visible) return null
  const label = opsOnly ? t('activityEntry') : t('items.inbox')
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
        <EntryIcon ops={ops} testId="inbox-ring" navIcon />
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
  const { visible, open, badge, failed, ops, opsOnly, toggle } = useInboxEntry()
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
      title={opsOnly ? t('activityEntry') : t('items.inbox')}
      onClick={() => toggle(ref.current)}
    >
      <EntryIcon ops={ops} testId="mobile-inbox-ring" />
      {badge && (
        <span key={badge} className={`nav-badge inbox-bell__badge${failed ? ' nav-badge--error' : ''}`} data-testid="mobile-inbox-count" aria-hidden="true">{badge}</span>
      )}
    </button>
  )
}
