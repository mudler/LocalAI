/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Gmail change that waits for the person, inside the host's
// approval frame: what it does, to whom, and the exact text. The frame owns
// Confirm and Cancel; this card only draws. Addresses and text are drawn as
// text, never as markup.
import { useTranslation } from 'react-i18next'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { ProposalFacts, ProposalFrame, ProposalRow as Row, ProposalText } from '../../../internal/connector-ui/src/addonparts'
import { proposalView } from './proposal'
import './i18n'

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('gmail')
  const v = proposalView(tool, args, effects?.sample)
  const send = v.tool === 'send-draft'
  return (
    <ProposalFrame ns="gmail" title={t(`approval.action.${v.tool}`, { defaultValue: v.tool })}>
      <ProposalFacts>
        <Row label={t('approval.to')} testId="gmail-proposal-to">{v.to || t('approval.none')}</Row>
        {v.cc && <Row label={t('approval.cc')}>{v.cc}</Row>}
        {v.bcc && <Row label={t('approval.bcc')}>{v.bcc}</Row>}
        <Row label={t('approval.subject')} testId="gmail-proposal-subject">{v.subject || t('approval.none')}</Row>
        {v.replyToMessageId && <Row label={t('approval.replyTo')}>{[v.replyToFrom, v.replyToSubject].filter(Boolean).join(' - ') || v.replyToMessageId}</Row>}
        {v.attachments.length > 0 && <Row label={t('approval.attachments')}>{v.attachments.join(', ')}</Row>}
      </ProposalFacts>
      {v.hasBody && (
        <>
          <ProposalText ns="gmail" label={t('approval.text', { bytes: v.bytes })}>{v.body}</ProposalText>
          {v.cut && <p className="cn-muted">{t('approval.cut', { bytes: v.bytes })}</p>}
        </>
      )}
      <Notice tone={send ? 'warn' : 'info'} role="note" testId="gmail-proposal-note">{t(send ? 'approval.sendNote' : 'approval.draftNote')}</Notice>
    </ProposalFrame>
  )
}
