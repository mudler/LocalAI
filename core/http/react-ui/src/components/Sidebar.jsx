import { Fragment, useState, useEffect, useRef } from 'react'
import { NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ThemeToggle from './ThemeToggle'
import LanguageSwitcher from './LanguageSwitcher'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'
import { preloadRoute } from '../router'
import { hubs, operateHub, hubEntryPath, hubOwnsPath } from './hub/hubConfig'
import { topDestinations } from '../utils/destinations'
import { primaryActive, primaryDestinations, visiblePrimary } from '../utils/navigationModel'
import { useNavigation } from '../context/NavigationContext'
import { useOperations } from '../hooks/useOperations'
import Icon from './Icon'
import './sidebar.css'
import AppsPinnedLinks from './AppsPinnedLinks'
import PagesPinnedLinks from './PagesPinnedLinks'
import SidebarChats from './SidebarChats'
import { usePages } from '../hooks/usePages'
import { useSharedPages } from '../hooks/useSharedPages'
import { usePinnedPages } from '../hooks/usePinnedPages'
import { useNewLabel } from './wellknown/useNewLabel'

const COLLAPSED_KEY = 'localai_sidebar_collapsed'
const SECTIONS_KEY = 'localai_sidebar_sections'

// The sidebar has two layouts. The primary layout shows Home, Files, Agents,
// MCP connectors, Memory, Models and Add-ons (the entry for the add-ons
// drawer), then the recent chats with New chat and All chats (SidebarChats).
// Chats is not an entry: Home is where a chat starts. Pinned apps are listed
// under Add-ons. Administration (the Operate
// hub) is pinned at the bottom, above the account controls, not in a section.
// With the legacy switch on (features.addon_navigation === false) it shows the
// legacy entries of destinations.js, and Workspace. Operate is a single entry
// that opens a hub whose tab bar lives in hub/hubConfig.js (shared with
// HubLayout). The legacy entries are shared with the 404 page, which lists
// the same destinations.
const topItems = topDestinations

const sections = [
  // Items come from the hubs (hubConfig.js) and carry their own gating.
  { id: 'workspace', titleKey: 'sections.workspace', hubs: true },
]

function NavItem({ item, onClose, collapsed, active, badge }) {
  const { t } = useTranslation('nav')
  const label = t(item.labelKey)
  // Warm the route's lazy chunk before the user clicks. Touch fires ~150ms
  // before the synthetic click on mobile; mouseenter/focus cover desktop and
  // keyboard. The underlying import() is memoised so multiple triggers are free.
  const preload = () => preloadRoute(item.path)
  return (
    <NavLink
      to={item.path}
      end={item.path === '/app'}
      className={({ isActive }) =>
        `nav-item ${(active ?? isActive) ? 'active' : ''}`
      }
      onClick={onClose}
      onMouseEnter={preload}
      onFocus={preload}
      onTouchStart={preload}
      title={collapsed ? label : undefined}
    >
      <Icon name={item.icon} className="nav-icon" aria-hidden="true" />
      <span className="nav-label">{label}</span>
      {badge}
    </NavLink>
  )
}

function loadSectionState() {
  // Tiers render expanded by default (the redesign favours showing the few
  // intent groups up front); users can still collapse any tier and the choice
  // is persisted. Stored values override the defaults so a saved collapse wins.
  const defaults = Object.fromEntries(sections.map(s => [s.id, true]))
  try {
    const stored = localStorage.getItem(SECTIONS_KEY)
    return stored ? { ...defaults, ...JSON.parse(stored) } : defaults
  } catch (_) {
    return defaults
  }
}

function saveSectionState(state) {
  try { localStorage.setItem(SECTIONS_KEY, JSON.stringify(state)) } catch (_) { /* ignore */ }
}

export default function Sidebar({ isOpen, onClose }) {
  const { t } = useTranslation('nav')
  const [features, setFeatures] = useState({})
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem(COLLAPSED_KEY) === 'true' } catch (_) { return false }
  })
  const [openSections, setOpenSections] = useState(loadSectionState)
  const { isAdmin, authEnabled, user, logout, hasFeature } = useAuth()
  const { operations } = useOperations()
  const { builtin } = useNavigation()
  const branding = useBranding()
  const navigate = useNavigate()
  const location = useLocation()
  const closeBtnRef = useRef(null)

  useEffect(() => {
    fetch(apiUrl('/api/features')).then(r => r.json()).then(setFeatures).catch(() => {})
  }, [])

  // Stay in sync with external collapse dispatches (e.g. the chat
  // page's focus mode). The collapse-toggle button still owns the
  // localStorage write — listeners only mirror state, otherwise an
  // outside dispatch would silently overwrite the user's preference.
  useEffect(() => {
    const handler = (e) => {
      const next = !!e.detail?.collapsed
      setCollapsed(prev => (prev === next ? prev : next))
    }
    window.addEventListener('sidebar-collapse', handler)
    return () => window.removeEventListener('sidebar-collapse', handler)
  }, [])

  // Move focus into the drawer when opened on mobile/tablet so keyboard
  // and screen-reader users land inside the dialog. Targeting the close
  // button avoids hijacking the visual focus to a nav item the user may
  // not have meant to activate.
  useEffect(() => {
    if (!isOpen) return
    const id = window.requestAnimationFrame(() => closeBtnRef.current?.focus())
    return () => window.cancelAnimationFrame(id)
  }, [isOpen])

  // Auto-expand section containing the active route
  useEffect(() => {
    for (const section of sections) {
      if (!section.items) continue
      const match = section.items.some(item => location.pathname.startsWith(item.path))
      if (match && !openSections[section.id]) {
        setOpenSections(prev => {
          const next = { ...prev, [section.id]: true }
          saveSectionState(next)
          return next
        })
      }
    }
  }, [location.pathname])

  const toggleCollapse = () => {
    // Side effects (persist + broadcast) live in the handler body, never inside
    // the setState updater: StrictMode double-invokes updaters in dev, and the
    // synchronous sidebar-collapse dispatch re-entered setState from the
    // listeners mid-update, so the toggle silently no-op'd in dev builds.
    const next = !collapsed
    try { localStorage.setItem(COLLAPSED_KEY, String(next)) } catch (_) { /* ignore */ }
    setCollapsed(next)
    window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: next } }))
  }

  const toggleSection = (id) => {
    setOpenSections(prev => {
      const next = { ...prev, [id]: !prev[id] }
      saveSectionState(next)
      return next
    })
  }

  const filterItem = (item) => {
    if (item.adminOnly && !isAdmin) return false
    if (item.authOnly && !authEnabled) return false
    if (item.feature && features[item.feature] === false) return false
    if (item.feature && !hasFeature(item.feature)) return false
    return true
  }

  const visibleTopItems = topItems.filter(filterItem)
  // Shared shape for the hub gating helpers (hubConfig.js).
  const auth = { isAdmin, authEnabled, hasFeature, features }
  // The previous sidebar returns when the operator sets the legacy switch.
  const legacy = features.addon_navigation === false
  const hasBuiltin = builtin.length > 0
  // The Pages entry shows once the user has a page; the list is read only for
  // a user who may have pages at all.
  const pagesOn = !legacy && hasFeature('chats') && hasFeature('pages')
  const { pages } = usePages(pagesOn)
  // Pages of shared folders count too: they are reached from the Pages screen.
  const { pages: shared } = useSharedPages(pagesOn)
  const hasPages = pagesOn && (pages.length > 0 || shared.length > 0)
  const { pins } = usePinnedPages(hasPages)
  // A pinned page lights up its own link under Pages, not the Pages entry too.
  const pagesActive = (pathname) => {
    if (pathname === '/app/pages') return true
    const m = /^\/app\/pages\/([^/]+)/.exec(pathname)
    return Boolean(m) && !pins.includes(decodeURIComponent(m[1]))
  }
  // The one-time "New" label of the Pages entry, kept in user.json.
  const pagesNew = useNewLabel('pages', { enabled: hasPages, visiting: location.pathname.startsWith('/app/pages') })
  const navAuth = { isAdmin, hasFeature, hasBuiltin, features, hasPages }
  const primary = visiblePrimary(primaryDestinations, navAuth)

  // One badge, on the always-visible sidebar entry. The Operate tab bar only
  // exists while the user is on an Operate route, so badging a tab instead
  // would let the count disappear entirely.
  const failedOps = operations.filter((op) => op.error).length
  const activeOps = operations.length

  // Workspace lists one entry per hub the viewer can use; its target is the
  // hub's overview.
  const hubEntry = (hub) => {
    const path = hubEntryPath(hub, auth)
    return path ? { path, icon: hub.icon, labelKey: hub.titleKey, hub } : null
  }
  // Administration is not part of Workspace: it is pinned at the bottom.
  const getVisibleSectionItems = (section) => {
    if (!section.hubs) return section.items.filter(filterItem)
    return hubs.filter(hub => hub.id !== operateHub.id).flatMap(hub => hubEntry(hub) ?? [])
  }
  const adminItem = hubEntry(operateHub)
  const operateBadge = activeOps > 0 ? (
    <span className={`nav-badge${failedOps > 0 ? ' nav-badge--error' : ''}`}>
      {failedOps > 0 ? failedOps : activeOps}
    </span>
  ) : null

  return (
    <>
      {isOpen && <div className="sidebar-overlay" onClick={onClose} />}

      <aside
        id="app-sidebar"
        className={`sidebar ${isOpen ? 'open' : ''} ${collapsed ? 'collapsed' : ''}`}
        aria-label={t('primaryNavigation')}
      >
        {/* Logo */}
        <div className="sidebar-header">
          <a href="./" className="sidebar-logo-link">
            <img src={apiUrl(branding.logoHorizontalUrl)} alt={branding.instanceName} className="sidebar-logo-img" />
          </a>
          <a href="./" className="sidebar-logo-icon" title={branding.instanceName}>
            <img src={apiUrl(branding.logoUrl)} alt={branding.instanceName} className="sidebar-logo-icon-img" />
          </a>
          <button
            ref={closeBtnRef}
            className="sidebar-close-btn"
            onClick={onClose}
            aria-label={t('closeMenu')}
          >
            <Icon name="close" />
          </button>
        </div>

        {/* Navigation */}
        <nav className="sidebar-nav">
          {/* Top-level items */}
          <div className="sidebar-section">
            {(legacy ? visibleTopItems : [...visibleTopItems.filter(i => i.path === '/app'), ...primary]).map(item => (
              <Fragment key={item.path}>
                <NavItem
                  item={item} onClose={onClose} collapsed={collapsed}
                  active={legacy || item.path === '/app' ? undefined : item.pagesEntry ? pagesActive(location.pathname) : primaryActive(item, location.pathname)}
                  badge={item.pagesEntry && pagesNew && !collapsed ? <span className="nav-new" data-testid="nav-pages-new">{t('new')}</span> : undefined}
                />
                {/* The Apps group: the pinned apps in pin order, under the Apps entry. */}
                {!legacy && item.addonsEntry && features.addons !== false && !collapsed && <AppsPinnedLinks onClose={onClose} />}
                {/* Pinned pages, in pin order, under the Pages entry. */}
                {item.pagesEntry && !collapsed && <PagesPinnedLinks pages={pages} onClose={onClose} />}
              </Fragment>
            ))}
          </div>

          {/* Recent chats, New chat and All chats. */}
          {!legacy && hasFeature('chats') && features.chats !== false && (
            <SidebarChats onClose={onClose} collapsed={collapsed} />
          )}

          {/* Collapsible sections */}
          {(legacy ? sections : sections.filter(s => s.hubs)).map(section => {
            const visibleItems = getVisibleSectionItems(section)
            if (visibleItems.length === 0) return null

            const isSectionOpen = openSections[section.id]
            const showItems = isSectionOpen || collapsed
            const sectionTitle = t(section.titleKey)

            return (
              <div key={section.id} className="sidebar-section">
                <button
                  className={`sidebar-section-title sidebar-section-toggle ${isSectionOpen ? 'open' : ''}`}
                  onClick={() => toggleSection(section.id)}
                  title={collapsed ? sectionTitle : undefined}
                >
                  <span>{sectionTitle}</span>
                  <Icon name="chevron-right" className="sidebar-section-chevron" />
                </button>
                {showItems && (
                  <div className="sidebar-section-items">
                    {visibleItems.map(item => (
                      <NavItem
                        key={item.path}
                        item={item}
                        onClose={onClose}
                        collapsed={collapsed}
                        active={item.hub ? hubOwnsPath(item.hub, location.pathname, auth) : undefined}
                      />
                    ))}
                  </div>
                )}
              </div>
            )
          })}

        </nav>

        {/* Administration: pinned above the footer so it stays in reach when the list scrolls. */}
        {adminItem && (
          <nav className="sidebar-nav sidebar-nav--pinned" aria-label={t('sections.operate')}>
            <NavItem
              item={adminItem}
              onClose={onClose}
              collapsed={collapsed}
              active={hubOwnsPath(adminItem.hub, location.pathname, auth)}
              badge={operateBadge}
            />
          </nav>
        )}

        {/* Footer */}
        <div className="sidebar-footer">
          {authEnabled && user && (
            <div className="sidebar-user" title={collapsed ? (user.name || user.email) : undefined}>
              <button
                className="sidebar-user-link"
                onClick={() => { navigate('/app/account'); onClose?.() }}
                onMouseEnter={() => preloadRoute('/app/account')}
                onFocus={() => preloadRoute('/app/account')}
                onTouchStart={() => preloadRoute('/app/account')}
                title={t('accountSettings')}
              >
                {user.avatarUrl ? (
                  <img src={user.avatarUrl} alt="" className="sidebar-user-avatar" />
                ) : (
                  <Icon name="user" className="sidebar-user-avatar-icon" />
                )}
                <span className="nav-label sidebar-user-name">{user.name || user.email}</span>
              </button>
              <button className="sidebar-logout-btn" onClick={logout} title={t('logout')}>
                <Icon name="log-out" />
              </button>
            </div>
          )}
          {!authEnabled && hasFeature('chats') && (
            <div className="sidebar-user">
              <button
                className="sidebar-user-link"
                data-testid="sidebar-your-settings"
                onClick={() => { navigate('/app/account'); onClose?.() }}
                onMouseEnter={() => preloadRoute('/app/account')}
                onFocus={() => preloadRoute('/app/account')}
                title={t('yourSettings')}
              >
                <Icon name="user-settings" className="sidebar-user-avatar-icon" />
                <span className="nav-label sidebar-user-name">{t('yourSettings')}</span>
              </button>
            </div>
          )}
          <LanguageSwitcher />
          <ThemeToggle />
          <button
            className="sidebar-collapse-btn"
            onClick={toggleCollapse}
            title={collapsed ? t('expandSidebar') : t('collapseSidebar')}
          >
            <Icon name={`chevron-${collapsed ? 'right' : 'left'}`} />
          </button>
        </div>
      </aside>
    </>
  )
}
