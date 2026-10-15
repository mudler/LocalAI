// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import Icon from '../Icon'
import './tools.css'

// Shown in place of a Build tool the signed-in person may not use. It replaces a
// silent redirect: it says that the account lacks the permission and who can
// grant it, and points at the tools the person can open. `tool` is 'fineTune' or
// 'quantize'.
export default function ToolOff({ tool }) {
  const { t } = useTranslation('tools')
  const { isAdmin } = useAuth()
  return (
    <main className="page page--medium bt-page bt-off" data-testid="tool-off" data-cause="account">
      <section className="bt-off__card dk-card">
        <span className="dk-badge dk-badge--warn"><Icon name="lock" /> {t('off.badge')}</span>
        <h1 className="bt-h1">{t(`off.${tool}.title`)}</h1>
        <p className="bt-off__text">{t(`off.${tool}.text`)}</p>
        <p className="dk-hint">{isAdmin ? t('off.admin') : t('off.askAdmin')}</p>
        <div className="bt-off__acts">
          <Link className="dk-btn dk-btn--primary" to="/app"><Icon name="home" /> {t('off.back')}</Link>
        </div>
      </section>
    </main>
  )
}
