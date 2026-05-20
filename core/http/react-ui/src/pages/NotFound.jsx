import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

export default function NotFound() {
  const navigate = useNavigate()
  const { t } = useTranslation('auth')

  return (
    <div className="page page--narrow">
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-compass" /></div>
        <h1 className="empty-state-title" style={{ fontSize: '3rem' }}>404</h1>
        <h2 className="empty-state-title">{t('notFound.title')}</h2>
        <p className="empty-state-text">{t('notFound.text')}</p>
        <button className="btn btn-primary" onClick={() => navigate('/app')}>
          <i className="fas fa-home" /> {t('notFound.goHome')}
        </button>
      </div>
    </div>
  )
}
