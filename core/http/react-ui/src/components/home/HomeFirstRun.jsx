import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import StarterModels from '../StarterModels'
import Icon from '../Icon'

// First run: no model is installed yet. Three steps, with the recommended
// models for the detected hardware inside the second one, so the way forward
// is on the page without a separate wizard.
export default function HomeFirstRun({ addToast, onInstallStarted, onGallery, onImport }) {
  const { t } = useTranslation('home')
  return (
    <ol className="home-guide" aria-label={t('wizard.guideLabel')} data-testid="home-first-run">
      <li className="home-step" data-state="now" aria-current="step">
        <span className="home-step__num" aria-hidden="true">1</span>
        <div className="home-step__main">
          <b>{t('wizard.steps.step1Title')}</b>
          <span className="home-step__desc">{t('wizard.steps.step1Body')}</span>
          <div className="home-step__extra">
            <button type="button" className="home-secondary home-secondary--sm" onClick={onGallery}>
              <Icon name="store" /> {t('wizard.browseGallery')}
            </button>
            <button type="button" className="home-ghost" onClick={onImport}>
              <Icon name="upload" /> {t('wizard.importModel')}
            </button>
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
    </ol>
  )
}
