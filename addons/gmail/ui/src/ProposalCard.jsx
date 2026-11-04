/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Gmail change that waits for the person, inside the host's
// approval frame: what it does, to whom, and the exact text. The frame owns
// Confirm and Cancel; this card only draws. Addresses and text are drawn as
// text, never as markup.
import { useTranslation } from 'react-i18next'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { proposalView } from './proposal'
import en from './i18n/gmail.en.json'
import it from './i18n/gmail.it.json'
import es from './i18n/gmail.es.json'
import de from './i18n/gmail.de.json'
import fr from './i18n/gmail.fr.json'
import zh from './i18n/gmail.zh-CN.json'
import id from './i18n/gmail.id.json'
import ko from './i18n/gmail.ko.json'
import pt from './i18n/gmail.pt-BR.json'
import './gmail.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ gmail: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

function Row({ label, children, testId }) {
  return (
    <div className="gm-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('gmail')
  const v = proposalView(tool, args, effects?.sample)
  const send = v.tool === 'send-draft'
  return (
    <div className="gm-prop" data-testid="gmail-proposal">
      <h3 className="gm-prop__title">{t(`approval.action.${v.tool}`, { defaultValue: v.tool })}</h3>
      <dl className="gm-prop__facts">
        <Row label={t('approval.to')} testId="gmail-proposal-to">{v.to || t('approval.none')}</Row>
        {v.cc && <Row label={t('approval.cc')}>{v.cc}</Row>}
        {v.bcc && <Row label={t('approval.bcc')}>{v.bcc}</Row>}
        <Row label={t('approval.subject')} testId="gmail-proposal-subject">{v.subject || t('approval.none')}</Row>
        {v.replyToMessageId && <Row label={t('approval.replyTo')}>{[v.replyToFrom, v.replyToSubject].filter(Boolean).join(' - ') || v.replyToMessageId}</Row>}
        {v.attachments.length > 0 && <Row label={t('approval.attachments')}>{v.attachments.join(', ')}</Row>}
      </dl>
      {v.hasBody && (
        <>
          <p className="gm-prop__label" id="gm-prop-text">{t('approval.text', { bytes: v.bytes })}</p>
          <blockquote className="gm-prop__text" aria-labelledby="gm-prop-text" data-testid="gmail-proposal-text">{v.body}</blockquote>
          {v.cut && <p className="cn-muted">{t('approval.cut', { bytes: v.bytes })}</p>}
        </>
      )}
      <Notice tone={send ? 'warn' : 'info'} role="note" testId="gmail-proposal-note">{t(send ? 'approval.sendNote' : 'approval.draftNote')}</Notice>
    </div>
  )
}
