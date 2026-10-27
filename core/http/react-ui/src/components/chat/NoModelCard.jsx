import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
// eslint-disable-next-line no-unused-vars
import StarterModels from '../StarterModels'
import Icon from '../Icon'

// Chat has nothing to talk to: no chat model is installed. Say so, offer the
// way forward (the starter models for this hardware, the gallery, importing
// your own) and leave the composer where it is, so what you typed stays.
export default function NoModelCard({ isAdmin, addToast, onInstallStarted }) {
  const { t } = useTranslation('chat')
  const navigate = useNavigate()
  return (
    <section className="cx-nomodel" data-testid="chat-no-model" aria-labelledby="chat-no-model-title">
      <h2 id="chat-no-model-title">{t('noModel.title')}</h2>
      <p>{isAdmin ? t('noModel.body') : t('noModel.bodyMember')}</p>
      {isAdmin && (
        <>
          <StarterModels addToast={addToast} onInstallStarted={onInstallStarted} />
          <div className="cx-nomodel__acts">
            <button type="button" className="home-secondary home-secondary--sm" onClick={() => navigate('/app/models')}>
              <Icon name="store" /> {t('noModel.gallery')}
            </button>
            <button type="button" className="home-ghost" onClick={() => navigate('/app/import-model')}>
              <Icon name="upload" /> {t('noModel.import')}
            </button>
          </div>
        </>
      )}
    </section>
  )
}
