/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a PiKVM action that waits for the person, inside the host's
// approval frame: what will be pressed or typed, on which device and connection,
// and the power state read just now. The frame owns Confirm and Cancel; this
// card only draws.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { connectionsFor, proposalView } from './proposal'
import './pikvm.css'

function useConnections(device) {
  const [names, setNames] = useState(null)
  useEffect(() => {
    let live = true
    if (!device) return undefined
    connectorsApi.list().then((r) => { if (live) setNames(connectionsFor(r.connectors, device)) }).catch(() => { if (live) setNames([]) })
    return () => { live = false }
  }, [device])
  return names
}

function Row({ label, children, testId }) {
  return (
    <div className="pk-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, arguments: args, effects }) {
  const { t } = useTranslation('pikvm')
  const v = proposalView(tool, args, effects?.sample)
  const names = useConnections(v.device)
  const asText = names === null || names.length === 0
    ? t('approval.asUnknown')
    : names.length === 1 ? t('approval.asOne', { name: names[0] }) : t('approval.asFirst')
  const title = v.kind === 'power' ? t('approval.titlePower', { action: v.action })
    : v.kind === 'text' ? t('approval.titleText', { count: v.chars })
      : t('approval.titleKeys', { count: v.keys.length })
  return (
    <div className="pk-prop" data-testid="pikvm-proposal">
      <h3 className="pk-prop__title">{title}</h3>
      <Notice tone="warn" role="note" testId="pikvm-proposal-physical">{t('approval.physical')}</Notice>
      <dl className="pk-prop__facts">
        <Row label={t('approval.device')} testId="pikvm-proposal-device"><code>{v.device.split('#')[0]}</code></Row>
        <Row label={t('approval.as')} testId="pikvm-proposal-as">{asText}</Row>
        {v.kind === 'power' && (
          <>
            <Row label={t('approval.now')} testId="pikvm-proposal-now">{v.powerNow ? t(`approval.power.${v.powerNow}`) : t('approval.power.unknown')}</Row>
            <Row label={t('approval.what')} testId="pikvm-proposal-effect">{v.effect}</Row>
            {v.hard && <Notice tone="error" role="note" testId="pikvm-proposal-hard">{t('approval.hard')}</Notice>}
          </>
        )}
        {v.kind === 'text' && (
          <>
            <Row label={t('approval.text')} testId="pikvm-proposal-text">
              <code className="pk-prop__code">{v.text}</code>
              <span className="cn-muted">{t('approval.counts', { chars: v.chars, lines: v.lines || 1 })}</span>
            </Row>
            <Row label={t('approval.what')} testId="pikvm-proposal-effect">{t('approval.typeEffect')}</Row>
          </>
        )}
        {v.kind === 'keys' && (
          <>
            <Row label={t('approval.keys')} testId="pikvm-proposal-keys">
              <ol className="pk-prop__keys">{v.keys.map((k, i) => <li key={i}><kbd>{k}</kbd></li>)}</ol>
            </Row>
            <Row label={t('approval.what')} testId="pikvm-proposal-effect">{t('approval.keysEffect')}</Row>
          </>
        )}
      </dl>
    </div>
  )
}
