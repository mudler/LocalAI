/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Google Drive change that waits for the person, inside the
// host's approval frame: what it does, to which file, and the exact text that
// will be saved. The frame owns Confirm and Cancel; this card only draws.
import { useTranslation } from 'react-i18next'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { proposalView } from './proposal'
import en from './i18n/gdrive.en.json'
import it from './i18n/gdrive.it.json'
import es from './i18n/gdrive.es.json'
import de from './i18n/gdrive.de.json'
import fr from './i18n/gdrive.fr.json'
import zh from './i18n/gdrive.zh-CN.json'
import id from './i18n/gdrive.id.json'
import ko from './i18n/gdrive.ko.json'
import pt from './i18n/gdrive.pt-BR.json'
import './gdrive.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ gdrive: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

function Row({ label, children, testId }) {
  return (
    <div className="gd-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('gdrive')
  const v = proposalView(tool, args, effects?.sample)
  return (
    <div className="gd-prop" data-testid="gdrive-proposal">
      <h3 className="gd-prop__title">{t(`approval.action.${v.tool}`, { defaultValue: v.tool })}</h3>
      <dl className="gd-prop__facts">
        {v.targetName && <Row label={t('approval.file')} testId="gdrive-proposal-file">{v.targetName}</Row>}
        {v.tool === 'create-file' && v.name && <Row label={t('approval.name')}>{v.name}</Row>}
        {v.tool === 'update-file' && v.name && <Row label={t('approval.newName')}>{v.name}</Row>}
        {v.tool === 'create-file' && <Row label={t('approval.in')}>{v.parentName || v.parent || t('approval.allowedFolder')}</Row>}
        {v.tool === 'move-file' && <Row label={t('approval.moveTo')}>{v.newParentName || v.newParent}</Row>}
        {v.tool === 'create-file' && v.mimeType && <Row label={t('approval.type')}>{v.mimeType}</Row>}
      </dl>
      {v.hasContent && (
        <>
          <p className="gd-prop__label" id="gd-prop-text">{t('approval.text', { bytes: v.bytes })}</p>
          <blockquote className="gd-prop__text" aria-labelledby="gd-prop-text" data-testid="gdrive-proposal-text">{v.content}</blockquote>
          {v.cut && <p className="cn-muted">{t('approval.cut', { bytes: v.bytes })}</p>}
        </>
      )}
      {v.tool === 'trash-file' && <Notice tone="info" role="note" testId="gdrive-proposal-trash">{t('approval.trashNote')}</Notice>}
      {v.tool === 'update-file' && v.hasContent && <Notice tone="warn" role="note" testId="gdrive-proposal-replace">{t('approval.replaceNote')}</Notice>}
    </div>
  )
}
