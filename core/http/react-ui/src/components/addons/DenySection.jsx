/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import Icon from '../Icon'
import HostDialog from './HostDialog'
import { denyRows, pageOps, validateDenyDraft, withDenyRule } from './hostGrantsState'

// "Never allow": operations an add-on may never hold, whatever else grants
// them. Rules from the deployment's policy file or flags are shown locked and
// cannot be removed here. Rules made here apply at once. Adding one first asks
// the owner what it would take away, and says which add-ons would stop.
export default function DenySection({ overview, api, locked, busy, run }) {
  const { t } = useTranslation('addons')
  const [draft, setDraft] = useState({ target: '*', entry: '' })
  const [error, setError] = useState('')
  const [check, setCheck] = useState(null)
  const listId = useId()
  const files = denyRows(overview.deny.file, 'file')
  const ui = denyRows(overview.deny.ui, 'ui')
  const known = [...new Set(overview.addons.flatMap(a => pageOps(a).map(o => o.name)))]
  const families = [...new Set(known.map(n => `${n.split('.')[0]}.`))]
  const targetLabel = (target) => (target === '*' ? t('hostAccess.deny.allAddons') : (overview.addons.find(a => a.id === target)?.title || target))
  const entryLabel = (entry) => (entry === '*' ? t('hostAccess.deny.everything') : entry)

  const submitRule = async (rule, add) => {
    const deny = withDenyRule(overview.deny.ui, rule, add)
    if (!add) { await run(() => api.setDeny({ revision: overview.revision, deny }), t('hostAccess.deny.removed')); return }
    setError('')
    let affects = []
    try {
      const dry = await api.setDeny({ revision: overview.revision, deny, dryRun: true })
      affects = Array.isArray(dry && dry.affects) ? dry.affects : []
    } catch (e) {
      setError(t('hostAccess.deny.checkFailed'))
      return
    }
    setCheck({ rule, deny, affects })
  }
  const add = (e) => {
    e.preventDefault()
    const entry = draft.entry.trim().toLowerCase()
    if (validateDenyDraft({ target: draft.target, entry })) { setError(t('hostAccess.deny.invalid')); return }
    submitRule({ target: draft.target, entry }, true)
  }
  const confirmAdd = async () => {
    const ok = await run(() => api.setDeny({ revision: overview.revision, deny: check.deny }), t('hostAccess.deny.added'))
    if (ok) { setCheck(null); setDraft(d => ({ ...d, entry: '' })) }
  }

  return (
    <section className="ad-host__section" aria-labelledby="host-deny-title" data-testid="host-deny">
      <h2 id="host-deny-title" className="ad-host__heading">{t('hostAccess.deny.title')}</h2>
      <p className="ad-host__intro">{t('hostAccess.deny.intro')}</p>
      {files.length + ui.length === 0 && <p className="ad-quiet" data-testid="deny-empty">{t('hostAccess.deny.none')}</p>}
      <ul className="ad-deny-list">
        {files.map(r => (
          <li key={r.key} className="ad-deny-row" data-testid="deny-rule" data-origin="file">
            <Icon name="lock" aria-hidden="true" />
            <span className="ad-deny-row__text"><strong>{entryLabel(r.entry)}</strong> <span className="ad-quiet">{targetLabel(r.target)}</span></span>
            <span className="ad-quiet">{t('hostAccess.deny.fromFile')}</span>
          </li>
        ))}
        {ui.map(r => (
          <li key={r.key} className="ad-deny-row" data-testid="deny-rule" data-origin="ui">
            <Icon name="ban" aria-hidden="true" />
            <span className="ad-deny-row__text"><strong>{entryLabel(r.entry)}</strong> <span className="ad-quiet">{targetLabel(r.target)}</span></span>
            {!locked && <Button size="sm" variant="ghost" disabled={busy} onClick={() => submitRule(r, false)} aria-label={t('hostAccess.deny.removeNamed', { entry: entryLabel(r.entry), target: targetLabel(r.target) })}>{t('hostAccess.deny.remove')}</Button>}
          </li>
        ))}
      </ul>
      {!locked && (
        <form className="ad-deny-form" onSubmit={add} data-testid="deny-form" noValidate>
          <label className="ad-field">
            <span>{t('hostAccess.deny.target')}</span>
            <select className="dk-select" value={draft.target} onChange={(e) => setDraft(d => ({ ...d, target: e.target.value }))} data-testid="deny-target">
              <option value="*">{t('hostAccess.deny.allAddons')}</option>
              {overview.addons.map(a => <option key={a.id} value={a.id}>{a.title}</option>)}
            </select>
          </label>
          <label className="ad-field">
            <span>{t('hostAccess.deny.entry')}</span>
            <input className="dk-input" list={listId} value={draft.entry} onChange={(e) => setDraft(d => ({ ...d, entry: e.target.value }))} placeholder="egress." autoComplete="off" spellCheck={false} data-testid="deny-entry" aria-invalid={error ? 'true' : undefined} />
            <datalist id={listId}>
              <option value="*" />
              {families.map(f => <option key={f} value={f} />)}
              {known.map(n => <option key={n} value={n} />)}
            </datalist>
          </label>
          <Button type="submit" disabled={busy || !draft.entry.trim()} data-testid="deny-add">{t('hostAccess.deny.add')}</Button>
        </form>
      )}
      {!locked && <p className="ad-quiet">{t('hostAccess.deny.hint')}</p>}
      {error && <Callout tone="error" role="alert" className="ad-callout" testId="deny-error">{error}</Callout>}
      {check && (
        <HostDialog
          testId="deny-confirm-dialog"
          title={t('hostAccess.deny.confirmTitle', { entry: entryLabel(check.rule.entry) })}
          onClose={() => setCheck(null)}
          busy={busy}
          actions={(
            <>
              <Button onClick={() => setCheck(null)} disabled={busy}>{t('catalog.cancel')}</Button>
              <Button variant="primary" busy={busy} disabled={busy} onClick={confirmAdd} data-autofocus="true" data-testid="deny-confirm">{t('hostAccess.deny.confirmAction')}</Button>
            </>
          )}
        >
          <p>{t('hostAccess.deny.confirmBody', { target: targetLabel(check.rule.target) })}</p>
          {check.affects.length === 0
            ? <p className="ad-quiet" data-testid="deny-affects-none">{t('hostAccess.deny.affectsNone')}</p>
            : (
              <ul className="ad-plain-list" data-testid="deny-affects">
                {check.affects.map(a => (
                  <li key={a.id} data-will-stop={a.willStop ? 'true' : undefined}>
                    <strong>{targetLabel(a.id)}</strong> <span className="ad-quiet">{(a.loses || []).join(', ')}</span>
                    {a.willStop && <> <span className="ad-warn-text">{t('hostAccess.deny.willStop')}</span></>}
                  </li>
                ))}
              </ul>
            )}
        </HostDialog>
      )}
    </section>
  )
}
