/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Kubernetes write that waits for the person, inside the
// host's approval frame: the cluster, the object, and what changes (the diff
// of a dry run, the pods a drain evicts, the argv of an exec). The frame owns
// Confirm and Cancel; this card only draws.
import { useTranslation } from 'react-i18next'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { proposalView, targetOf } from './proposal'
import './kubernetes.css'

function Row({ label, children, testId }) {
  return (
    <div className="kube-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

const CHANGE_KEYS = { create: 'card.create', update: 'card.update', 'no change': 'card.noChange' }

export default function ProposalCard({ tool, arguments: args, effects }) {
  const { t } = useTranslation('kubernetes')
  const v = proposalView(tool, args, effects?.sample)
  const target = targetOf(v)
  const isNode = ['cordon', 'uncordon', 'drain'].includes(v.tool)
  const hasDiff = v.tool === 'apply' || v.tool === 'patch'
  return (
    <div className="kube-prop" data-testid="kubernetes-proposal">
      <h3 className="kube-prop__title">{t('card.title', { tool: t(`tool.${v.tool}`, { defaultValue: v.tool }), target })}</h3>
      <Notice tone="warn" role="note" testId="kubernetes-proposal-physical">{t('card.physical')}</Notice>
      <dl className="kube-prop__facts">
        {v.cluster && (
          <Row label={t('card.cluster')} testId="kubernetes-proposal-cluster">
            {v.server ? <><code>{v.context}</code> <span className="cn-muted">{v.server}</span></> : <code>{v.cluster}</code>}
          </Row>
        )}
        {isNode
          ? <Row label={t('card.node')} testId="kubernetes-proposal-node"><code>{v.node}</code></Row>
          : <Row label={t('card.object')} testId="kubernetes-proposal-object"><code>{target}</code></Row>}
        {(v.from !== '' || v.to !== '') && (
          <Row label={t('card.change')} testId="kubernetes-proposal-fromto"><code>{t('card.fromTo', { from: v.from || '?', to: v.to || '?' })}</code></Row>
        )}
        {v.change && CHANGE_KEYS[v.change] && <Row label={t('card.change')} testId="kubernetes-proposal-change">{t(CHANGE_KEYS[v.change])}</Row>}
        {v.patchType && <Row label={t('card.patchType')} testId="kubernetes-proposal-patchtype"><code>{v.patchType}</code></Row>}
        {v.gracePeriodSeconds !== null && <Row label={t('card.grace')} testId="kubernetes-proposal-grace">{t('card.seconds', { count: v.gracePeriodSeconds })}</Row>}
        {v.tool === 'exec' && (
          <>
            {v.container && <Row label={t('card.container')} testId="kubernetes-proposal-container"><code>{v.container}</code></Row>}
            <Row label={t('card.command')} testId="kubernetes-proposal-command">
              <ol className="kube-argv" data-testid="kubernetes-proposal-argv">
                {v.command.map((a, n) => <li key={n}><code className="kube-argv__item">{a}</code></li>)}
              </ol>
              <span className="cn-muted">{t('card.commandNote', { count: v.command.length })}</span>
            </Row>
          </>
        )}
        {v.tool === 'drain' && (
          <>
            {v.cordoned !== null && <Row label={t('card.node')} testId="kubernetes-proposal-cordoned">{t(v.cordoned ? 'card.cordoned' : 'card.notCordoned')}</Row>}
            <Row label={t('card.pods')} testId="kubernetes-proposal-pods">
              {v.pods.length === 0 ? <span className="cn-muted">{t('card.noPods')}</span> : (
                <ul className="kube-list">{v.pods.map((p) => <li key={p}><code>{p}</code></li>)}</ul>
              )}
            </Row>
            {v.skipped.length > 0 && (
              <Row label={t('card.skipped')} testId="kubernetes-proposal-skipped">
                <ul className="kube-list">{v.skipped.map((s) => <li key={s.pod}><code>{s.pod}</code> <span className="cn-muted">{s.reason}</span></li>)}</ul>
              </Row>
            )}
          </>
        )}
        {v.timeoutSeconds !== null && <Row label={t('card.timeout')} testId="kubernetes-proposal-timeout">{t('card.seconds', { count: v.timeoutSeconds })}</Row>}
      </dl>
      {hasDiff && (
        <div className="kube-diff" data-testid="kubernetes-proposal-diff">
          <p className="kube-diff__title">{t('card.diff')}</p>
          {v.lines.length === 0 ? <p className="cn-muted">{t('card.noDiff')}</p> : (
            <pre className="kube-diff__body">{v.lines.map((l, n) => <span key={n} className={`kube-diff__line kube-diff__line--${l.kind}`}>{l.text}{'\n'}</span>)}</pre>
          )}
          {v.diffCut && <p className="cn-muted" data-testid="kubernetes-proposal-diffcut">{t('card.diffCut')}</p>}
        </div>
      )}
    </div>
  )
}
