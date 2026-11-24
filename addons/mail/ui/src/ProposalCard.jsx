/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Mail write that waits for the person, inside the host's
// approval frame: what it does, from which address, to whom, and the exact
// text. The frame owns Confirm and Cancel; this card only draws.
import { useTranslation } from 'react-i18next'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { proposalView } from './proposal'
import './mail.css'

function Row({ label, children, testId }) {
  return (
    <div className="ml-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, arguments: args, effects }) {
  const { t } = useTranslation('mail')
  const v = proposalView(tool, args, effects?.sample)
  return (
    <div className="ml-prop" data-testid="mail-proposal">
      <h3 className="ml-prop__title">{t(`approval.action.${v.tool}`)}</h3>
      <dl className="ml-prop__facts">
        {v.from && <Row label={t('approval.from')} testId="mail-proposal-from">{v.from}</Row>}
        <Row label={t('approval.to')} testId="mail-proposal-to">{v.to.join(', ')}</Row>
        {v.cc.length > 0 && <Row label={t('approval.cc')} testId="mail-proposal-cc">{v.cc.join(', ')}</Row>}
        <Row label={t('approval.subject')} testId="mail-proposal-subject">{v.subject}</Row>
        {v.reply && <Row label={t('approval.reply')}>{t('approval.replyYes')}</Row>}
      </dl>
      <p className="ml-prop__label" id="ml-prop-text">{t('approval.text')}</p>
      <blockquote className="ml-prop__text" aria-labelledby="ml-prop-text" data-testid="mail-proposal-text">{v.body}</blockquote>
      {v.cut && <p className="cn-muted">{t('approval.cut', { bytes: v.bytes })}</p>}
      <Notice tone="warn" role="note" testId="mail-proposal-note">{t(v.tool === 'save-draft' ? 'approval.noteDraft' : 'approval.noteSend')}</Notice>
    </div>
  )
}
