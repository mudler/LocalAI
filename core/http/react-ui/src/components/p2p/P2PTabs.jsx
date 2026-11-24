/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

export const P2P_PATH = '/app/p2p'
export const P2P_SETTINGS_PATH = '/app/p2p/settings'
export const P2P_FEDERATION_PATH = '/app/p2p/federation'

const TABS = [
  { path: P2P_PATH, key: 'devices', exact: true },
  { path: P2P_FEDERATION_PATH, key: 'federation' },
  { path: P2P_SETTINGS_PATH, key: 'settings' },
]

// The tabs of Swarm, P2P: the paired devices and the invites, the federation
// groups, and the settings of the P2P layer. The pages render it at the top, so both show the
// same row; it reuses the look of the hub's second row.
export default function P2PTabs() {
  const { t } = useTranslation('p2p')
  const { pathname } = useLocation()
  return (
    <nav className="hub-subnav p2p-tabs" aria-label={t('tabs.label')} data-testid="p2p-tabs">
      {TABS.map(tab => {
        const active = tab.exact ? pathname === tab.path : pathname.startsWith(tab.path)
        return (
          <Link key={tab.path} to={tab.path} className="hub-subnav__link" aria-current={active ? 'page' : undefined}>
            {t(`tabs.${tab.key}`)}
          </Link>
        )
      })}
    </nav>
  )
}
