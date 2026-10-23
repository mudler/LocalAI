/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { adminInvitesApi } from '../../utils/api'
import { copyToClipboard } from '../../utils/clipboard'
import { inviteState, timeUntil, INVITE_LIFETIMES } from '../../utils/access'
import LoadingSpinner from '../LoadingSpinner'
import Dialog from '../Dialog'
import Icon from '../Icon'

const inviteUrl = (code) => `${window.location.origin}/invite/${code}`

// Invite links. A link is shown once, when it is created: the list keeps only
// the first characters of the code, so a link that was not copied cannot be
// recovered, only revoked and made again.
export default function InvitesPanel({ addToast, registrationMode }) {
  const { t } = useTranslation('auth')
  const [invites, setInvites] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [hours, setHours] = useState(168)
  const [codes, setCodes] = useState({})
  const [fresh, setFresh] = useState(null)
  const [confirm, setConfirm] = useState(null)

  const fetchInvites = useCallback(async () => {
    setLoading(true)
    try {
      const data = await adminInvitesApi.list()
      setInvites(Array.isArray(data) ? data : data.invites || [])
    } catch (err) {
      addToast(`Failed to load invites: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast])

  useEffect(() => { fetchInvites() }, [fetchInvites])

  const create = async () => {
    setCreating(true)
    try {
      const resp = await adminInvitesApi.create(hours)
      if (resp && resp.id && resp.code) {
        setCodes(prev => ({ ...prev, [resp.id]: resp.code }))
        setFresh({ id: resp.id, code: resp.code, copied: false })
      }
      addToast('Invite link created', 'success')
      fetchInvites()
    } catch (err) {
      addToast(`Failed to create invite: ${err.message}`, 'error')
    } finally {
      setCreating(false)
    }
  }

  const copy = async (code, id) => {
    const ok = await copyToClipboard(inviteUrl(code))
    if (ok && fresh?.id === id) setFresh(f => ({ ...f, copied: true }))
    addToast(ok ? 'Invite URL copied to clipboard' : 'Failed to copy URL', ok ? 'success' : 'error')
  }

  const revoke = async (invite) => {
    setConfirm(null)
    try {
      await adminInvitesApi.delete(invite.id)
      setInvites(prev => prev.filter(x => x.id !== invite.id))
      if (fresh?.id === invite.id) setFresh(null)
      addToast('Invite revoked', 'success')
    } catch (err) {
      addToast(`Failed to revoke invite: ${err.message}`, 'error')
    }
  }

  return (
    <div className="iv" data-testid="invites-panel">
      <div className="iv-create">
        <span className="iv-create__label" id="iv-life">New invite link valid for</span>
        <div className="dk-segmented" role="radiogroup" aria-labelledby="iv-life">
          {INVITE_LIFETIMES.map(o => (
            <button key={o.hours} type="button" role="radio" className="dk-seg" aria-checked={hours === o.hours} onClick={() => setHours(o.hours)}>{o.label}</button>
          ))}
        </div>
        <button type="button" className="dk-btn dk-btn--primary" onClick={create} disabled={creating}>
          <Icon name="plus" /> {creating ? 'Creating...' : 'Generate Invite Link'}
        </button>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon" aria-label="Refresh invites" onClick={fetchInvites} disabled={loading}>
          <Icon name="refresh" spin={loading} />
        </button>
      </div>

      {fresh && (
        <div className="ak-reveal" role="alert" data-testid="invite-reveal">
          <p className="ak-reveal__head"><Icon name="warning" aria-hidden="true" /> {t('invites.copyNow')}</p>
          <div className="ak-reveal__row">
            <code className="ak-reveal__key" data-testid="invite-link">{inviteUrl(fresh.code)}</code>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => copy(fresh.code, fresh.id)}>
              <Icon name={fresh.copied ? 'check' : 'copy'} /> {fresh.copied ? t('account.apiKeys.copied') : t('account.apiKeys.copy')}
            </button>
          </div>
          <div className="ak-reveal__foot">
            <span>{t('invites.revealNote')}</span>
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => setFresh(null)}>{t('invites.done')}</button>
          </div>
        </div>
      )}

      {loading && invites.length === 0 ? (
        <div className="ak-loading"><LoadingSpinner size="lg" /></div>
      ) : invites.length === 0 ? (
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="mail" /></div>
          <h3 className="dk-empty-title">No invite links</h3>
          <p className="dk-empty-text">Generate an invite link to let someone register.</p>
        </div>
      ) : (
        <div className="dk-table-wrap dk-table-wrap--flat" role="region" aria-label="Invites" tabIndex={0}>
          <table className="dk-table" data-testid="invites-table">
            <caption className="dk-sr-only">Invite links with who made and used them</caption>
            <thead>
              <tr>
                <th scope="col">Invite Link</th>
                <th scope="col" className="dk-hide-phone">Created By</th>
                <th scope="col" className="dk-hide-phone">Used By</th>
                <th scope="col">Expires</th>
                <th scope="col"><span className="dk-sr-only">Actions</span></th>
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
                        <span className="us-state" data-state={state === 'open' ? 'active' : state === 'used' ? 'info' : 'error'}>
                          {state === 'open' ? 'Available' : state === 'used' ? 'Used' : 'Expired'}
                        </span>
                      </span>
                      {state === 'open' && code ? (
                        <span className="dk-table-sub iv-link">
                          <span className="dk-mono iv-link__text" title={inviteUrl(code)}>{inviteUrl(code)}</span>
                          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => copy(code, inv.id)} title="Copy invite URL"><Icon name="copy" /> Copy</button>
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
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setConfirm(inv)} title="Revoke invite">
                          <Icon name="trash" /> Revoke
                        </button>
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
        <p className="us-foot">{t('registration.label', { mode: registrationMode })} {t(`registration.${registrationMode}`, { defaultValue: '' })}</p>
      )}

      {confirm && (
        <Dialog
          role="alertdialog" title="Revoke Invite" description="The link stops working at once. People who already registered with it keep their accounts."
          onClose={() => setConfirm(null)} labelId="iv-revoke-title" closeLabel="Close"
          foot={<>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setConfirm(null)}>Cancel</button>
            <button type="button" className="dk-btn dk-btn--danger" onClick={() => revoke(confirm)}>Revoke</button>
          </>}
        >
          <p className="us-dialog-text">Revoke the invite link that starts with <span className="dk-mono">{confirm.codePrefix || '…'}</span>?</p>
        </Dialog>
      )}
    </div>
  )
}
