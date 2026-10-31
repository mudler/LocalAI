import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import HomeResume from '../home/HomeResume'
// eslint-disable-next-line no-unused-vars
import NoModelCard from './NoModelCard'
import Icon from '../Icon'

// The top of an empty chat: one line that says what the page is for.
export function EmptyHead({ name, manage }) {
  const { t } = useTranslation('chat')
  return (
    <div className="cx-empty" data-testid="chat-empty">
      <h1>{manage ? t('empty.manageTitle') : t('empty.title')}</h1>
      <span className="cx-empty__name">{name}</span>
    </div>
  )
}

// Whether the model of a new chat is loaded, or loads with the first reply.
export function ReadyLine({ model, warm }) {
  const { t } = useTranslation('chat')
  if (!model) return null
  return (
    <p className="cx-ready" data-testid="chat-ready">
      <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />
      <span>{warm ? t('empty.warmReady', { model }) : t('empty.coldReady', { model })}</span>
    </p>
  )
}

// Under the composer of an empty chat: whether the model is ready, and the
// conversations you can go back to. The starters moved into the shortcuts row
// above the composer. With no model installed it is the install card instead.
export function EmptyUnder({
  noModel, isAdmin, addToast, onInstallStarted, manage, model, warm,
  conversations, onResume,
}) {
  const { t } = useTranslation('chat')
  if (noModel) {
    return <NoModelCard isAdmin={isAdmin} addToast={addToast} onInstallStarted={onInstallStarted} />
  }
  return (
    <>
      {manage && <p className="cx-empty__text">{t('empty.manageText')}</p>}
      <ReadyLine model={model} warm={warm} />
      {conversations.length > 0 && (
        <HomeResume items={conversations} onResume={onResume} />
      )}
      <p className="cx-empty__hint"><Icon name="keyboard" /> {t('empty.hintKeys')}</p>
    </>
  )
}
