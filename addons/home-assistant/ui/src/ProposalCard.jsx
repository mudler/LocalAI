/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Home Assistant change that waits for the person, inside the
// host's approval frame: what will be switched, on which connection, and the
// service data. The frame owns Confirm and Cancel; this card only draws.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { connectionsFor, proposalView } from './proposal'
import './home-assistant.css'

function useConnections(instance) {
  const [names, setNames] = useState(null)
  useEffect(() => {
    let live = true
    if (!instance) return undefined
    connectorsApi.list().then((r) => { if (live) setNames(connectionsFor(r.connectors, instance)) }).catch(() => { if (live) setNames([]) })
    return () => { live = false }
  }, [instance])
  return names
}

function Row({ label, children, testId }) {
  return (
    <div className="ha-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('home-assistant')
  const v = proposalView(tool, args, effects?.sample)
  const names = useConnections(v.instance)
  const asText = names === null || names.length === 0
    ? t('approval.asUnknown')
    : names.length === 1 ? t('approval.asOne', { name: names[0] }) : t('approval.asFirst')
  return (
    <div className="ha-prop" data-testid="home-assistant-proposal">
      <h3 className="ha-prop__title">{t('approval.title', { action: v.action })}</h3>
      <dl className="ha-prop__facts">
        <Row label={t('approval.targets')} testId="home-assistant-proposal-targets">
          <ul className="ha-prop__targets">
            {v.targets.map((x) => (
              <li key={x.entityId}>
                <span className="ha-prop__name">{x.name || x.entityId}</span>
                {x.name && <code className="ha-prop__id">{x.entityId}</code>}
                {x.state && <span className="ha-prop__state">{t('approval.now', { state: x.state })}</span>}
              </li>
            ))}
          </ul>
        </Row>
        <Row label={t('approval.as')} testId="home-assistant-proposal-as">{asText}</Row>
        {v.data && (
          <Row label={t('approval.data')} testId="home-assistant-proposal-data">
            <code className="ha-prop__code">{v.data}</code>
            {v.dataCut && <span className="cn-muted">{t('approval.cut')}</span>}
          </Row>
        )}
      </dl>
      {v.physical
        ? <Notice tone="warn" role="note" testId="home-assistant-proposal-physical">{t('approval.physical')}</Notice>
        : <Notice tone="info" role="note" testId="home-assistant-proposal-quiet">{t('approval.quiet')}</Notice>}
    </div>
  )
}
