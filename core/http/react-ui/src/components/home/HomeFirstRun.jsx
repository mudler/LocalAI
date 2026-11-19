import { Suspense, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { useBranding, isDefaultBranding } from '../../contexts/BrandingContext'
import Mascot from '../ui/Mascot'
import { useHubAuth } from '../hub/useHubAuth'
// eslint-disable-next-line no-unused-vars
import StarterModels from '../StarterModels'
// eslint-disable-next-line no-unused-vars
import Button from '../ui/Button'
import Segmented from '../ui/Segmented'
import RemoteConnectStep from '../remotes/RemoteConnectStep'
import RemoteChooseStep from '../remotes/RemoteChooseStep'
import { useRemoteConnect } from '../remotes/useRemoteConnect'
import { useRemotes } from '../../hooks/useRemotes'

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
//
// The remote steps read their own strings, which load when they first show.
// Their boundary keeps that wait to them: without it the route's boundary
// hides all of Home, and the switch above loses the focus.
//
// An administrator can also say the models run somewhere else (another
// server or a hosted service). That swaps steps 1 and 2 for "connect a
// remote" and "choose models"; the steps after them stay. `onRemoteDone` runs
// with the result of the remote, so the page can say so and read the
// models again: they now exist, and the guide goes away on its own.
export default function HomeFirstRun({ addToast, onInstallStarted, onGallery, onImport, onRemoteDone }) {
  const { t } = useTranslation('home')
  const { authEnabled, hasFeature, isAdmin } = useAuth()
  const branding = useBranding()
  const showMascot = isDefaultBranding(branding)
  const [where, setWhere] = useState('local')
  const whereId = useId()
  const remote = useRemoteConnect({ onDone: onRemoteDone })
  // A server without the remote routes offers no such choice.
  const { unsupported } = useRemotes({ enabled: isAdmin })
  const canRemote = isAdmin && !unsupported
  const remoteMode = canRemote && where === 'remote'
  const { features, featuresLoaded } = useHubAuth()
  const showAgents = authEnabled
  const showAddons = featuresLoaded && features.addons !== false && hasFeature('addons')
  return (
    <ol className="home-guide" aria-label={t('wizard.guideLabel')} data-testid="home-first-run">
      {canRemote && (
        <li className="home-where" role="presentation" data-testid="home-where">
          <div className="home-step__main">
            <b id={whereId}>{t('remoteSetup.where.title')}</b>
            <div className="home-step__extra">
              <Segmented
                mode="radio"
                labelledBy={whereId}
                value={where}
                onChange={setWhere}
                options={[
                  { id: 'local', label: t('remoteSetup.where.local') },
                  { id: 'remote', label: t('remoteSetup.where.remote') },
                ]}
                testId="home-where-choice"
                itemTestId="home-where-"
              />
            </div>
            {remoteMode && (
              <>
                <span className="home-step__desc" data-testid="home-remote-intro">{t('remoteSetup.where.remoteIntro')}</span>
                <span className="home-step__desc">{t('remoteSetup.where.remoteNote')}</span>
              </>
            )}
          </div>
        </li>
      )}
      {remoteMode ? (
        <>
          <li className={`home-step${showMascot ? ' home-step--lead' : ''}`} data-state="now" aria-current="step" data-testid="home-step-connect">
            {showMascot && <Mascot pose="circle" size="badge" className="home-step__mascot" />}
            <span className="home-step__num" aria-hidden="true">1</span>
            <div className="home-step__main">
              <b>{t('remoteSetup.step1Title')}</b>
              <span className="home-step__desc">{t('remoteSetup.step1Body')}</span>
              <div className="home-step__extra home-step__extra--block">
                <Suspense fallback={null}><RemoteConnectStep remote={remote} /></Suspense>
              </div>
            </div>
          </li>
          <li className="home-step" data-state={remote.phase === 'ok' ? 'now' : 'todo'} data-testid="home-step-choose">
            <span className="home-step__num" aria-hidden="true">2</span>
            <div className="home-step__main">
              <b>{t('remoteSetup.step2Title')}</b>
              <span className="home-step__desc">{t('remoteSetup.step2Body')}</span>
              <div className="home-step__extra home-step__extra--block">
                <Suspense fallback={null}><RemoteChooseStep remote={remote} /></Suspense>
              </div>
            </div>
          </li>
        </>
      ) : (<>
      <li className={`home-step${showMascot ? ' home-step--lead' : ''}`} data-state="now" aria-current="step">
        {showMascot && <Mascot pose="circle" size="badge" className="home-step__mascot" />}
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
      </>)}
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
            <span className="home-step__desc">{t('wizard.steps.step5Body')}</span>
            <div className="home-step__extra">
              <Button size="sm" icon="puzzle" to={ADDONS_PATH} data-testid="home-step-addons-link">{t('wizard.browseAddons')}</Button>
            </div>
          </div>
        </li>
      )}
    </ol>
  )
}
