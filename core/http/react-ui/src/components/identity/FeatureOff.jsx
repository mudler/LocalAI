import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

// Shown in place of a page whose feature the signed-in user may not use. It
// replaces a silent redirect: the person learns what is off and what turns it on.
export default function FeatureOff({ feature }) {
  const { t } = useTranslation('biometrics')
  return (
    <main className="page idn-page idn-off" data-testid="feature-off">
      <section className="idn-needed dk-card">
        <span className="dk-badge dk-badge--warn"><Icon name="lock" /> {t('off.badge')}</span>
        <h1 className="idn-h2">{t(`off.${feature}.title`)}</h1>
        <p className="idn-needed__text">{t(`off.${feature}.text`)}</p>
        <details className="idn-disclosure" open>
          <summary><Icon name="chevron-right" /> {t('needed.whatTitle')}</summary>
          <p className="idn-disclosure__text">{t(`off.${feature}.what`)}</p>
        </details>
      </section>
    </main>
  )
}
