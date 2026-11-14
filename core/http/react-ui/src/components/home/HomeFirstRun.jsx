import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { useHubAuth } from '../hub/useHubAuth'
// eslint-disable-next-line no-unused-vars
import StarterModels from '../StarterModels'
// eslint-disable-next-line no-unused-vars
import Button from '../ui/Button'

// Add-ons that ship in the add-on library, named as in the docs catalogue.
// Only names, so nothing here goes stale with a version.
export const ADDON_EXAMPLES = ['Sandbox', 'Telegram']
// The guide that prints a coding agent's config, under API keys in Account.
export const AGENTS_GUIDE_PATH = '/app/account?tab=keys'
export const ADDONS_PATH = '/app/addons'
export const CODING_AGENTS = 'Codex, Claude Code, opencode, Hermes Agent, nib, pi'

// First run: no model is installed yet. Three steps, with the recommended
// model for the detected hardware inside the second one, so the way forward
// is on the page without a separate wizard. Two more steps say what comes
// after: connect a coding agent, and extend LocalAI with an add-on. The
// agents step needs accounts (the guide lives under API keys), and the add-on
// step needs the add-ons feature, so each one is left out when its target is
// not there.
export default function HomeFirstRun({ addToast, onInstallStarted, onGallery, onImport }) {
  const { t } = useTranslation('home')
  const { authEnabled, hasFeature } = useAuth()
  const { features, featuresLoaded } = useHubAuth()
  const showAgents = authEnabled
  const showAddons = featuresLoaded && features.addons !== false && hasFeature('addons')
  return (
    <ol className="home-guide" aria-label={t('wizard.guideLabel')} data-testid="home-first-run">
      <li className="home-step" data-state="now" aria-current="step">
        <span className="home-step__num" aria-hidden="true">1</span>
        <div className="home-step__main">
          <b>{t('wizard.steps.step1Title')}</b>
          <span className="home-step__desc">{t('wizard.steps.step1Body')}</span>
          <div className="home-step__extra">
            <Button size="sm" icon="store" onClick={onGallery}>{t('wizard.browseGallery')}</Button>
            <Button size="sm" variant="ghost" icon="upload" onClick={onImport}>{t('wizard.importModel')}</Button>
          </div>
        </div>
      </li>
      <li className="home-step" data-state="todo">
        <span className="home-step__num" aria-hidden="true">2</span>
        <div className="home-step__main">
          <b>{t('wizard.steps.step2Title')}</b>
          <span className="home-step__desc">{t('wizard.steps.step2Body')}</span>
          <div className="home-step__extra">
            <StarterModels addToast={addToast} onInstallStarted={onInstallStarted} />
          </div>
        </div>
      </li>
      <li className="home-step" data-state="todo">
        <span className="home-step__num" aria-hidden="true">3</span>
        <div className="home-step__main">
          <b>{t('wizard.steps.step3Title')}</b>
          <span className="home-step__desc">{t('wizard.steps.step3Body')}</span>
        </div>
      </li>
      {showAgents && (
        <li className="home-step" data-state="todo" data-testid="home-step-agents">
          <span className="home-step__num" aria-hidden="true">4</span>
          <div className="home-step__main">
            <b>{t('wizard.steps.step4Title')}</b>
            <span className="home-step__desc">{t('wizard.steps.step4Body', { agents: CODING_AGENTS })}</span>
            <div className="home-step__extra">
              <Button size="sm" icon="key" to={AGENTS_GUIDE_PATH} data-testid="home-step-agents-link">{t('wizard.connectAgents')}</Button>
            </div>
          </div>
        </li>
      )}
      {showAddons && (
        <li className="home-step" data-state="todo" data-testid="home-step-addons">
          <span className="home-step__num" aria-hidden="true">{showAgents ? 5 : 4}</span>
          <div className="home-step__main">
            <b>{t('wizard.steps.step5Title')}</b>
            <span className="home-step__desc">{t('wizard.steps.step5Body', { examples: ADDON_EXAMPLES.join(', ') })}</span>
            <div className="home-step__extra">
              <Button size="sm" icon="puzzle" to={ADDONS_PATH} data-testid="home-step-addons-link">{t('wizard.browseAddons')}</Button>
            </div>
          </div>
        </li>
      )}
    </ol>
  )
}
