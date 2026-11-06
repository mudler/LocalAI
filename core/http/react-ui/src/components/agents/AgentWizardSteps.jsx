/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import ModelPicker from '../ModelPicker'
import { AppIcon } from '../addons/AddonBits'
import { apiUrl } from '../../utils/basePath'
import { Button, Callout, StatusChip, Switch } from '../ui'
import AgentTemplates from './AgentTemplates'
import AgentPresets, { AppliedNotice } from './AgentPresets'
import AgentFiles from './AgentFiles'
import AgentUsesPanel from './AgentUsesPanel'
import { AGENT_TEMPLATES } from '../../utils/agentConfigTools'
import { CAP_CHAT } from '../../utils/capabilities'
import { needsLine } from '../../utils/agentWizard'
import { describeChatModel } from '../../utils/describeModel'

// The four steps of the new-agent wizard. Each is a plain section: the page
// owns the form, the step and the apps that were wanted.

export function PurposeStep({ form, updateField, picked, onTemplate, models, modelsLoading }) {
  const { t } = useTranslation(['agents', 'home'])
  const [choosing, setChoosing] = useState(Boolean(form.model))
  return (
    <>
      <AgentTemplates templates={AGENT_TEMPLATES} picked={picked} onPick={onTemplate} hideTools />
      <div className="form-group">
        <label className="form-label" htmlFor="aw-name">{t('create.name')} <span className="text-error">*</span></label>
        <input id="aw-name" className="dk-input" value={form.name} maxLength={80} placeholder="research-assistant" onChange={(e) => updateField('name', e.target.value)} />
      </div>
      <div className="form-group">
        <label className="form-label" htmlFor="aw-instructions">{t('create.instructions')} <span className="text-error">*</span></label>
        <textarea id="aw-instructions" className="dk-input dk-textarea" rows={8} value={form.instructions} onChange={(e) => updateField('instructions', e.target.value)} />
        <p className="ag-note">{t('create.instructionsHelp')}</p>
      </div>
      <div className="aw-model" data-testid="wizard-model">
        {choosing ? (
          <div className="form-group">
            <label className="form-label" htmlFor="agent-model">{t('create.model')}</label>
            <ModelPicker
              id="agent-model"
              labels={{ label: t('create.model') }}
              autoSelect={false}
              rememberSelection={false}
              defaultChoice={t('wizard.purpose.modelEach')}
              defaultSelected={!form.model}
              onChooseDefault={() => { updateField('model', ''); setChoosing(false) }}
              value={form.model}
              onChange={(v) => updateField('model', v)}
              capability={CAP_CHAT}
              models={models}
              loading={modelsLoading}
              describe={(name) => describeChatModel(models, name, (c) => t(`home:picker.cap.${c}`, { defaultValue: c }))}
              variant="field"
              searchable
              placeholder={t('create.modelPlaceholder')}
            />
          </div>
        ) : (
          <p className="ag-note aw-model__line">
            {t('wizard.purpose.modelLine')}{' '}
            <button type="button" className="ag-link" onClick={() => setChoosing(true)} data-testid="wizard-model-choose">{t('wizard.purpose.modelChoose')}</button>
          </p>
        )}
      </div>
    </>
  )
}

export function AbilitiesStep({ form, updateField, tools, toolGroups, ab, pristine, templateLevel }) {
  const { t } = useTranslation('agents')
  const [fine, setFine] = useState(false)
  return (
    <>
      <div data-testid="wizard-levels">
        <AgentPresets
          active={ab.level} ready={ab.levelReady} onPick={ab.applyLevel}
          customText={pristine ? t('wizard.abilities.defaultNote') : undefined}
        />
        <AppliedNotice applied={ab.applied?.kind === 'preset' ? ab.applied : null} onUndo={ab.undoApplied} onKeep={() => ab.setApplied(null)} testId="agent-preset-applied" />
        {templateLevel && ab.level !== templateLevel && (
          <p className="ag-note" data-testid="wizard-template-level">
            {t('wizard.abilities.templateHint', { level: t(`create.preset.${templateLevel}.title`) })}{' '}
            <button type="button" className="ag-link" onClick={() => ab.applyLevel(templateLevel)}>{t('wizard.abilities.templateUse', { level: t(`create.preset.${templateLevel}.title`) })}</button>
          </p>
        )}
      </div>
      <section className="aw-block" aria-labelledby="aw-files-h" id="aw-files" tabIndex={-1} data-testid="wizard-files">
        <h3 className="ag-h3" id="aw-files-h">{t('wizard.abilities.files')}</h3>
        <AgentFiles form={form} updateField={updateField} abilities={ab} savedConfig={null} isEdit={false} />
      </section>
      <details className="aw-fine" open={fine} onToggle={(e) => setFine(e.currentTarget.open)} data-testid="wizard-fine">
        <summary>{t('wizard.abilities.fine')}</summary>
        {fine && (
          <AgentUsesPanel
            abilities={ab} tools={tools} toolGroups={toolGroups} form={form} updateField={updateField}
            notices={<p className="ag-note">{t('create.toolsHelp')}</p>}
          />
        )}
      </details>
    </>
  )
}

