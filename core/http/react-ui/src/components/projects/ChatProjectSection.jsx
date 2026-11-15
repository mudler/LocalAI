/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { PULLED_MAX, composerProjects, homeProject, isBusy, pulledChoices, pulledProjects, withPulled } from '../../utils/projects'
import { sharedChoices } from '../../utils/sharedProjects'
import './projectchat.css'

// The Project section of the chat settings: the home project of the chat and
// the projects it pulls in. A change goes to the server at once (the home
// project with PUT /api/chats/{id}/project, the pulled list with PUT
// /api/chats/{id}/projects) and applies from the next message. The server
// refuses while a turn runs; that shows as a line here, and the controls are
// disabled meanwhile. Nothing is drawn when projects are not available.
//
//   chat        the chat record ({ project_id, pulled_projects })
//   list        the result of useProjectList
//   running     a turn is running
//   onProject   (id) => Promise, '' takes the chat out of its project
//   onPulled    (ids) => Promise
export default function ChatProjectSection({ chat, list, running, onProject, onPulled }) {
  const { t } = useTranslation('projects')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [add, setAdd] = useState('')
  const { projects, status, shared } = list
  if (status !== 'ready' || !projects) return null

  const pulled = chat.pulled_projects || []
  const home = homeProject(chat, projects)
  const gone = Boolean(chat.project_id) && !home
  const homeOptions = composerProjects(projects)
  const choices = pulledChoices(projects, chat)
  const sharedOptions = sharedChoices(shared, chat)
  const hasChoices = choices.length + sharedOptions.length > 0
  const locked = busy || running
  const full = pulled.length >= PULLED_MAX

  const run = async (fn) => {
    setBusy(true)
    setError('')
    try {
      await fn()
    } catch (err) {
      setError(isBusy(err) ? t('settings.busy') : t('settings.failed', { message: err.message || String(err) }))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="cxs-sec" data-testid="chat-project-settings">
      <h3 className="cxs-sec__title">{t('settings.title')}</h3>
      <p className="dk-hint cxs-sec__lead">{t('settings.lead')}</p>
      <div className="dk-field cxs-field">
        <label className="dk-label" htmlFor="cx-set-project">{t('settings.home')}</label>
        <div className="dk-select-wrap">
          <select
            id="cx-set-project"
            className="dk-select"
            value={chat.project_id && home ? chat.project_id : ''}
            disabled={locked}
            onChange={(e) => run(() => onProject(e.target.value))}
          >
            <option value="">{t('composer.none')}</option>
            {home && !homeOptions.some(p => p.id === home.id) && <option value={home.id}>{home.name}</option>}
            {homeOptions.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </div>
        <p className="dk-hint">{t('settings.homeHint')}</p>
        {gone && <p className="dk-hint" data-testid="chat-project-gone">{t('settings.gone')}</p>}
      </div>

      <div className="dk-field cxs-field">
        <span className="dk-label" id="cx-set-pulled-label">{t('settings.pulled')}</span>
        <p className="dk-hint">{t('settings.pulledHint', { max: PULLED_MAX })}</p>
        {pulled.length === 0
          ? <p className="dk-hint">{t('settings.pulledNone')}</p>
          : (
            <ul className="pjc-pulled" aria-labelledby="cx-set-pulled-label" data-testid="chat-pulled-list">
              {pulledProjects(chat, projects, shared).map(p => (
                <li key={p.id} className="pjc-pulled__row" data-testid="chat-pulled-row" data-state={p.state}>
                  <span className="pjc-pulled__name" data-state={p.state}>
                    <Icon name={p.state === 'mounted' ? (p.shared ? 'users' : 'lock') : 'alert-circle'} />
                    {p.state === 'unlisted' ? t('chat.unlistedName') : p.name}
                    {p.shared && p.state !== 'unlisted' && <span className="pjc-badge" data-testid="chat-pulled-shared-badge">{t('share.badge')}</span>}
                    {p.state !== 'mounted' && <small className="pjc-pulled__state" data-testid="chat-pulled-state">{t(p.state === 'unlisted' ? 'chat.unlistedShort' : p.state === 'archived' ? 'chat.pulledArchived' : 'chat.pulledUnavailable')}</small>}
                  </span>
                  {p.shared && p.state === 'mounted' && <small className="pjc-pulled__by">{t('share.byAccess', { owner: p.owner, access: t(p.mode === 'write' ? 'share.canEdit' : 'share.readOnly') })}</small>}
                  <Button size="sm" variant="ghost" icon="close" disabled={locked} aria-label={t('settings.remove', { name: p.state === 'unlisted' ? t('chat.unlistedName') : p.name })} onClick={() => run(() => onPulled(withPulled(pulled, p.id, false)))} />
                </li>
              ))}
            </ul>
          )}
        {pulledProjects(chat, projects, shared).some(p => p.shared) && <p className="dk-hint" data-testid="chat-pulled-shared-hint">{t('settings.sharedHint')}</p>}
        {full
          ? <p className="dk-hint">{t('settings.limit', { max: PULLED_MAX })}</p>
          : !hasChoices
            ? <p className="dk-hint">{t('settings.noneToAdd')}</p>
            : (
              <div className="pjc-add">
                <div className="dk-select-wrap">
                  <select className="dk-select" aria-label={t('settings.addLabel')} value={add} disabled={locked} onChange={(e) => setAdd(e.target.value)}>
                    <option value="">{t('settings.addPlaceholder')}</option>
                    {choices.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
                    {sharedOptions.length > 0 && (
                      <optgroup label={t('settings.sharedGroup')}>
                        {sharedOptions.map(p => <option key={p.ref} value={p.ref}>{t('settings.sharedOption', { name: p.name, owner: p.owner_name })}</option>)}
                      </optgroup>
                    )}
                  </select>
                </div>
                <Button size="sm" disabled={locked || !add} onClick={() => run(async () => { await onPulled(withPulled(pulled, add, true)); setAdd('') })}>{t('settings.add')}</Button>
              </div>
            )}
      </div>
      {running && <p className="dk-hint" data-testid="chat-project-running">{t('settings.busy')}</p>}
      {error && <Callout tone="error" role="alert" testId="chat-project-error">{error}</Callout>}
    </section>
  )
}
