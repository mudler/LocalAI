// SPDX-License-Identifier: MIT
/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { Button, Callout, EmptyState } from '../ui'
import {
  EVERYONE, cleanReferences, cleanRoles, describeShareError, freshUsers,
  referenceChoices, referencesReady, searchText, splitGrants,
} from './shareModel'
import './share.css'

// The one Share dialog of the app: who has access to one resource, their
// role, a picker that finds named accounts and, where the kind allows it, the
// grant to everyone. It does not know the routes: `client` carries the four
// calls (see shareClients.js), so a host page passes kindShareClient and an
// add-on passes sdk.resources.shareClient. The server decides every call.
//
//   client        { grants(), setGrant(principal, role), revoke(principal), searchUsers(query) }
//   roles         [{ id, label, help }] the kind grants, in order (the first is the default)
//   allowEveryone true when the kind may be shared with every account
//   resource      { id, name }
//   sharing       false in local-owner mode: the dialog says so and calls nothing
//   references    rows the owner decides before sharing: [{ id, label, kind, options }]
//                 with options out of view, use, none
//   onShare       ({ principal, role, choices }) after a new grant, with the choice of every reference
//   onChanged     after any change
//   onLost        (error) when the person can no longer share this resource here
//   close         closes the dialog
//   title, intro  replace the default head
export default function ShareDialog({
  client, roles, allowEveryone = false, resource, sharing = true, references, onShare,
  onChanged, onLost, close, title, intro, testId = 'share-dialog',
}) {
  const { t } = useTranslation('common')
  const labelId = useId()
  const name = resource?.name || t('share.untitled')
  const [busy, setBusy] = useState('')

  return (
    <Dialog
      title={title || t('share.title', { name })}
      description={intro || (sharing ? t('share.intro') : undefined)}
      onClose={close}
      pending={busy !== ''}
      closeLabel={t('actions.close')}
      labelId={labelId}
      testId={testId}
      foot={<Button onClick={close} disabled={busy !== ''}>{t('actions.close')}</Button>}
    >
      {sharing
        ? <SharePanel client={client} roles={roles} allowEveryone={allowEveryone} references={references}
            onShare={onShare} onChanged={onChanged} onLost={onLost} busy={busy} setBusy={setBusy} t={t} />
        : <EmptyState variant="unavailable" icon="users" level={4} title={t('share.disabled.title')} body={t('share.disabled.text')} testId="share-disabled" />}
    </Dialog>
  )
}

// A read whose latest call wins: an older answer, or one that arrives after
// the dialog closed, is dropped. The caller decides when it runs.
function useRead(fn) {
  const [s, setS] = useState({ state: 'idle', data: null, error: null })
  const seq = useRef(0)
  const fnRef = useRef(fn)
  fnRef.current = fn
  const run = useCallback(async () => {
    const n = ++seq.current
    setS(prev => ({ ...prev, state: 'loading', error: null }))
    try {
      const data = await fnRef.current()
      if (n === seq.current) setS({ state: data === null ? 'idle' : 'ready', data, error: null })
    } catch (error) {
      if (n === seq.current) setS(prev => ({ state: 'error', data: prev.data, error }))
    }
  }, [])
  useEffect(() => () => { seq.current++ }, [])
  const set = useCallback((data) => { seq.current++; setS({ state: 'ready', data, error: null }) }, [])
  return { ...s, reload: run, set }
}

function useDebounced(value, ms) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

function RoleSelect({ value, roles, onChange, disabled, label, testId }) {
  const known = roles.some(r => r.id === value)
  return (
    <span className="dk-select-wrap share-dlg__role">
      <select className="dk-select" value={value} disabled={disabled} aria-label={label} data-testid={testId}
        onChange={e => onChange(e.target.value)}>
        {known ? null : <option value={value}>{value}</option>}
        {roles.map(r => <option key={r.id} value={r.id}>{r.label}</option>)}
      </select>
    </span>
  )
}

