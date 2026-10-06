import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'

export default function NotFound() {
  const navigate = useNavigate()
  const { t } = useTranslation('auth')

  return (
    <div className="page page--narrow">
      <div className="empty-state">
        <div className="empty-state-icon"><Icon name="compass" /></div>
        <h1 className="empty-state-title" style={{ fontSize: '3rem' }}>404</h1>
        <h2 className="empty-state-title">{t('notFound.title')}</h2>
        <p className="empty-state-text">{t('notFound.text')}</p>
        <button className="btn btn-primary" onClick={() => navigate('/app')}>
          <Icon name="home" /> {t('notFound.goHome')}
        </button>
      </div>
    </div>
  )
}
