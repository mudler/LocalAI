/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import { P2P_PATH } from '../p2p/P2PTabs'
import '../p2p/p2p.css'

const AUTH_DOCS_URL = 'https://localai.io/features/authentication/'

// The optional last step of the local first run: use this server from other
// machines over P2P. Sharing accepts paired devices, so it needs
// authentication; without it the step says so and points to the way that
// needs none, using another instance's models from "On another server".
export default function HomeShareStep({ authEnabled }) {
  const { t } = useTranslation('p2p')
  return (
    <li className="home-step home-step--optional" data-state="todo" data-testid="home-step-share">
      <span className="home-step__num home-step__num--optional" aria-hidden="true" />
      <div className="home-step__main">
        <b>{t('home.title')} <span className="dk-badge">{t('home.optional')}</span></b>
        <span className="home-step__desc">{t('home.body')}</span>
        <div className="home-step__extra">
          {authEnabled ? (
            <>
              <Button size="sm" variant="ghost" icon="network" to={P2P_PATH} data-testid="home-step-share-link">{t('home.open')}</Button>
              <span className="home-step__desc">{t('home.openNote')}</span>
            </>
          ) : (
            <>
              <Button size="sm" variant="ghost" icon="key" href={AUTH_DOCS_URL} target="_blank" rel="noopener noreferrer" data-testid="home-step-share-auth">{t('home.auth')}</Button>
              <span className="home-step__desc" data-testid="home-step-share-noauth">{t('home.authNote')}</span>
            </>
          )}
        </div>
      </div>
    </li>
  )
}
