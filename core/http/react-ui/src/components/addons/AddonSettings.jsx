/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Callout } from '../ui'
import AddonSlot from '../../addons/AddonSlot'
import { usable } from '../../addons/registryState'
import { chatEgressApi } from '../../utils/api'
import { serverSettingsAddress, serverSettingsFor } from '../../utils/addonSettings'

// The Settings tab of an add-on's details: the add-on's own settings section
// (its settings.sections contribution, drawn here and nowhere else), then the
// server settings of the add-on's feature, which stay on the Settings page and
// are linked from here. Administrators only: the server refuses every change
// to anyone else, and the tab is not offered to them.
export default function AddonSettings({ app, title }) {
  const { t } = useTranslation('addons')
  const context = useMemo(() => ({ addonId: app.id }), [app.id])
  const server = serverSettingsFor(app.id)
  return (
    <div className="ad-settings" data-testid="addon-settings" data-app={app.id}>
      {usable(app)
        ? <AddonSlot slot="settings.sections" context={context} />
        : <Callout tone="info" testId="addon-settings-off">{t('settingsTab.off', { name: title })}</Callout>}
      {server.length > 0 && (
        <section className="ad-settings__server" aria-labelledby="ad-settings-server" data-testid="addon-server-settings">
          <h3 className="ad-section__title" id="ad-settings-server">{t('settingsTab.serverTitle')}</h3>
          <p className="ad-pane__text">{t('settingsTab.serverIntro', { name: title })}</p>
          <ul className="ad-settings__links">
            {server.map(entry => (
              <li key={entry.id} className="ad-settings__link" data-testid="addon-server-setting" data-setting={entry.id}>
                <div className="ad-settings__words">
                  <span className="ad-settings__name">{t(`settingsTab.server.${entry.id}.title`)}</span>
                  {entry.summary === 'egress'
                    ? <EgressSummary />
                    : <span className="ad-quiet">{t(`settingsTab.server.${entry.id}.body`)}</span>}
                </div>
                <Link className="dk-btn dk-btn--ghost dk-btn--sm" to={serverSettingsAddress(entry)} data-testid="addon-server-setting-link">
                  {t('settingsTab.open')} <Icon name="arrow-right" />
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

// What the network of the Sandbox commands is now, read-only: the limit, the
// preset of a new chat, the ports and how long the audit keeps a line.
function EgressSummary() {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', info: null })
  useEffect(() => {
    let live = true
    chatEgressApi.admin()
      .then(info => { if (live) setState({ status: 'ready', info }) })
      .catch(() => { if (live) setState({ status: 'error', info: null }) })
    return () => { live = false }
  }, [])
  if (state.status === 'loading') return <span className="ad-quiet" aria-busy="true">{t('settingsTab.server.network.loading')}</span>
  if (state.status === 'error') return <span className="ad-quiet">{t('settingsTab.server.network.body')}</span>
  const info = state.info || {}
  const preset = (p) => t(`settingsTab.server.network.presets.${['off', 'registries', 'all'].includes(p) ? p : 'off'}`)
  return (
    <dl className="dk-kv ad-kv ad-settings__facts" data-testid="addon-egress-summary">
      <dt>{t('settingsTab.server.network.limit')}</dt><dd>{preset(info.ceiling)}</dd>
      <dt>{t('settingsTab.server.network.newChat')}</dt><dd>{preset(info.default)}</dd>
      <dt>{t('settingsTab.server.network.ports')}</dt><dd className="dk-mono">{(info.ports || []).join(', ') || '80, 443'}</dd>
      <dt>{t('settingsTab.server.network.audit')}</dt><dd>{t('settingsTab.server.network.days', { count: info.audit_days || 30 })}</dd>
    </dl>
  )
}
