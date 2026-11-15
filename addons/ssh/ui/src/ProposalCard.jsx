/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of an SSH command that waits for the person, inside the host's
// approval frame: the host, the user, the exact command, the host key it will
// be checked against, and the connection that runs it. The frame owns Confirm
// and Cancel; this card only draws.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { connectionsFor, proposalView } from './proposal'
import './ssh.css'

function useConnections(host) {
  const [names, setNames] = useState(null)
  useEffect(() => {
    let live = true
    if (!host) return undefined
    connectorsApi.list().then((r) => { if (live) setNames(connectionsFor(r.connectors, host)) }).catch(() => { if (live) setNames([]) })
    return () => { live = false }
  }, [host])
  return names
}

function Row({ label, children, testId }) {
  return (
    <div className="ssh-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, arguments: args, effects }) {
  const { t } = useTranslation('ssh')
  const v = proposalView(tool, args, effects?.sample)
  const names = useConnections(v.host)
  const asText = names === null || names.length === 0
    ? t('approval.asUnknown')
    : names.length === 1 ? t('approval.asOne', { name: names[0] }) : t('approval.asFirst')
  return (
    <div className="ssh-prop" data-testid="ssh-proposal">
      <h3 className="ssh-prop__title">{t('approval.title', { host: v.host.split('#')[0] })}</h3>
      <Notice tone="warn" role="note" testId="ssh-proposal-physical">{t('approval.physical')}</Notice>
      <dl className="ssh-prop__facts">
        <Row label={t('approval.host')} testId="ssh-proposal-host"><code>{v.host.split('#')[0]}</code></Row>
        <Row label={t('approval.user')} testId="ssh-proposal-user">{v.user || '?'}</Row>
        <Row label={t('approval.command')} testId="ssh-proposal-command">
          <code className="ssh-prop__code">{v.command}</code>
          <span className="cn-muted">{t('approval.lines', { count: v.lines || 1 })}</span>
        </Row>
        {v.timeoutSeconds !== null && <Row label={t('approval.timeout')} testId="ssh-proposal-timeout">{t('approval.seconds', { count: v.timeoutSeconds })}</Row>}
        {v.hostKey && <Row label={t('approval.hostKey')} testId="ssh-proposal-key"><code>{v.hostKey}</code></Row>}
        <Row label={t('approval.as')} testId="ssh-proposal-as">{asText}</Row>
        <Row label={t('approval.what')} testId="ssh-proposal-effect">{t('approval.effect')}</Row>
      </dl>
    </div>
  )
}
