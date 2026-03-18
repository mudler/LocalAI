import { useState } from 'react'

/**
 * UserGroupSection — collapsible section showing other users' resources.
 *
 * Props:
 *   title        — e.g. "Other Users' Agents"
 *   userGroups   — { [userId]: { agents: [...], skills: [...], etc } }
 *   userMap      — { [userId]: { name, email, avatarUrl } }
 *   currentUserId — current user's ID (excluded from display)
 *   renderGroup  — (items, userId) => JSX — renders the items for one user
 *   itemKey      — key in the group object to count items (e.g. "agents", "skills")
 */
export default function UserGroupSection({ title, userGroups, userMap, currentUserId, renderGroup, itemKey }) {
  const [open, setOpen] = useState(false)

  if (!userGroups || Object.keys(userGroups).length === 0) return null

  const userIds = Object.keys(userGroups).filter(id => id !== currentUserId)
  if (userIds.length === 0) return null

  const totalUsers = userIds.length

  return (
    <div style={{ marginTop: 'var(--spacing-lg)' }}>
      <style>{`
        .ugs-header {
          display: flex;
          align-items: center;
          gap: var(--spacing-sm);
          cursor: pointer;
          padding: var(--spacing-sm) 0;
          border-top: 1px solid var(--color-border-subtle);
          user-select: none;
        }
        .ugs-header:hover { opacity: 0.8; }
        .ugs-chevron {
          transition: transform 0.2s;
          font-size: 0.75rem;
          color: var(--color-text-muted);
        }
        .ugs-chevron.open { transform: rotate(90deg); }
        .ugs-title {
          font-weight: 600;
          font-size: 0.875rem;
          color: var(--color-text-secondary);
        }
        .ugs-badge {
          font-size: 0.75rem;
          background: var(--color-bg-tertiary);
          color: var(--color-text-muted);
          padding: 2px 8px;
          border-radius: var(--radius-sm);
        }
        .ugs-content {
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-lg);
          padding: var(--spacing-md);
          margin-top: var(--spacing-sm);
        }
        .ugs-user-section {
          margin-bottom: var(--spacing-md);
        }
        .ugs-user-section:last-child { margin-bottom: 0; }
        .ugs-user-header {
          display: flex;
          align-items: center;
          gap: var(--spacing-sm);
          margin-bottom: var(--spacing-sm);
          cursor: pointer;
        }
        .ugs-avatar {
          width: 24px;
          height: 24px;
          border-radius: 50%;
          background: var(--color-primary);
          color: white;
          display: flex;
          align-items: center;
          justify-content: center;
          font-size: 0.6875rem;
          font-weight: 600;
          flex-shrink: 0;
        }
        .ugs-avatar img {
          width: 100%;
          height: 100%;
          border-radius: 50%;
          object-fit: cover;
        }
        .ugs-user-name {
          font-weight: 500;
          font-size: 0.8125rem;
        }
        .ugs-user-count {
          font-size: 0.75rem;
          color: var(--color-text-muted);
        }
      `}</style>

      <div
        className="ugs-header"
        role="button"
        tabIndex={0}
        onClick={() => setOpen(v => !v)}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen(v => !v) } }}
        aria-expanded={open}
      >
        <i className={`fas fa-chevron-right ugs-chevron ${open ? 'open' : ''}`} />
        <span className="ugs-title">{title}</span>
        <span className="ugs-badge">{totalUsers} user{totalUsers !== 1 ? 's' : ''}</span>
      </div>

      {open && (
        <div className="ugs-content">
          {userIds.map(uid => {
            const user = userMap[uid] || {}
            const displayName = user.name || user.email || uid.slice(0, 8) + '...'
            const initials = (displayName[0] || '?').toUpperCase()
            const group = userGroups[uid]
            const items = itemKey ? group[itemKey] : group
            const count = Array.isArray(items) ? items.length : 0

            return (
              <UserSubSection
                key={uid}
                uid={uid}
                displayName={displayName}
                initials={initials}
                avatarUrl={user.avatarUrl}
                count={count}
                itemKey={itemKey}
              >
                {renderGroup(items, uid)}
              </UserSubSection>
            )
          })}
        </div>
      )}
    </div>
  )
}

function UserSubSection({ uid, displayName, initials, avatarUrl, count, itemKey, children }) {
  const [open, setOpen] = useState(true)

  return (
    <div className="ugs-user-section">
      <div
        className="ugs-user-header"
        role="button"
        tabIndex={0}
        onClick={() => setOpen(v => !v)}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen(v => !v) } }}
        aria-expanded={open}
      >
        <i className={`fas fa-chevron-right ugs-chevron ${open ? 'open' : ''}`} style={{ fontSize: '0.625rem' }} />
        <div className="ugs-avatar">
          {avatarUrl ? <img src={avatarUrl} alt="" /> : initials}
        </div>
        <span className="ugs-user-name">{displayName}</span>
        <span className="ugs-user-count">
          {count} {itemKey || 'item'}{count !== 1 ? 's' : ''}
        </span>
      </div>
      {open && children}
    </div>
  )
}
