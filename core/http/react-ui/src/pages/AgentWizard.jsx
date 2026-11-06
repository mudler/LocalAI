/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAgentsApi } from '../utils/api'
import { useAgentTools } from '../hooks/useAgentTools'
import { useAgentAbilities } from '../hooks/useAgentAbilities'
import { useModels } from '../hooks/useModels'
import { useToolGroups } from '../hooks/useToolGroups'
import { useWhereItems } from '../hooks/useWhereItems'
import { AGENT_TEMPLATES } from '../utils/agentConfigTools'
import { filesChoiceOf, foldersUnchosen } from '../utils/chatAgents'
import { wizardForm, wizardPayload, purposeReady, stepReachable, chosenItems, WIZARD_STEPS } from '../utils/agentWizard'
import { saveSetup, wishOf } from '../utils/agentSetup'
import { CAP_CHAT } from '../utils/capabilities'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import Icon from '../components/Icon'
import { Button, Page, PageHeader } from '../components/ui'
import { PurposeStep, AbilitiesStep, WhereStep, ReviewStep } from '../components/agents/AgentWizardSteps'
import './agents.css'
import './library.css'
import '../components/agents/agentwizard.css'

// The new-agent wizard: Purpose, Abilities, Where it works, Review. Only a name
// and instructions are required. An untouched optional step sends what the full
// editor sends for a blank new agent (see wizardPayload). Choosing an app here
// connects nothing: the choice is remembered in this browser and the agent's
// page shows a "Finish setup" card for it.
export default function AgentWizard() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('agents')
  const [searchParams] = useSearchParams()
  const templateId = searchParams.get('template') || ''

  const initial = useRef(null)
  if (!initial.current) initial.current = wizardForm(templateId)
  const [form, setForm] = useState(initial.current)
  const [picked, setPicked] = useState(AGENT_TEMPLATES.some((x) => x.id === templateId) ? templateId : '')
  const [step, setStep] = useState('purpose')
  const [furthest, setFurthest] = useState(0)
  const [wanted, setWanted] = useState([])
  const [saving, setSaving] = useState(false)
  const headingRef = useRef(null)
  const moved = useRef(false)

  const updateField = useCallback((name, value) => setForm((prev) => ({ ...prev, [name]: value })), [])
  const { models, loading: modelsLoading } = useModels(CAP_CHAT, { keepBroken: true })
  const tools = useAgentTools(form, updateField)
  const toolGroups = useToolGroups(tools, { commandState: false, forAgent: true })
  const ab = useAgentAbilities({ form, setForm, updateField, tools, initialFolders: [] })
  const where = useWhereItems(tools.addons)

  const index = WIZARD_STEPS.indexOf(step)
  const folderUnchosen = filesChoiceOf(form) !== 'none' && foldersUnchosen([], form.folders)
  const payload = useMemo(() => wizardPayload(form), [form])
  const dirty = JSON.stringify(payload) !== JSON.stringify(wizardPayload(initial.current)) || wanted.length > 0
  const chosen = chosenItems(where.groups, wanted)

  // Focus follows the step, so a keyboard or screen reader person lands on its heading.
  useEffect(() => {
    if (!moved.current) { moved.current = true; return }
    headingRef.current?.focus()
  }, [step])

  const go = (next) => {
    const i = WIZARD_STEPS.indexOf(next)
    if (i > 0 && !stepReachable(next, form)) return
    setStep(next)
    setFurthest((f) => Math.max(f, i))
  }

  const onTemplate = (tpl) => {
    setPicked(tpl.id)
    setForm((prev) => ({ ...prev, name: tpl.name, description: tpl.description, instructions: tpl.instructions }))
  }
  const toggleApp = (item, on) => setWanted((prev) => (on ? [...new Set([...prev, item.key])] : prev.filter((k) => k !== item.key)))

  const create = async () => {
    if (!purposeReady(form)) { setStep('purpose'); return }
    if (folderUnchosen) {
      addToast(t('folder.needsChoice'), 'warning')
      setStep('abilities')
      return
    }
    setSaving(true)
    try {
      const res = await chatAgentsApi.create(payload)
      const id = res?.id || res?.agent?.id
      if (id && chosen.length) saveSetup(id, { wanted: chosen.map(wishOf), dismissed: false })
      addToast(t('create.created', { name: payload.name }), 'success')
      initial.current = form
      setSaving(false)
      // The guard must see the form as saved before the route changes.
      setTimeout(() => navigate(id ? `/app/agents/${encodeURIComponent(id)}` : '/app/agents'), 0)
    } catch (err) {
      addToast(t('create.saveFailed', { message: err.message }), 'error')
      setSaving(false)
    }
  }

  const next = () => { if (index < WIZARD_STEPS.length - 1) go(WIZARD_STEPS[index + 1]) }
  const back = () => { if (index > 0) go(WIZARD_STEPS[index - 1]) }

  const pristine = ['builtins_off', 'builtins_on', 'builtins'].every((k) => JSON.stringify(form[k]) === JSON.stringify(initial.current[k]))
  const levelName = ab.level ? t(`create.preset.${ab.level}.title`) : t('wizard.defaultTools')
  const filesChoice = ab.filesChoice
  const filesText = filesChoice === 'none' ? t('folder.none') : filesChoice === 'all' ? t('folder.all') : t('wizard.rail.folders', { count: (form.folders || []).filter((f) => f.path).length })
  const templateLevel = AGENT_TEMPLATES.find((x) => x.id === picked)?.preset || ''
  const canNext = step !== 'purpose' || purposeReady(form)

  const limitFolders = () => {
    setStep('abilities')
    setTimeout(() => document.getElementById('aw-files')?.scrollIntoView({ block: 'center' }), 0)
  }

  return (
    <Page className="ag-page aw-page" data-testid="agent-wizard">
      <UnsavedChangesGuard when={dirty && !saving} />
      <div className="ag-home">
        <button type="button" className="ag-back" onClick={() => navigate('/app/agents')}><Icon name="arrow-left" /> {t('agent.back')}</button>
        <PageHeader title={t('wizard.title')} supporting={t('wizard.supporting')} />

        <nav aria-label={t('wizard.stepsLabel')}>
          <ol className="aw-steps" data-testid="wizard-steps">
            {WIZARD_STEPS.map((id, i) => (
              <li key={id}>
                <button
                  type="button" className="aw-step" aria-current={step === id ? 'step' : undefined}
                  data-done={i < index || undefined} disabled={i > furthest || (i > 0 && !stepReachable(id, form))}
                  onClick={() => go(id)} data-testid={`wizard-step-${id}`}
                >
                  <span className="aw-step__n" aria-hidden="true">{i + 1}</span>
                  {t(`wizard.step.${id}`)}
                </button>
              </li>
            ))}
          </ol>
        </nav>

        <div className="aw-layout">
          <section className="aw-main" aria-labelledby="aw-step-h" data-testid={`wizard-pane-${step}`}>
            <h2 className="ag-h2" id="aw-step-h" tabIndex={-1} ref={headingRef}>{t(`wizard.${step}.title`)}</h2>
            <p className="ag-note">{t(`wizard.${step}.lead`)}</p>
            {step === 'purpose' && <PurposeStep form={form} updateField={updateField} picked={picked} onTemplate={onTemplate} models={models} modelsLoading={modelsLoading} />}
            {step === 'abilities' && <AbilitiesStep form={form} updateField={updateField} tools={tools} toolGroups={toolGroups} ab={ab} pristine={pristine} templateLevel={templateLevel} />}
            {step === 'where' && <WhereStep groups={where.groups} icons={where.icons} ready={where.ready} wanted={wanted} onToggle={toggleApp} />}
            {step === 'review' && <ReviewStep form={form} filesChoice={filesChoice} levelName={levelName} chosen={chosen} onLimit={limitFolders} modelName={form.model} />}

            <div className="aw-foot">
              <Button variant="ghost" onClick={index === 0 ? () => navigate('/app/agents') : back} data-testid="wizard-back">{index === 0 ? t('create.discard') : t('wizard.back')}</Button>
              {step === 'review'
                ? <Button variant="primary" icon="save" busy={saving} onClick={create} data-testid="wizard-create">{t('create.titleCreate')}</Button>
                : <Button variant="primary" iconEnd="arrow-right" disabled={!canNext} onClick={next} data-testid="wizard-next">{t('wizard.next')}</Button>}
            </div>
            {step === 'purpose' && !canNext && <p className="ag-note" data-testid="wizard-needs">{t('wizard.purpose.needs')}</p>}
          </section>

          <aside className="aw-rail" aria-label={t('wizard.rail.title')} data-testid="wizard-rail">
            <p className="ag-eyebrow">{t('wizard.rail.title')}</p>
            <h3 className="ag-h3 aw-rail__name" data-testid="wizard-rail-name">{form.name.trim() || t('wizard.rail.unnamed')}</h3>
            <dl className="aw-rail__list">
              <div><dt>{t('wizard.rail.abilities')}</dt><dd data-testid="wizard-rail-level">{levelName}</dd></div>
              <div><dt>{t('create.model')}</dt><dd>{form.model || t('wizard.rail.modelEach')}</dd></div>
              <div><dt>{t('wizard.rail.files')}</dt><dd data-testid="wizard-rail-files">{filesText}</dd></div>
              <div>
                <dt>{t('wizard.rail.works')}</dt>
                <dd data-testid="wizard-rail-apps">
                  <span className="aw-chip">{t('wizard.rail.thisApp')}</span>
                  {chosen.map((c) => <span key={c.key} className="aw-chip">{c.title}</span>)}
                </dd>
              </div>
            </dl>
          </aside>
        </div>
      </div>
    </Page>
  )
}
