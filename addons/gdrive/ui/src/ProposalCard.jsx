/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Google Drive change that waits for the person, inside the
// host's approval frame: what it does, to which file, and the exact text that
// will be saved. The frame owns Confirm and Cancel; this card only draws.
import { useTranslation } from 'react-i18next'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { ProposalFacts, ProposalFrame, ProposalRow as Row, ProposalText } from '../../../internal/connector-ui/src/addonparts'
import { proposalView } from './proposal'
import './i18n'

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('gdrive')
  const v = proposalView(tool, args, effects?.sample)
  return (
    <ProposalFrame ns="gdrive" title={t(`approval.action.${v.tool}`, { defaultValue: v.tool })}>
      <ProposalFacts>
        {v.targetName && <Row label={t('approval.file')} testId="gdrive-proposal-file">{v.targetName}</Row>}
        {v.tool === 'create-file' && v.name && <Row label={t('approval.name')}>{v.name}</Row>}
        {v.tool === 'update-file' && v.name && <Row label={t('approval.newName')}>{v.name}</Row>}
        {v.tool === 'create-file' && <Row label={t('approval.in')}>{v.parentName || v.parent || t('approval.allowedFolder')}</Row>}
        {v.tool === 'move-file' && <Row label={t('approval.moveTo')}>{v.newParentName || v.newParent}</Row>}
        {v.tool === 'create-file' && v.mimeType && <Row label={t('approval.type')}>{v.mimeType}</Row>}
      </ProposalFacts>
      {v.hasContent && (
        <>
          <ProposalText ns="gdrive" label={t('approval.text', { bytes: v.bytes })}>{v.content}</ProposalText>
          {v.cut && <p className="cn-muted">{t('approval.cut', { bytes: v.bytes })}</p>}
        </>
      )}
      {v.tool === 'trash-file' && <Notice tone="info" role="note" testId="gdrive-proposal-trash">{t('approval.trashNote')}</Notice>}
      {v.tool === 'update-file' && v.hasContent && <Notice tone="warn" role="note" testId="gdrive-proposal-replace">{t('approval.replaceNote')}</Notice>}
    </ProposalFrame>
  )
}
