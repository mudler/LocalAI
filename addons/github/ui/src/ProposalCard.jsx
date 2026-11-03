/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a GitHub write that waits for the person, inside the host's
// approval frame: what it does, where, as which connection, and the exact
// text that will be posted. The frame owns Confirm and Cancel; this card only
// draws.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { connectionsFor, proposalView } from './proposal'
import './github.css'

function useConnections(repo) {
  const [names, setNames] = useState(null)
  useEffect(() => {
    let live = true
    if (!repo) return undefined
    connectorsApi.list().then((r) => { if (live) setNames(connectionsFor(r.connectors, repo)) }).catch(() => { if (live) setNames([]) })
    return () => { live = false }
  }, [repo])
  return names
}

function Row({ label, children, testId }) {
  return (
    <div className="gh-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('github')
  const v = proposalView(tool, args, effects?.sample)
  const names = useConnections(v.repo)
  const asText = names === null || names.length === 0
    ? t('approval.asUnknown', { repo: v.repo })
    : names.length === 1 ? t('approval.asOne', { name: names[0] }) : t('approval.asFirst', { repo: v.repo })
  const hasText = v.body !== ''
  const stateText = v.state === 'closed'
    ? (v.reason ? t('approval.closeReason', { reason: t(`approval.${v.reason}`, { defaultValue: v.reason }) }) : t('approval.close'))
    : v.state === 'open' ? t('approval.reopen') : ''
  return (
    <div className="gh-prop" data-testid="github-proposal">
      <h3 className="gh-prop__title">{t(`approval.action.${v.tool}`, { number: v.number, defaultValue: v.tool })}</h3>
      <dl className="gh-prop__facts">
        {v.repo && (
          <Row label={t('approval.where')} testId="github-proposal-where">
            {v.url
              ? <a href={v.url} target="_blank" rel="noopener noreferrer" className="gh-link">{t('approval.repo', { repo: v.repo })} #{v.number}</a>
              : t('approval.repo', { repo: v.repo })}
            {v.targetTitle && <span className="gh-prop__target">{v.targetTitle}</span>}
          </Row>
        )}
        <Row label={t('approval.as')} testId="github-proposal-as">{asText}</Row>
        {v.tool === 'create-issue' && v.title && <Row label={t('approval.title')}>{v.title}</Row>}
        {v.tool === 'edit-issue' && v.title && <Row label={t('approval.newTitle')}>{v.title}</Row>}
        {v.tool === 'edit-issue' && stateText && <Row label={t('approval.state')}>{stateText}</Row>}
        {v.labels.length > 0 && <Row label={t('approval.labels')}>{v.labels.join(', ')}</Row>}
        {v.path && <Row label={t('approval.file')}>{t('approval.fileLine', { path: v.path, line: v.line, side: v.side === 'LEFT' ? t('approval.sideLeft') : t('approval.sideRight') })}</Row>}
      </dl>
      {hasText && (
        <>
          <p className="gh-prop__label" id="gh-prop-text">{t('approval.text')}</p>
          <blockquote className="gh-prop__text" aria-labelledby="gh-prop-text" data-testid="github-proposal-text">{v.body}</blockquote>
          {v.cut && <p className="cn-muted">{t('approval.cut', { bytes: v.bytes })}</p>}
        </>
      )}
      {v.repo && <Notice tone="warn" role="note" testId="github-proposal-public">{t('approval.public', { repo: v.repo })}</Notice>}
    </div>
  )
}
