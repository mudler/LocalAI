/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { usePinnedPages } from '../hooks/usePinnedPages'
import { pinnedPages } from '../utils/pages'
import { useArrivals } from '../hooks/useArrivals'

// The pinned pages, drawn as children of the Pages entry in pin order. A pin
// whose page is gone is not drawn. A page pinned while the sidebar is on
// screen opens into its slot with an accent wash (sidebar.css).
export default function PagesPinnedLinks({ pages, onClose }) {
  const { t } = useTranslation('nav')
  const { pins, loaded } = usePinnedPages(true)
  const links = pinnedPages(pages, pins)
  const fresh = useArrivals(links.map(p => p.name), { ready: loaded && Array.isArray(pages) })
  if (links.length === 0) return null
  return (
    <div role="group" aria-label={t('pinnedPages')}>
      {links.map((p) => (
        <div key={p.name} className="nav-pin" data-fresh={fresh.get(p.name)}>
        <NavLink
          to={`/app/pages/${encodeURIComponent(p.name)}`}
          onClick={onClose}
          className={({ isActive }) => `nav-item nav-item--sub${isActive ? ' active' : ''}`}
          data-testid="pages-pinned-link"
          data-page={p.name}
        >
          <Icon name={p.icon || 'file-code'} className="nav-icon" aria-hidden="true" />
          <span className="nav-label">{p.title}</span>
        </NavLink>
        </div>
      ))}
    </div>
  )
}
