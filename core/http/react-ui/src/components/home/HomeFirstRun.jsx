import { useContext, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { RegistryContext, EMPTY_LIST } from '../../addons/registryContext'
import { SANDBOX_INSTALL_LINK, SANDBOX_ADDON_ID } from '../../utils/chatCommands'
// eslint-disable-next-line no-unused-vars
import StarterModels from '../StarterModels'
// eslint-disable-next-line no-unused-vars
import Button from '../ui/Button'

const SANDBOX_SKIP_KEY = 'localai.firstRun.sandboxSkipped'

function readSkipped() {
  try { return localStorage.getItem(SANDBOX_SKIP_KEY) === '1' } catch { return false }
}

// The optional step "Run code safely": offered while the Sandbox add-on is not
// installed and the person has not skipped it. Nothing is shown before the
// add-on list has loaded, so an installed Sandbox never flashes the step.
function useSandboxStep() {
  const registry = useContext(RegistryContext)
  const acquire = registry ? registry.acquire : null
  const allowed = registry ? registry.allowed : false
  useEffect(() => (acquire && allowed ? acquire() : undefined), [acquire, allowed])
  const [skipped, setSkipped] = useState(readSkipped)
  const skip = () => {
    try { localStorage.setItem(SANDBOX_SKIP_KEY, '1') } catch { /* private mode: skip for this visit only */ }
    setSkipped(true)
  }
  const apps = registry ? registry.apps : EMPTY_LIST
  const installed = apps.some(a => a.id === SANDBOX_ADDON_ID)
  const show = Boolean(registry) && allowed && registry.status === 'ready' && !installed && !skipped
  return { show, skip }
}

// First run: no model is installed yet. Three steps, with the recommended
// models for the detected hardware inside the second one, so the way forward
// is on the page without a separate wizard.
export default function HomeFirstRun({ addToast, onInstallStarted, onGallery, onImport }) {
  const { t } = useTranslation('home')
  const navigate = useNavigate()
  const sandbox = useSandboxStep()
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
      {sandbox.show && (
        <li className="home-step" data-state="todo" data-testid="home-first-run-sandbox">
          <span className="home-step__num" aria-hidden="true">4</span>
          <div className="home-step__main">
            <b>{t('wizard.steps.sandboxTitle')}</b>
            <span className="home-step__desc">{t('wizard.steps.sandboxBody')}</span>
            <div className="home-step__extra">
              <Button size="sm" icon="shield" onClick={() => navigate(SANDBOX_INSTALL_LINK)}>{t('wizard.sandboxInstall')}</Button>
              <Button size="sm" variant="ghost" onClick={sandbox.skip}>{t('wizard.sandboxSkip')}</Button>
            </div>
          </div>
        </li>
      )}
    </ol>
  )
}
