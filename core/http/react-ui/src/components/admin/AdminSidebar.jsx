import { useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { apiUrl } from '../../utils/basePath'
import { preloadRoute } from '../../router'
import { useBranding } from '../../contexts/BrandingContext'
import { useOperateSummary } from '../../contexts/OperateSummaryContext'
import { operateHub, resolveGroups } from '../hub/hubConfig'
import { openAdminPalette } from './adminEvents'
import { isApplePlatform } from './paletteModel'
import './admin.css'

// The counts at the end of an item or a folded group. A value that is zero or
// unknown draws nothing. Colour is never the only cue: the text is the number.
function Signal({ signal, summary }) {
  const value = signal && summary?.signals ? summary.signals[signal.key] : null
  if (value == null) return null
  const level = signal.kind === 'attention' ? 'attn'
    : signal.kind === 'error' ? 'error'
      : signal.kind === 'update' ? (summary.signals.updatesLevel === 'error' ? 'error' : 'update')
        : 'quiet'
  return (
    <span className="nav-badge admin-badge" data-level={level} data-testid={`admin-signal-${signal.key}`}>
      <span aria-hidden="true">{value}</span>
      <span className="sr-only">{value}</span>
    </span>
  )
}

function itemHref(item) {
  return item.query ? `${item.path}?${item.query}` : item.path
}

// The sidebar of Administration. It replaces the main sidebar's list while the
// viewer is on an Administration page: a way back, the search, then the groups
// of hubConfig.js (the one place that says which page exists when). The group
// of the current page is open; a folded group (Traffic) opens only for one of
// its pages or by hand.
export default function AdminSidebar({ auth, collapsed, onClose, closeBtnRef }) {
  const { t } = useTranslation('nav')
  const location = useLocation()
  const branding = useBranding()
  const summary = useOperateSummary()
  const [opened, setOpened] = useState({})
  const { groups, current, owner } = resolveGroups(operateHub, auth, location)
  const external = (operateHub.external || []).filter(item => !item.adminOnly || auth.isAdmin)
  const shortcut = isApplePlatform() ? '⌘ K' : 'Ctrl K'

  return (
    <>
      <div className="sidebar-header admin-header">
        <NavLink
          to="/app"
          className="admin-back"
          onClick={onClose}
          onMouseEnter={() => preloadRoute('/app')}
          aria-label={t('admin.nav.back', { name: branding.instanceName })}
          title={collapsed ? t('admin.nav.back', { name: branding.instanceName }) : undefined}
        >
          <Icon name="arrow-left" className="nav-icon" aria-hidden="true" />
          <span className="nav-label">{branding.instanceName}</span>
        </NavLink>
        <button ref={closeBtnRef} className="sidebar-close-btn" onClick={onClose} aria-label={t('closeMenu')}>
          <Icon name="close" />
        </button>
      </div>

      <div className="admin-search-row">
        <button
          type="button"
          className="admin-search"
          data-testid="admin-search"
          aria-haspopup="dialog"
          aria-keyshortcuts="Control+K Meta+K"
          title={collapsed ? t('admin.nav.search') : undefined}
          onClick={() => { onClose?.(); openAdminPalette() }}
        >
          <Icon name="search" aria-hidden="true" />
          <span className="admin-search__label">{t('admin.nav.search')}</span>
          <kbd className="admin-search__key">{shortcut}</kbd>
        </button>
      </div>

      <nav className="sidebar-nav admin-nav" aria-label={t('admin.nav.label')} data-testid="admin-nav">
        <p className="admin-nav__title">
          <Icon name="sliders" aria-hidden="true" />
          <span className="nav-label">{t('sections.operate')}</span>
        </p>
        {groups.map(({ group, items }) => {
          const isOwner = owner === group
          const open = !group.folded || isOwner || opened[group.id] === true
          const title = group.labelKey ? t(group.labelKey) : null
          const bodyId = `admin-group-${group.id}`
          return (
            <div
              key={group.id}
              className="sidebar-section admin-group"
              data-group={group.id}
              data-open={open ? 'true' : 'false'}
              data-divider={group.divider ? 'true' : undefined}
              {...(title && !group.folded ? { role: 'group', 'aria-label': title } : {})}
            >
              {title && group.folded && (
                <button
                  type="button"
                  className={`sidebar-section-title sidebar-section-toggle ${open ? 'open' : ''}`}
                  aria-expanded={open}
                  aria-controls={bodyId}
                  // The group of the current page cannot be folded away.
                  disabled={isOwner}
                  onClick={() => setOpened(state => ({ ...state, [group.id]: !open }))}
                >
                  <span>{title}</span>
                  {!open && <Signal signal={group.signal} summary={summary} />}
                  <Icon name="chevron-right" className="sidebar-section-chevron" aria-hidden="true" />
                </button>
              )}
              {title && !group.folded && <div className="sidebar-section-title" aria-hidden="true">{title}</div>}
              <div className="sidebar-section-items" id={bodyId}>
                {items.map(item => {
                  const active = current?.item === item
                  const label = t(item.labelKey)
                  return (
                    <Link
                      key={`${item.path}?${item.query || ''}`}
                      to={itemHref(item)}
                      className={`nav-item ${active ? 'active' : ''}`}
                      aria-current={active ? 'page' : undefined}
                      data-admin-item={item.path}
                      onClick={onClose}
                      onMouseEnter={() => preloadRoute(item.path)}
                      onFocus={() => preloadRoute(item.path)}
                      onTouchStart={() => preloadRoute(item.path)}
                      title={collapsed ? label : undefined}
                    >
                      <Icon name={item.icon} className="nav-icon" aria-hidden="true" />
                      <span className="nav-label">{label}</span>
                      <Signal signal={item.signal} summary={summary} />
                    </Link>
                  )
                })}
              </div>
            </div>
          )
        })}
        {external.length > 0 && (
          <div className="sidebar-section admin-group admin-group--external">
            {external.map(item => (
              <a
                key={item.href}
                className="nav-item admin-external"
                href={apiUrl(item.href)}
                target="_blank"
                rel="noopener noreferrer"
                title={collapsed ? t(item.labelKey) : undefined}
              >
                <Icon name={item.icon} className="nav-icon" aria-hidden="true" />
                <span className="nav-label">{t(item.labelKey)}</span>
              </a>
            ))}
          </div>
        )}
      </nav>
    </>
  )
}
