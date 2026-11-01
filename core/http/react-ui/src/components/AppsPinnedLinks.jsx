/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo } from 'react'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { useNavigation } from '../context/NavigationContext'
import { useAddonRegistry } from '../addons/useAddonRegistry'
import { buildTiles, sidebarPinLinks } from '../utils/addonTiles'
import { apiUrl } from '../utils/basePath'
import { useArrivals } from '../hooks/useArrivals'

// The caller's pinned apps, drawn as children of the Apps entry in pin order.
// The apps come from the add-on registry, which the drawer feeds on every list
// read, so the links follow the drawer. A pin without an installed app is not
// drawn, and nothing is drawn when nothing is pinned.
export default function AppsPinnedLinks({ onClose }) {
  const { t } = useTranslation('nav')
  const { i18n } = useTranslation('addons')
  const { builtin, pinIds } = useNavigation()
  const registry = useAddonRegistry()
  const { acquire } = registry
  const needsApps = pinIds.some(id => !builtin.some(b => b.id === id))
  // Resolving an app pin needs the apps list once; the registry keeps it current.
  useEffect(() => (needsApps ? acquire() : undefined), [needsApps, acquire])

  const links = useMemo(
    () => sidebarPinLinks(pinIds, buildTiles(registry.apps, builtin, { language: i18n.language })),
    [pinIds, registry.apps, builtin, i18n.language],
  )

  // An app pinned while the sidebar is on screen opens into its slot.
  const fresh = useArrivals(links.map(l => l.id), { ready: !needsApps || registry.apps.length > 0 })

  return links.map(link => {
    const title = link.titleKey ? t(link.titleKey) : link.title
    return (
      <div key={link.id} className="nav-pin" data-fresh={fresh.get(link.id)}>
      <NavLink
        to={link.to}
        onClick={onClose}
        className={({ isActive }) => `nav-item nav-item--sub${isActive ? ' active' : ''}${link.stopped ? ' nav-item--muted' : ''}`}
        data-testid="apps-pinned-link"
        data-app={link.id}
      >
        {link.source === 'builtin' || !link.icon
          ? <Icon name={link.iconName || 'puzzle'} className="nav-icon" aria-hidden="true" />
          : <img className="nav-icon" src={apiUrl(link.icon)} alt="" width="18" height="18" />}
        <span className="nav-label">{title}</span>
      </NavLink>
      </div>
    )
  })
}