function AppRow({ item, checked, onChange, icon }) {
  const { t } = useTranslation('agents')
  return (
    <li className="aw-app" data-app={item.key} data-installed={item.installed || undefined}>
      <AppIcon src={icon ? apiUrl(icon) : ''} id={item.addon} name={item.title} size={32} />
      <div className="aw-app__text">
        <span className="aw-app__title" id={`aw-app-${item.key}`}>
          {item.title}
          {!item.installed && <StatusChip tone="idle" label={t('wizard.where.notInstalled')} />}
        </span>
        {item.description && <span className="aw-app__body">{item.description}</span>}
        <span className="aw-app__needs">{needsLine(item, (title) => t('wizard.where.generic', { title }))}</span>
      </div>
      <Switch checked={checked} onChange={onChange} labelledBy={`aw-app-${item.key}`} testId={`wizard-app-${item.key}`} />
    </li>
  )
}

export function WhereStep({ groups, icons, ready, wanted, onToggle }) {
  const { t } = useTranslation('agents')
  const none = ready && groups.chat.length + groups.tools.length === 0
  const section = (id, items) => items.length > 0 && (
    <section className="aw-block" aria-labelledby={`aw-where-${id}`} data-testid={`wizard-group-${id}`}>
      <h3 className="ag-h3" id={`aw-where-${id}`}>{t(`wizard.where.${id}`)}</h3>
      <p className="ag-note">{t(`wizard.where.${id}Lead`)}</p>
      <ul className="aw-apps">
        {items.map((it) => <AppRow key={it.key} item={it} icon={icons[it.addon]} checked={wanted.includes(it.key)} onChange={(on) => onToggle(it, on)} />)}
      </ul>
    </section>
  )
  return (
    <>
      {!ready && <p className="ag-note" role="status">{t('wizard.where.loading')}</p>}
      {section('chat', groups.chat)}
      {section('tools', groups.tools)}
      {none && <p className="ag-note" data-testid="wizard-where-empty">{t('wizard.where.empty')}</p>}
      <Callout tone="info" testId="wizard-where-note">{t('wizard.where.note')}</Callout>
      <div>
        <Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/addons?tab=browse" target="_blank" rel="noreferrer" data-testid="wizard-find-addons">{t('wizard.where.find')}</Button>
      </div>
    </>
  )
}

export function ReviewStep({ form, filesChoice, levelName, chosen, onLimit, modelName }) {
  const { t } = useTranslation('agents')
  return (
    <>
      <ul className="aw-checks" data-testid="wizard-checks">
        <li><span className="aw-checks__ok" aria-hidden="true">&#10003;</span>{t('wizard.review.named')}</li>
        <li><span className="aw-checks__ok" aria-hidden="true">&#10003;</span>{t('wizard.review.tools', { level: levelName })}</li>
        <li><span className="aw-checks__ok" aria-hidden="true">&#10003;</span>{modelName ? t('wizard.review.modelFixed', { model: modelName }) : t('wizard.review.modelEach')}</li>
        {chosen.length > 0 && <li><span className="aw-checks__ok" aria-hidden="true">&#10003;</span>{t('wizard.review.apps', { count: chosen.length })}</li>}
      </ul>
      {filesChoice === 'all' && (
        <Callout
          tone="warn" testId="wizard-files-warning" title={t('wizard.review.filesTitle')}
          actions={<Button size="sm" onClick={onLimit} data-testid="wizard-limit-folders">{t('wizard.review.limit')}</Button>}
        >
          {t('wizard.review.filesBody')}
        </Callout>
      )}
      {chosen.length > 0 && <p className="ag-note" data-testid="wizard-review-setup">{t('wizard.review.setupNext')}</p>}
    </>
  )
}
