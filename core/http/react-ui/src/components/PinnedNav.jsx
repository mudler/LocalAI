/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo } from 'react'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { useNavigation } from '../context/NavigationContext'
import { useAddonRegistry } from '../addons/useAddonRegistry'
import { navigationItems } from '../addons/registryState'
import { buildTiles, resolvePins } from '../utils/addonTiles'
import { apiUrl } from '../utils/basePath'

// The Pinned section of the sidebar: the caller's pins in the chosen order.
// Each pin is resolved against the built-in catalog and the Apps list at
// render time, so a stopped add-on is muted, and a removed or hidden one is
// not drawn at all. An empty section is not drawn.
export default function PinnedNav({ onClose, collapsed }) {
  const { t } = useTranslation('nav')
  const { t: ta, i18n } = useTranslation('addons')
  const { builtin, pinIds } = useNavigation()
  const registry = useAddonRegistry()
  const { acquire } = registry
  const needsApps = pinIds.some(id => !builtin.some(b => b.id === id))
  // Resolving an add-on pin needs the Apps list once; the registry keeps it
  // current while an add-on contributes UI.
  useEffect(() => (needsApps ? acquire() : undefined), [needsApps, acquire])

  const tiles = useMemo(() => buildTiles(registry.apps, builtin, { language: i18n.language }), [registry.apps, builtin, i18n.language])
  const pinned = useMemo(() => resolvePins(pinIds, tiles), [pinIds, tiles])
  const extra = useMemo(() => navigationItems(registry.apps, { language: i18n.language }), [registry.apps, i18n.language])
  if (pinned.length === 0) return null

  return (
    <div className="sidebar-section" data-testid="pinned-nav">
      {!collapsed && <div className="sidebar-section-title">{t('sections.pinned')}</div>}
      <div className="sidebar-section-items">
        {pinned.map(tile => {
          const title = tile.titleKey ? t(tile.titleKey) : tile.title
          const to = tile.target || tile.detailPath
          const links = extra.filter(x => x.addonId === tile.id && x.path !== tile.target)
          return (
            <div key={tile.id}>
              <NavLink
                to={to}
                onClick={onClose}
                className={({ isActive }) => `nav-item${isActive ? ' active' : ''}${tile.state === 'stopped' ? ' nav-item--muted' : ''}`}
                title={collapsed ? title : undefined}
              >
                {tile.source === 'builtin' || !tile.icon
                  ? <Icon name={tile.iconName || 'puzzle'} className="nav-icon" aria-hidden="true" />
                  : <img className="nav-icon" src={apiUrl(tile.icon)} alt="" width="18" height="18" />}
                <span className="nav-label">{title}</span>
                {tile.state === 'stopped' && <span className="sr-only">{ta('drawer.stopped')}</span>}
              </NavLink>
              {tile.state === 'running' && links.map(link => (
                <NavLink key={link.path} to={link.path} onClick={onClose} className="nav-item nav-item--sub">
                  <span className="nav-label">{link.label}</span>
                </NavLink>
              ))}
            </div>
          )
        })}
      </div>
    </div>
  )
}
