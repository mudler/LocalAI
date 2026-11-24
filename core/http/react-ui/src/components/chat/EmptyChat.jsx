import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import HomeResume from '../home/HomeResume'
// eslint-disable-next-line no-unused-vars
import NoModelCard from './NoModelCard'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import { AssistantAvatar } from '../wellknown/Identity'

// The top of an empty chat: one line that says what the page is for. With
// an identity (persona.json) it shows the assistant's picture, its own
// headline and subtitle, and its greeting as the first line of the chat; the
// greeting is shown only, never sent to the model. fallbackTitle is the
// override of ui.json strings, used when the identity has no headline.
export function EmptyHead({ name, manage, identity, fallbackTitle }) {
  const { t } = useTranslation('chat')
  const id = manage ? null : identity
  const pictured = !!(id && (id.avatar || id.name))
  const title = manage ? t('empty.manageTitle') : (id?.emptyTitle || fallbackTitle || t('empty.title'))
  return (
    <div className={`cx-empty${pictured ? ' cx-empty--identity' : ''}`} data-testid="chat-empty">
      {pictured && <AssistantAvatar avatar={id.avatar} name={id.name} size="lg" testId="chat-empty-avatar" />}
      <h1 data-testid="chat-empty-title">{title}</h1>
      <span className="cx-empty__name">{name}</span>
      {id?.emptySubtitle && <p className="cx-empty__sub" data-testid="chat-empty-subtitle">{id.emptySubtitle}</p>}
      {id?.greeting && (
        <div className="cx-greeting" data-testid="chat-greeting">
          <AssistantAvatar avatar={id.avatar} name={id.name} size="xs" />
          <div>
            {id.name && <b>{id.name}</b>}
            <p>{id.greeting}</p>
          </div>
        </div>
      )}
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
