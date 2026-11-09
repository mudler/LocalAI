/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import { activeProfile, disabledReason, fellBack, percentOf, phaseOf, profileById, profileMode, profilesOf, pulling, sourceOf } from '../../utils/chatSandboxProfile'
import './sandboxprofile.css'

// The Sandbox profile row of the chat's Tools menu, next to the Network row:
// the environment the chat's isolated commands run in (which programs they
// have). It lists the profiles with what each has, says what an image that is
// being prepared is doing, and says in words, before the choice is made, what a
// switch costs. A chat that is not yet started shows the default only.
export default function SandboxProfileRow({ view }) {
  const { t } = useTranslation('chats')
  const labelId = useId()
  const [pending, setPending] = useState('')
  if (profileMode(view) === 'hidden') return null
  const active = activeProfile(view)
  const draft = Boolean(view.draft)
  const shown = profileById(view, pending) || active
  const busy = pulling(view)
  const fb = fellBack(view) ? profileById(view, view.selected || view.default) || active : null
  const pickable = (p) => !draft && !disabledReason(p)
  const choose = (p) => {
    if (p.id === active?.id) { setPending(''); return }
    setPending(p.id)
  }
  const confirm = async () => {
    const id = pending
    setPending('')
    await view.setProfile(id)
  }
  return (
    <div className="chat-sandbox-profile" data-testid="chat-sandbox-profile">
      <div className="chat-sandbox-profile__head">
        <span id={labelId} className="chat-sandbox-profile__label">{t('sandboxProfile.label')}</span>
        <div className="dk-segmented" role="radiogroup" aria-labelledby={labelId}>
          {profilesOf(view).map(p => {
            const why = disabledReason(p)
            return (
              <button
                key={p.id} type="button" role="radio" className="dk-seg"
                aria-checked={(pending || active?.id) === p.id} aria-pressed={(pending || active?.id) === p.id}
                disabled={!pickable(p)} title={why ? t(`sandboxProfile.${why}`) : p.description}
                onClick={() => choose(p)} data-testid={`chat-sandbox-profile-${p.id}`}
              >
                {p.title}
              </button>
            )
          })}
        </div>
      </div>
      {shown && <p className="chat-sandbox-profile__note" data-testid="chat-sandbox-profile-about">{shown.description}</p>}
      {shown && (
        <p className="chat-sandbox-profile__note" data-testid="chat-sandbox-profile-tools">
          {t('sandboxProfile.tools', { tools: (shown.tools || []).join(', ') })}
        </p>
      )}
      {profilesOf(view).filter(p => disabledReason(p)).map(p => (
        <p key={p.id} className="chat-sandbox-profile__note" data-testid={`chat-sandbox-profile-why-${p.id}`}>
          {t('sandboxProfile.unavailable', { title: p.title, reason: t(`sandboxProfile.${disabledReason(p)}`) })}
        </p>
      ))}
      {draft && <p className="chat-sandbox-profile__note" data-testid="chat-sandbox-profile-draft">{t('sandboxProfile.draft', { title: active?.title })}</p>}
      {!draft && <p className="chat-sandbox-profile__note" data-testid="chat-sandbox-profile-source">{t(`sandboxProfile.source.${sourceOf(view)}`, { title: active?.title })}</p>}
      {pending && (
        <div className="chat-sandbox-profile__confirm" data-testid="chat-sandbox-profile-confirm">
          <p className="chat-sandbox-profile__warn" role="status" data-testid="chat-sandbox-profile-warning">{t('sandboxProfile.switchWarning')}</p>
          <div className="chat-sandbox-profile__actions">
            <Button size="sm" variant="primary" onClick={confirm} data-testid="chat-sandbox-profile-confirm-yes">
              {t('sandboxProfile.switchTo', { title: profileById(view, pending)?.title })}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setPending('')} data-testid="chat-sandbox-profile-confirm-no">{t('sandboxProfile.cancel')}</Button>
          </div>
        </div>
      )}
      {busy.map(p => (
        <div key={p.id} className="chat-sandbox-profile__progress" data-testid={`chat-sandbox-profile-pulling-${p.id}`}>
          <progress value={percentOf(p)} max="100" aria-label={t('sandboxProfile.preparing', { title: p.title, percent: percentOf(p) })} />
          <span>{t('sandboxProfile.preparing', { title: p.title, percent: percentOf(p) })}</span>
        </div>
      ))}
      {profilesOf(view).filter(p => phaseOf(p) === 'failed').map(p => (
        <p key={p.id} className="chat-sandbox-profile__error" role="alert" data-testid={`chat-sandbox-profile-failed-${p.id}`}>
          {t('sandboxProfile.failed', { title: p.title, error: p.state?.error || '' })}
        </p>
      ))}
      {fb && (
        <Callout tone="warn" testId="chat-sandbox-profile-fallback">
          {t('sandboxProfile.fallback', { title: fb.title, base: profilesOf(view)[0]?.title })}
          {view.fallback_reason ? ` ${view.fallback_reason}` : ''}
        </Callout>
      )}
      {view.newer_image && <p className="chat-sandbox-profile__note" data-testid="chat-sandbox-profile-newer">{t('sandboxProfile.newerImage')}</p>}
      {view.error && <p className="chat-sandbox-profile__error" role="alert" data-testid="chat-sandbox-profile-error">{t(`sandboxProfile.${view.error}`)}</p>}
    </div>
  )
}
