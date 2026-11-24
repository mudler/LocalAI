/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, StatusChip } from '../ui'
import { apiUrl } from '../../utils/basePath'
import { AppIcon } from './AddonBits'
import { addonPageAddress } from '../../addons/addresses'
import { connectionLine, rowState } from '../../utils/chatConnectors'

const STATE_CHIP = { running: { status: 'running' }, off: { tone: 'idle' }, attention: { tone: 'warn' } }

// The connections of an add-on, in its details. Each row has the app icon, the
// name, the facts and one state; one button leads to the add-on page where
// connections are added and edited. Nothing shows when it has no connections
// and no connector kind.
export default function ConnectionList({ app, connections, agents, kinds }) {
  const { t } = useTranslation('addons')
  const kind = (kinds || []).find(k => k.addon === app.id)
  if (!kind && connections.length === 0) return null
  const href = kind && kind.page ? addonPageAddress({ addon: kind.addon, page: kind.page }) : ''
  return (
    <section className="ad-pane__section" aria-labelledby="ad-pane-conns" data-testid="addon-connections">
      <h3 className="ad-section__title" id="ad-pane-conns">{t('connection.title')}</h3>
      {connections.length === 0
        ? <p className="ad-quiet ad-pane__text">{t('connection.none')}</p>
        : (
          <ul className="ad-conns">
            {connections.map(c => {
              const state = rowState(c)
              const chip = STATE_CHIP[state]
              const { agentName, who } = connectionLine(c, agents, t)
              return (
                <li key={c.id} className="ad-conn" data-testid="addon-connection" data-state={state}>
                  <AppIcon src={app.icon ? apiUrl(app.icon) : ''} id={app.id} name={app.title} size={32} />
                  <span className="ad-conn__text">
                    <span className="ad-conn__name">{c.name}</span>
                    <span className="ad-conn__facts">{agentName ? t('connection.answersAs', { name: agentName }) : t('connection.noAgent')} · {who}</span>
                  </span>
                  <StatusChip status={chip.status} tone={chip.tone} label={t(`connection.state.${state}`)} />
                </li>
              )
            })}
          </ul>
        )}
      {href && <div className="ad-pane__acts"><Button size="sm" iconEnd="arrow-right" to={href} data-testid="addon-connections-manage">{t('connection.manage')}</Button></div>}
    </section>
  )
}