function SharePanel({ client, roles: rawRoles, allowEveryone, references: rawRefs, onShare, onChanged, onLost, busy, setBusy, t }) {
  const roles = useMemo(() => cleanRoles(rawRoles), [rawRoles])
  const refs = useMemo(() => cleanReferences(rawRefs), [rawRefs])
  const [newRole, setNewRole] = useState(roles[0]?.id || '')
  const [query, setQuery] = useState('')
  const [choices, setChoices] = useState({})
  const [confirming, setConfirming] = useState('')
  const [notice, setNotice] = useState('')
  const [actionError, setActionError] = useState(null)
  const searchId = useId()
  const hintId = useId()
  const pendingId = useId()
  const lostRef = useRef(onLost)
  lostRef.current = onLost

  const grants = useRead(() => client.grants())
  const typed = searchText(query)
  const debounced = useDebounced(typed, 250)
  const found = useRead(() => (debounced ? client.searchUsers(debounced) : Promise.resolve(null)))
  const loadGrants = grants.reload
  const search = found.reload
  useEffect(() => { loadGrants() }, [client, loadGrants])
  useEffect(() => { search() }, [client, debounced, search])

  useEffect(() => {
    if (grants.state === 'error' && describeShareError(grants.error).lost) lostRef.current?.(grants.error)
  }, [grants.state, grants.error])

  const { everyone, people } = splitGrants(grants.data)
  const all = everyone ? [everyone, ...people] : people
  const fresh = freshUsers(found.data, people)
  const ready = referencesReady(refs, choices)
  const roleLabel = (id) => roles.find(r => r.id === id)?.label || id
  const nameOf = (g) => (g.userId === EVERYONE ? t('share.everyone') : (g.displayName || t('share.unnamed')))
  const locked = busy !== ''

  const act = async (key, fn, message) => {
    setBusy(key)
    setActionError(null)
    setNotice('')
    try {
      const out = await fn()
      if (out && Array.isArray(out.grants)) grants.set(out)
      else await grants.reload()
      setNotice(message)
      onChanged?.()
      return true
    } catch (e) {
      if (describeShareError(e).lost) onLost?.(e)
      setActionError(e)
      return false
    } finally {
      setBusy('')
      setConfirming('')
    }
  }

  // A new grant. With references, the owner's choice for each one goes to
  // onShare right after the grant, in the same busy step.
  const add = async (principal, label) => {
    const ok = await act(`add:${principal}`, async () => {
      const out = await client.setGrant(principal, newRole)
      if (refs.length > 0 && onShare) await onShare({ principal, role: newRole, choices: referenceChoices(refs, choices) })
      return out
    }, t('share.granted', { name: label }))
    if (ok && principal !== EVERYONE) setQuery('')
  }

  const searching = typed !== '' && (typed !== debounced || found.state === 'loading')
  const blocked = refs.length > 0 && !ready

  return (
    <div className="share-dlg" data-testid="share-panel">
      {refs.length > 0 && (
        <section className="share-dlg__section share-dlg__refs" aria-labelledby={`${pendingId}-refs`} data-testid="share-refs">
          <h4 className="share-dlg__head" id={`${pendingId}-refs`}>{t('share.refs.title')}</h4>
          <p className="share-dlg__note">{t('share.refs.intro')}</p>
          <ul className="share-dlg__list">
            {refs.map(r => (
              <li key={r.id} className="share-dlg__row" data-testid={r.readOnly ? 'share-ref-own' : 'share-ref'} data-ref={r.id} data-choice={choices[r.id] || ''}>
                <span className="share-dlg__who">
                  <span className="share-dlg__words">
                    <span className="share-dlg__name">{r.label}</span>
                    {r.kind && <span className="share-dlg__meta">{r.kind}</span>}
                  </span>
                </span>
                {r.readOnly ? (
                  <span className="share-dlg__own">{r.note || t('share.refs.own')}</span>
                ) : (
                <span className="dk-select-wrap share-dlg__role">
                  <select className="dk-select" value={choices[r.id] || ''} disabled={locked}
                    aria-label={t('share.refs.label', { name: r.label })}
                    onChange={e => setChoices(c => ({ ...c, [r.id]: e.target.value }))}>
                    <option value="" disabled>{t('share.refs.choose')}</option>
                    {r.options.map(o => <option key={o} value={o}>{t(`share.refs.option.${o}`)}</option>)}
                  </select>
                </span>
                )}
              </li>
            ))}
          </ul>
          {blocked && <p className="share-dlg__note share-dlg__note--warn" id={pendingId} data-testid="share-refs-pending">{t('share.refs.pending')}</p>}
        </section>
      )}

      <section className="share-dlg__section" aria-labelledby={`${searchId}-head`}>
        <h4 className="share-dlg__head" id={`${searchId}-head`}>{t('share.add')}</h4>
        <div className="share-dlg__add">
          <label className="dk-sr-only" htmlFor={searchId}>{t('share.search')}</label>
          <span className="ui-search share-dlg__search">
            <Icon name="search" />
            <input id={searchId} type="search" className="dk-input ui-search__input" autoComplete="off" spellCheck={false}
              placeholder={t('share.search')} aria-describedby={hintId} maxLength={64} value={query}
              onChange={e => setQuery(e.target.value)} data-testid="share-search" />
          </span>
          {roles.length > 1 && (
            <RoleSelect value={newRole} roles={roles} onChange={setNewRole} disabled={locked} label={t('share.newRole')} testId="share-new-role" />
          )}
        </div>
        <p className="share-dlg__note" id={hintId}>{roles.find(r => r.id === newRole)?.help || t('share.searchHint')}</p>
        {searching && (
          <p className="share-dlg__status" role="status" data-testid="share-searching"><LoadingSpinner size="sm" /> {t('share.searching')}</p>
        )}
        {!searching && found.state === 'error' && <ShareProblem error={found.error} onRetry={found.reload} t={t} />}
        {!searching && found.state === 'ready' && debounced === typed && (fresh.length === 0
          ? <p className="share-dlg__note" role="status" data-testid="share-no-users">{(found.data?.users || []).length === 0 ? t('share.noResults') : t('share.alreadyShared')}</p>
          : (
            <ul className="share-dlg__list" data-testid="share-users" aria-label={t('share.results')}>
              {fresh.map(u => {
                const label = u.displayName || t('share.unnamed')
                return (
                  <li key={u.id} className="share-dlg__row" data-testid="share-user" data-user={u.id}>
                    <span className="share-dlg__who"><Icon name="user" className="share-dlg__icon" /><span className="share-dlg__name">{label}</span></span>
                    <Button size="sm" icon="plus" busy={busy === `add:${u.id}`} disabled={locked || blocked}
                      aria-label={t('share.addPerson', { name: label, role: roleLabel(newRole) })}
                      aria-describedby={blocked ? pendingId : undefined}
                      onClick={() => add(u.id, label)}>{t('actions.add')}</Button>
                  </li>
                )
              })}
            </ul>
          ))}
        {allowEveryone && !everyone && grants.state === 'ready' && (
          <div className="share-dlg__row share-dlg__everyone" data-testid="share-everyone-add">
            <span className="share-dlg__who">
              <Icon name="globe" className="share-dlg__icon" />
              <span className="share-dlg__words">
                <span className="share-dlg__name">{t('share.everyone')}</span>
                <span className="share-dlg__meta">{t('share.everyoneHelp')}</span>
              </span>
            </span>
            <Button size="sm" busy={busy === `add:${EVERYONE}`} disabled={locked || blocked}
              aria-describedby={blocked ? pendingId : undefined}
              onClick={() => add(EVERYONE, t('share.everyone'))}>{t('share.everyoneAdd')}</Button>
          </div>
        )}
      </section>

      <section className="share-dlg__section" aria-labelledby={`${hintId}-people`}>
        <h4 className="share-dlg__head" id={`${hintId}-people`}>{t('share.people')}</h4>
        {grants.state === 'loading' && !grants.data && (
          <p className="share-dlg__status" role="status" data-testid="share-loading"><LoadingSpinner size="sm" /> {t('share.loading')}</p>
        )}
        {grants.state === 'error' && <ShareProblem error={grants.error} onRetry={grants.reload} t={t} />}
        {grants.state === 'ready' && all.length === 0 && <p className="share-dlg__note" data-testid="share-no-grants">{t('share.none')}</p>}
        {all.length > 0 && (
          <ul className="share-dlg__list" data-testid="share-grants" aria-label={t('share.people')}>
            {all.map(g => {
              const label = nameOf(g)
              const isEveryone = g.userId === EVERYONE
              return (
                <li key={g.userId} className="share-dlg__row" data-testid="share-grant" data-user={g.userId} data-role={g.role}>
                  <span className="share-dlg__who">
                    <Icon name={isEveryone ? 'globe' : 'user'} className="share-dlg__icon" />
                    <span className="share-dlg__words">
                      <span className="share-dlg__name">{label}</span>
                      {isEveryone && <span className="share-dlg__meta">{t('share.everyoneHelp')}</span>}
                    </span>
                  </span>
                  {confirming === g.userId ? (
                    <span className="share-dlg__confirm" role="group" aria-label={t('share.revokeAria', { name: label })}>
                      <span className="share-dlg__note">{t('share.revokeConfirm', { name: label })}</span>
                      <span className="share-dlg__controls">
                        <Button size="sm" variant="danger" busy={busy === `revoke:${g.userId}`}
                          onClick={() => act(`revoke:${g.userId}`, () => client.revoke(g.userId), t('share.revoked', { name: label }))}>{t('share.revoke')}</Button>
                        <Button size="sm" variant="ghost" disabled={locked} onClick={() => setConfirming('')}>{t('actions.cancel')}</Button>
                      </span>
                    </span>
                  ) : (
                    <span className="share-dlg__controls">
                      <RoleSelect value={g.role} roles={roles} disabled={locked} label={t('share.roleOf', { name: label })}
                        onChange={(role) => act(`role:${g.userId}`, () => client.setGrant(g.userId, role), t('share.changed', { name: label, role: roleLabel(role) }))} />
                      <Button size="sm" variant="danger-ghost" icon="close" disabled={locked}
                        aria-label={t('share.revokeAria', { name: label })} title={t('share.revoke')}
                        onClick={() => setConfirming(g.userId)} />
                    </span>
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </section>

      {notice && <Callout tone="ok" role="status" testId="share-notice">{notice}</Callout>}
      {actionError && <ShareProblem error={actionError} t={t} />}
    </div>
  )
}

function ShareProblem({ error, onRetry, t }) {
  const d = describeShareError(error)
  const actions = d.retry && onRetry ? <Button size="sm" onClick={onRetry}>{t('actions.retry')}</Button> : null
  return <Callout tone="error" role="alert" actions={actions} testId="share-error">{t(d.key)}</Callout>
}
