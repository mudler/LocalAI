/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { adminInvitesApi } from '../../utils/api'
import { copyToClipboard } from '../../utils/clipboard'
import { inviteState, timeUntil, INVITE_LIFETIMES } from '../../utils/access'
import SideSheet from '../SideSheet'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button, Segmented, SkeletonRows, LoadError } from '../ui'

const inviteUrl = (code) => `${window.location.origin}/invite/${code}`

// "Invite someone": the side sheet behind the Users page's primary button. A
// link generator (pick how long it lives, make it, copy it) over the table of
// the invites that exist. A link is shown once, when it is created: the list
// keeps only the first characters of the code, so a link that was not copied
// cannot be recovered, only revoked and made again.
export default function InviteSheet({ addToast, registrationMode, onClose }) {
  const { t } = useTranslation('admin')
  const { t: ta } = useTranslation('auth')
  const [invites, setInvites] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState(null)
  const [creating, setCreating] = useState(false)
  const [hours, setHours] = useState(168)
  const [codes, setCodes] = useState({})
  const [fresh, setFresh] = useState(null)
  const [confirm, setConfirm] = useState(null)

  const fetchInvites = useCallback(async () => {
    try {
      const data = await adminInvitesApi.list()
      setInvites(Array.isArray(data) ? data : data.invites || [])
      setLoadError(null)
    } catch (err) {
      setLoadError(err)
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => { fetchInvites() }, [fetchInvites])

  const create = async () => {
    setCreating(true)
    try {
      const resp = await adminInvitesApi.create(hours)
      if (resp && resp.id && resp.code) {
        setCodes(prev => ({ ...prev, [resp.id]: resp.code }))
        setFresh({ id: resp.id, code: resp.code, copied: false })
      }
      addToast(t('invites.created'), 'success')
      fetchInvites()
    } catch (err) {
      addToast(t('invites.createFailed', { message: err.message }), 'error')
    } finally {
      setCreating(false)
    }
  }

  const copy = async (code, id) => {
    const ok = await copyToClipboard(inviteUrl(code))
    if (ok && fresh?.id === id) setFresh(f => ({ ...f, copied: true }))
    addToast(ok ? t('invites.copiedToast') : t('invites.copyFailed'), ok ? 'success' : 'error')
  }

  const revoke = async (invite) => {
    setConfirm(null)
    try {
      await adminInvitesApi.delete(invite.id)
      setInvites(prev => prev.filter(x => x.id !== invite.id))
      if (fresh?.id === invite.id) setFresh(null)
      addToast(t('invites.revoked'), 'success')
    } catch (err) {
      addToast(t('invites.revokeFailed', { message: err.message }), 'error')
    }
  }

  const stateText = { open: t('invites.state.open'), used: t('invites.state.used'), expired: t('invites.state.expired') }

  return (
    <SideSheet
      wide
      title={t('invites.title')} description={t('invites.description')}
      onClose={onClose} closeLabel={t('invites.close')} testId="invite-sheet" labelId="invite-sheet-title"
      footer={<Button onClick={onClose}>{t('invites.close')}</Button>}
    >
      <div className="iv" data-testid="invites-panel">
        <div className="iv-create">
          <span className="iv-create__label" id="iv-life">{t('invites.lifetime')}</span>
          <Segmented
            mode="radio" labelledBy="iv-life" value={hours} onChange={setHours}
            options={INVITE_LIFETIMES.map(o => ({ id: o.hours, label: t(`invites.life.${o.hours}`) }))}
          />
          <Button variant="primary" icon="plus" busy={creating} onClick={create}>{t('invites.generate')}</Button>
        </div>

        {fresh && (
          <div className="ak-reveal" role="alert" data-testid="invite-reveal">
            <p className="ak-reveal__head"><Icon name="warning" aria-hidden="true" /> {ta('invites.copyNow')}</p>
            <div className="ak-reveal__row">
              <code className="ak-reveal__key" data-testid="invite-link">{inviteUrl(fresh.code)}</code>
              <Button size="sm" icon={fresh.copied ? 'check' : 'copy'} onClick={() => copy(fresh.code, fresh.id)}>
                {fresh.copied ? ta('account.apiKeys.copied') : ta('account.apiKeys.copy')}
              </Button>
            </div>
            <div className="ak-reveal__foot">
              <span>{ta('invites.revealNote')}</span>
              <Button size="sm" variant="primary" onClick={() => setFresh(null)}>{ta('invites.done')}</Button>
            </div>
          </div>
        )}

        {!loaded ? (
          <SkeletonRows count={2} label={t('invites.loading')} />
        ) : loadError ? (
          <LoadError title={t('invites.loadFailed')} error={loadError} onRetry={fetchInvites} />
        ) : invites.length === 0 ? (
          <p className="iv-empty" data-testid="invites-empty"><strong>{t('invites.empty.title')}</strong> {t('invites.empty.body')}</p>
        ) : (
          <div className="dk-table-wrap dk-table-wrap--flat" role="region" aria-label={t('invites.region')} tabIndex={0}>
            <table className="dk-table" data-testid="invites-table">
              <caption className="dk-sr-only">{t('invites.caption')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('invites.columns.link')}</th>
                  <th scope="col" className="dk-hide-phone">{t('invites.columns.createdBy')}</th>
                  <th scope="col" className="dk-hide-phone">{t('invites.columns.usedBy')}</th>
                  <th scope="col">{t('invites.columns.expires')}</th>
                  <th scope="col"><span className="dk-sr-only">{t('invites.columns.actions')}</span></th>
                </tr>
              </thead>
              <tbody>
                {invites.map(inv => {
                  const state = inviteState(inv)
                  const code = inv.code || codes[inv.id]
                  return (
                    <tr key={inv.id} data-row data-state={state}>
                      <td>
                        <span className="dk-table-name">
                          <span className="us-state" data-state={state === 'open' ? 'active' : state === 'used' ? 'info' : 'error'}>{stateText[state]}</span>
                        </span>
                        {state === 'open' && code ? (
                          <span className="dk-table-sub iv-link">
                            <span className="dk-mono iv-link__text" title={inviteUrl(code)}>{inviteUrl(code)}</span>
                            <Button size="sm" variant="ghost" icon="copy" onClick={() => copy(code, inv.id)} title={t('invites.copyUrl')}>{t('invites.copy')}</Button>
                          </span>
                        ) : (
                          <span className="dk-table-sub dk-mono">{inv.codePrefix || '???'}…</span>
                        )}
                      </td>
                      <td className="dk-hide-phone">{inv.createdBy?.name || inv.createdBy?.id || '-'}</td>
                      <td className="dk-hide-phone">{inv.usedBy?.name || inv.usedBy?.id || '—'}</td>
                      <td>
                        {inv.expiresAt ? (
                          <>
                            <span>{state === 'used' ? '—' : timeUntil(inv.expiresAt)}</span>
                            <span className="dk-table-sub">{new Date(inv.expiresAt).toLocaleDateString()}</span>
                          </>
                        ) : '-'}
                      </td>
                      <td className="dk-num">
                        {state === 'open' && (
                          <Button size="sm" variant="danger-ghost" icon="trash" onClick={() => setConfirm(inv)} title={t('invites.revokeTitle')}>{t('invites.revoke')}</Button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {registrationMode && (
          <p className="us-foot">{ta('registration.label', { mode: registrationMode })} {ta(`registration.${registrationMode}`, { defaultValue: '' })}</p>
        )}
      </div>

      {confirm && (
        <Dialog
          role="alertdialog" title={t('invites.revokeDialog.title')} description={t('invites.revokeDialog.description')}
          onClose={() => setConfirm(null)} labelId="iv-revoke-title" closeLabel={t('invites.close')}
          foot={<>
            <Button onClick={() => setConfirm(null)}>{t('users.cancel')}</Button>
            <Button variant="danger" onClick={() => revoke(confirm)}>{t('invites.revoke')}</Button>
          </>}
        >
          <p className="us-dialog-text">{t('invites.revokeDialog.body', { prefix: confirm.codePrefix || '…' })}</p>
        </Dialog>
      )}
    </SideSheet>
  )
}
